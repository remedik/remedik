// Package gateway implements the HTTP receiver for Alertmanager webhooks.
//
// It authenticates the sender, decodes the delivery into normalized alerts
// and hands each one to a Sink. It deliberately holds no remediation logic:
// deciding what to do with an alert belongs to the engine.
//
// Response codes follow the contract in the alert-gateway spec:
//
//	401 — missing or wrong bearer token (body is never read)
//	404 — unknown path
//	405 — method other than POST
//	413 — body over the configured limit
//	400 — malformed or unsupported payload
//	200 — understood delivery, even when nothing matches
//
// The last rule matters operationally: Alertmanager retries non-2xx
// responses, so returning an error for "no strategy matched" would turn a
// normal condition into a retry storm.
package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ratyx/remedik/internal/alert"
)

// DefaultPath is the webhook path Alertmanager posts to.
const DefaultPath = "/webhooks/alertmanager"

// DefaultMaxBodyBytes caps a single delivery. Large groups are a handful of
// kilobytes; the limit exists so a hostile or broken sender cannot exhaust
// memory.
const DefaultMaxBodyBytes int64 = 1 << 20 // 1 MiB

// Sink consumes normalized alerts. Implementations must not block for long:
// the gateway calls Sink before responding, so a slow sink becomes sender
// backpressure.
type Sink interface {
	// Consume handles one alert delivery. It returns no error because the
	// HTTP response must not depend on downstream remediation outcomes.
	Consume(alerts []alert.Alert)
}

// SinkFunc adapts a function to the Sink interface.
type SinkFunc func(alerts []alert.Alert)

// Consume implements Sink.
func (f SinkFunc) Consume(alerts []alert.Alert) { f(alerts) }

// Config configures a Handler. Only Sink is required.
type Config struct {
	// Sink receives the decoded alerts. Required.
	Sink Sink
	// Path is the webhook path; defaults to DefaultPath.
	Path string
	// Token is the bearer token senders must present. An empty token
	// disables authentication, which is only appropriate for local
	// development; New logs a warning in that case.
	Token string
	// MaxBodyBytes caps the request body; defaults to DefaultMaxBodyBytes.
	MaxBodyBytes int64
	// Metrics receives telemetry; defaults to NopRecorder.
	Metrics Recorder
	// Logger is used for request logging; defaults to slog.Default().
	Logger *slog.Logger
}

// Handler serves the Alertmanager webhook endpoint.
type Handler struct {
	sink         Sink
	path         string
	token        []byte
	maxBodyBytes int64
	metrics      Recorder
	logger       *slog.Logger
}

// New validates cfg and returns a Handler.
func New(cfg Config) (*Handler, error) {
	if cfg.Sink == nil {
		return nil, errors.New("gateway: Sink is required")
	}

	h := &Handler{
		sink:         cfg.Sink,
		path:         cfg.Path,
		token:        []byte(cfg.Token),
		maxBodyBytes: cfg.MaxBodyBytes,
		metrics:      cfg.Metrics,
		logger:       cfg.Logger,
	}
	if h.path == "" {
		h.path = DefaultPath
	}
	if h.maxBodyBytes <= 0 {
		h.maxBodyBytes = DefaultMaxBodyBytes
	}
	if h.metrics == nil {
		h.metrics = NopRecorder{}
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	if len(h.token) == 0 {
		h.logger.Warn("gateway authentication is disabled: any sender can submit alerts",
			"path", h.path)
	}

	return h, nil
}

// Path returns the path this handler serves.
func (h *Handler) Path() string { return h.path }

// Mux returns an http.ServeMux serving only this handler's path, so the
// gateway can run on its own listener.
func (h *Handler) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(h.path, h)
	return mux
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != h.path {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "only POST is supported")
		return
	}

	// Authentication happens before the body is touched, so an
	// unauthenticated sender cannot make remedik do any parsing work.
	if !h.authorized(r) {
		h.metrics.Unauthorized()
		h.logger.Warn("rejected unauthenticated alert delivery",
			"remote_addr", r.RemoteAddr, "path", r.URL.Path)
		w.Header().Set("WWW-Authenticate", `Bearer realm="remedik"`)
		writeError(w, http.StatusUnauthorized, "missing or invalid bearer token")
		return
	}

	body := http.MaxBytesReader(w, r.Body, h.maxBodyBytes)
	result, err := alert.ParseWebhook(body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.metrics.IngestError(reasonBodyTooLarge)
			h.logger.Warn("rejected oversized alert delivery",
				"limit_bytes", h.maxBodyBytes, "remote_addr", r.RemoteAddr)
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("payload exceeds %d bytes", h.maxBodyBytes))
			return
		}

		h.metrics.IngestError(reasonMalformedPayload)
		h.logger.Error("rejected malformed alert delivery",
			"err", err, "remote_addr", r.RemoteAddr)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if result.TruncatedAlerts > 0 {
		h.metrics.AlertsTruncated(result.TruncatedAlerts)
		h.logger.Warn("sender truncated this delivery; remediation may be acting on partial information",
			"truncated", result.TruncatedAlerts, "received", len(result.Alerts))
	}

	h.metrics.AlertsReceived(len(result.Alerts))
	h.logger.Debug("accepted alert delivery", "count", len(result.Alerts))
	h.sink.Consume(result.Alerts)

	writeJSON(w, http.StatusOK, map[string]any{"received": len(result.Alerts)})
}

// authorized reports whether the request carries the configured bearer
// token. The comparison is constant-time so that a wrong token cannot be
// discovered by timing the response.
func (h *Handler) authorized(r *http.Request) bool {
	if len(h.token) == 0 {
		return true
	}

	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	presented := strings.TrimSpace(header[len(prefix):])

	return subtle.ConstantTimeCompare([]byte(presented), h.token) == 1
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The response is tiny and the client is a webhook sender that ignores
	// the body; a write failure has no recovery path beyond logging, which
	// the caller already does for the request.
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
