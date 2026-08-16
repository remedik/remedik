// Package dashboard serves the read-only web UI.
//
// It answers, in a browser, the two questions remedik exists to answer:
// "what would this have done?" during a dry-run trial, and "why did nothing
// happen?" during an incident. Three pages — an overview, one page per
// execution and the list of strategies — rendered by the operator itself.
//
// Read-only is structural, not a policy:
//
//   - the handler is built from a [client.Reader], so there is no write
//     method to call by accident;
//   - every request that is not GET or HEAD is answered 405 before routing,
//     so a new page cannot accidentally accept one;
//   - nothing here takes a parameter that reaches the cluster except a
//     resource name to read.
//
// Response codes:
//
//	401 — a token is configured and the request did not carry it
//	404 — unknown path, or a remediation that does not exist
//	405 — any method other than GET or HEAD
//	503 — the Kubernetes API could not be read
//	200 — a rendered page
package dashboard

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/api/v1alpha1"
)

// DefaultBindAddress is the address the dashboard listens on when it is
// enabled without an explicit one. It is not a default in the sense of
// "on": an empty address means the dashboard is not served at all.
const DefaultBindAddress = ":8082"

// recentLimit caps the executions listed on the overview.
//
// History is already bounded by pruning, so this is not what keeps the page
// finite — it is what keeps rendering cheap. A dashboard that could draw
// thousands of rows would be a way to make the operator slow at its real
// job, which is remediation.
const recentLimit = 50

// readTimeout bounds a single page's reads. The manager's cache answers
// from memory, so anything slower than this means the cache is not there.
const readTimeout = 10 * time.Second

// Reader is the slice of the Kubernetes client the dashboard is allowed to
// use. It is deliberately narrower than client.Client: with no write
// methods in the type, no page can grow one.
type Reader interface {
	Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error
	List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
}

// Config configures a Handler. Reader, Namespace and Logger are required.
type Config struct {
	// Reader reads Remediation and RemediationStrategy resources. It is a
	// read-only interface on purpose; see the package comment.
	Reader Reader

	// Namespace is where Remediation records live — the operator's own
	// namespace, which is also the only one its RBAC covers.
	Namespace string

	// Token is the bearer token a request must present. Empty disables
	// authentication, which New warns about: the dashboard discloses alert
	// labels, namespaces and workload names.
	Token string

	// DryRun reports whether the operator is running in dry-run, so the
	// pages can say so rather than leaving a reader to infer it from the
	// records.
	DryRun bool

	// Version is shown in the footer, so a screenshot says which build
	// produced it.
	Version string

	// Logger is required.
	Logger *slog.Logger

	// Now supplies the current time; tests inject a fixed clock. Defaults
	// to time.Now.
	Now func() time.Time
}

// Handler serves the dashboard.
type Handler struct {
	reader    Reader
	namespace string
	token     []byte
	dryRun    bool
	version   string
	logger    *slog.Logger
	now       func() time.Time
	mux       *http.ServeMux
}

// New validates cfg and returns a Handler.
func New(cfg Config) (*Handler, error) {
	if cfg.Reader == nil {
		return nil, errors.New("dashboard: Reader is required")
	}
	if cfg.Namespace == "" {
		return nil, errors.New("dashboard: Namespace is required")
	}
	if cfg.Logger == nil {
		return nil, errors.New("dashboard: Logger is required")
	}

	h := &Handler{
		reader:    cfg.Reader,
		namespace: cfg.Namespace,
		token:     []byte(cfg.Token),
		dryRun:    cfg.DryRun,
		version:   cfg.Version,
		logger:    cfg.Logger,
		now:       cfg.Now,
	}
	if h.now == nil {
		h.now = time.Now
	}
	if len(h.token) == 0 {
		h.logger.Warn("dashboard authentication is disabled: anything that can reach the port " +
			"can read alert labels, namespaces and workload names")
	}

	h.mux = http.NewServeMux()
	h.mux.HandleFunc("/{$}", h.overview)
	h.mux.HandleFunc("/strategies", h.strategies)
	h.mux.HandleFunc("/remediations/{name}", h.remediation)
	// "/remediations" and "/remediations/" are not pages of their own; the
	// overview is the list. Redirecting is friendlier than a 404 for a URL
	// someone shortened by hand.
	h.mux.HandleFunc("/remediations/{$}", redirectToOverview)
	h.mux.Handle("/static/", http.StripPrefix("/static/", staticHandler()))
	h.mux.HandleFunc("/", h.notFound)

	return h, nil
}

// Mux returns an http.Handler serving the dashboard, so it can run on its
// own listener.
func (h *Handler) Mux() http.Handler { return h }

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The method allowlist runs before routing, so it covers every path —
	// including ones added later, and ones that do not exist. This is the
	// second layer of the read-only guarantee; the first is that the
	// handler holds no client that can write.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		h.fail(w, r, http.StatusMethodNotAllowed,
			"The dashboard is read-only",
			"It serves GET and HEAD. Changing anything is done with kubectl.")
		return
	}

	if !h.authorized(r) {
		h.logger.Warn("rejected unauthenticated dashboard request",
			"remote_addr", r.RemoteAddr, "path", r.URL.Path)
		// Basic is offered alongside Bearer because a browser can be told
		// to send Basic and cannot be told to send Bearer. See
		// (*Handler).authorized.
		w.Header().Set("WWW-Authenticate", `Basic realm="remedik", charset="UTF-8"`)
		h.fail(w, r, http.StatusUnauthorized,
			"Authentication required",
			"Present the dashboard token: as a bearer token, or as the password "+
				"in the browser prompt (the username is ignored).")
		return
	}

	securityHeaders(w)
	h.mux.ServeHTTP(w, r)
}

// authorized reports whether the request carries the configured token.
//
// Two ways to present it, one token:
//
//	Authorization: Bearer <token>   — proxies, curl, anything scripted
//	Authorization: Basic <base64>   — the browser's own prompt, any username
//
// The second exists because the documented way to reach the dashboard is a
// port-forward and a browser, and a browser cannot be asked to send a
// bearer header. Refusing to accept Basic would not make the dashboard
// safer; it would make an authenticated dashboard unusable by the person it
// is for, which is the reliable way to end up with an unauthenticated one.
//
// Both comparisons are constant-time, so a wrong token cannot be discovered
// by timing the response.
func (h *Handler) authorized(r *http.Request) bool {
	if len(h.token) == 0 {
		return true
	}

	header := r.Header.Get("Authorization")
	scheme, rest, found := strings.Cut(header, " ")
	if !found {
		return false
	}
	rest = strings.TrimSpace(rest)

	switch {
	case strings.EqualFold(scheme, "Bearer"):
		return h.tokenMatches(rest)
	case strings.EqualFold(scheme, "Basic"):
		decoded, err := base64.StdEncoding.DecodeString(rest)
		if err != nil {
			return false
		}
		// The username carries no meaning here: there is one credential,
		// and it is the password. Naming a user would imply an identity
		// model the dashboard does not have.
		_, password, ok := strings.Cut(string(decoded), ":")
		return ok && h.tokenMatches(password)
	default:
		return false
	}
}

func (h *Handler) tokenMatches(presented string) bool {
	return subtle.ConstantTimeCompare([]byte(presented), h.token) == 1
}

// securityHeaders pins the page to what the operator itself serves.
//
// The content security policy is the machine-readable form of a promise the
// spec makes in prose: a cluster with no outbound internet access renders
// the dashboard identically, because the page is not allowed to ask for
// anything from anywhere else.
func securityHeaders(w http.ResponseWriter) {
	head := w.Header()
	head.Set("Content-Security-Policy",
		"default-src 'none'; style-src 'self'; script-src 'self'; img-src 'self' data:; "+
			"connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	head.Set("X-Content-Type-Options", "nosniff")
	head.Set("X-Frame-Options", "DENY")
	head.Set("Referrer-Policy", "no-referrer")
}

func redirectToOverview(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --------------------------------------------------------------------------
// Pages
// --------------------------------------------------------------------------

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()

	var remediations v1alpha1.RemediationList
	if err := h.reader.List(ctx, &remediations, client.InNamespace(h.namespace)); err != nil {
		h.unavailable(w, r, "list remediations", err)
		return
	}

	var strategies v1alpha1.RemediationStrategyList
	if err := h.reader.List(ctx, &strategies); err != nil {
		h.unavailable(w, r, "list strategies", err)
		return
	}

	view := buildOverview(remediations.Items, strategies.Items, h.dryRun, h.now())
	view.Page = h.page("Overview", navOverview)
	h.render(w, r, overviewTemplate, view)
}

func (h *Handler) remediation(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()

	var rem v1alpha1.Remediation
	key := client.ObjectKey{Namespace: h.namespace, Name: name}
	if err := h.reader.Get(ctx, key, &rem); err != nil {
		if apierrors.IsNotFound(err) {
			h.fail(w, r, http.StatusNotFound,
				"No such remediation",
				fmt.Sprintf("There is no Remediation named %q in namespace %s. "+
					"Terminal records are pruned once a strategy has enough of them.",
					name, h.namespace))
			return
		}
		h.unavailable(w, r, "read remediation", err)
		return
	}

	view := buildRemediation(&rem, h.now())
	view.Page = h.page(rem.Name, navOverview)
	h.render(w, r, remediationTemplate, view)
}

func (h *Handler) strategies(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()

	var strategies v1alpha1.RemediationStrategyList
	if err := h.reader.List(ctx, &strategies); err != nil {
		h.unavailable(w, r, "list strategies", err)
		return
	}

	var remediations v1alpha1.RemediationList
	if err := h.reader.List(ctx, &remediations, client.InNamespace(h.namespace)); err != nil {
		h.unavailable(w, r, "list remediations", err)
		return
	}

	view := buildStrategies(strategies.Items, remediations.Items, h.now())
	view.Page = h.page("Strategies", navStrategies)
	h.render(w, r, strategiesTemplate, view)
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	h.fail(w, r, http.StatusNotFound,
		"Page not found",
		fmt.Sprintf("The dashboard serves the overview, the strategies list and one page "+
			"per remediation. There is nothing at %s.", r.URL.Path))
}

// --------------------------------------------------------------------------
// Rendering
// --------------------------------------------------------------------------

// page builds the fields every page's chrome needs.
func (h *Handler) page(title, nav string) Page {
	return Page{
		Title:      title,
		Nav:        nav,
		DryRun:     h.dryRun,
		Namespace:  h.namespace,
		Version:    h.version,
		Asset:      assetVersion,
		RenderedAt: FormatClock(h.now()),
	}
}

// render writes a page, buffering it first.
//
// A template that fails halfway through would otherwise leave a truncated
// page behind a 200 — the one failure mode that looks like success. The
// buffer costs a few kilobytes and removes it.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, tmpl *template.Template, data any) {
	buf := newBuffer()
	defer releaseBuffer(buf)

	if err := tmpl.ExecuteTemplate(buf, layoutName, data); err != nil {
		h.logger.Error("could not render a dashboard page",
			"path", r.URL.Path, "err", err)
		// The page is already lost; what matters is that the reader is told
		// so rather than shown half of one.
		http.Error(w, "the dashboard could not render this page", http.StatusInternalServerError)
		return
	}

	h.writePage(w, r, http.StatusOK, buf.Bytes())
}

// writePage sends a rendered page.
//
// Content-Length is set explicitly so that a HEAD answers the same length a
// GET would send — the point of HEAD — and so a GET is not chunked for no
// reason.
func (h *Handler) writePage(w http.ResponseWriter, r *http.Request, status int, page []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(page)))
	// Pages are a live view of cluster state. Caching one would show an
	// operator a stale incident, which is worse than a re-render.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(page); err != nil {
		h.logger.Debug("dashboard response write failed", "path", r.URL.Path, "err", err)
	}
}

// unavailable reports that the cluster could not be read. The detail is
// shown rather than hidden: everything the dashboard can say is already
// behind whatever authentication is configured, and "it did not work" with
// no reason is the message that wastes an operator's incident.
func (h *Handler) unavailable(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.logger.Error("dashboard could not read from the API", "operation", what, "err", err)
	h.fail(w, r, http.StatusServiceUnavailable,
		"Cannot read from the cluster",
		fmt.Sprintf("The operator could not %s: %v", what, err))
}

// fail renders an error page. It is rendered with the same layout as
// everything else, because an error page that loses the navigation is an
// error page that ends the visit.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, status int, title, detail string) {
	// Errors can be produced before routing, so the security headers may not
	// be set yet. Setting them twice is harmless; not setting them is not.
	securityHeaders(w)

	buf := newBuffer()
	defer releaseBuffer(buf)

	view := ErrorView{
		Page:   h.page(title, navNone),
		Status: status,
		Title:  title,
		Detail: detail,
	}
	if err := errorTemplate.ExecuteTemplate(buf, layoutName, view); err != nil {
		h.logger.Error("could not render the dashboard error page", "err", err)
		http.Error(w, title, status)
		return
	}

	h.writePage(w, r, status, buf.Bytes())
}
