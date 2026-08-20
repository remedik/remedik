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
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/api/v1alpha1"
)

// DefaultBindAddress is the address the dashboard listens on when it is
// enabled without an explicit one. It is not a default in the sense of
// "on": an empty address means the dashboard is not served at all.
const DefaultBindAddress = ":8082"

// remediationsPath is the list page. Every filter control links to it, and
// every panel on the overview that counts something links to the view of it.
const remediationsPath = "/remediations"

// namespacesPath is where remediation is going badly, if it is.
const namespacesPath = "/namespaces"

// pageSize is how many executions the list draws at once.
//
// History is already bounded by pruning, so this is not what keeps the page
// finite — it is what keeps rendering cheap. A dashboard that could draw
// thousands of rows would be a way to make the operator slow at its real
// job, which is remediation. Everything beyond it is a page away, not
// missing.
const pageSize = 100

// recentLimit is how many the overview shows before sending the reader to
// the list. The front page answers "is anything wrong now?", and a long
// table is not how that question is answered.
const recentLimit = 8

// activityHours is the span of the overview's activity panel: one bar per
// hour over a day, which is long enough to show a night and short enough
// that a bar means something.
const activityHours = 24

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

	// Posture is what remedik is allowed to do, and where, so the pages can
	// say so rather than leaving a reader to infer it from the records. Its
	// zero value is dry-run everywhere, which is the safe reading of
	// "nothing was configured".
	Posture Posture

	// Paused reports whether the kill switch is on, so the pages say so.
	//
	// A function rather than a value: it is flipped at runtime, and a dashboard
	// that had to be restarted to notice would be the one place an operator
	// checks after stopping remediation and the one place still claiming it is
	// running. Optional; nil means never paused.
	Paused func() (bool, string)

	// Cluster names the cluster this operator watches. Optional, and shown
	// in the header when set.
	//
	// It is a label, not a filter. remedik sees one cluster because it runs
	// in one, so a control offering a choice of clusters would be offering
	// a choice of one — filtering across clusters needs the hub/spoke work.
	// What this solves is real today and smaller: three port-forwards on
	// three clusters produce three identical-looking tabs.
	Cluster string

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
	posture   Posture
	paused    func() (bool, string)
	cluster   string
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
		posture:   cfg.Posture,
		paused:    cfg.Paused,
		cluster:   cfg.Cluster,
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
	// Both spellings serve the list. Registering only the trailing-slash
	// form makes the mux answer "/remediations" with a 307 to it, so every
	// link on every page would cost a redirect.
	h.mux.HandleFunc(remediationsPath, h.remediations)
	h.mux.HandleFunc(remediationsPath+"/{$}", h.remediations)
	h.mux.HandleFunc("/remediations/{name}", h.remediation)
	h.mux.HandleFunc(namespacesPath, h.namespaces)
	h.mux.HandleFunc(namespacesPath+"/{$}", h.namespaces)
	h.mux.HandleFunc(approvalsPath, h.approvals)
	h.mux.HandleFunc(approvalsPath+"/{$}", h.approvals)
	h.mux.HandleFunc("/strategies", h.strategies)
	h.mux.Handle("/static/", http.StripPrefix("/static/", staticHandler()))
	h.mux.HandleFunc("/", h.notFound)

	return h, nil
}

// Mux returns an http.Handler serving the dashboard, so it can run on its
// own listener.
func (h *Handler) Mux() http.Handler { return h }

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A bug in a view builder must not become an empty reply. net/http
	// recovers the goroutine, so the operator survives either way, but the
	// reader gets a closed connection and no idea why — and this is the page
	// somebody opens when something is already wrong. It has happened once:
	// an empty filter result made a row loop start at index -1.
	//
	// Pages are rendered into a buffer before anything is written, so a
	// panic during rendering has not yet sent a byte and 500 is still
	// available.
	defer func() {
		if recovered := recover(); recovered != nil {
			h.logger.Error("panic serving a dashboard page",
				"path", r.URL.Path, "panic", recovered, "stack", string(debug.Stack()))
			h.fail(w, r, http.StatusInternalServerError,
				"Something went wrong rendering this page",
				"The operator logged the details. Every other page still works, and "+
					"nothing about your cluster has changed: the dashboard only reads.")
		}
	}()

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
			// 'self', not 'none'. The filter's select is a GET form posting to
			// this same origin, and 'none' blocked it — silently, in the
			// console, with the control looking merely unresponsive. It was
			// reported as "the dropdown does nothing" four times before a
			// browser was driven to read the console.
			//
			// 'self' is still the whole grant: the page cannot submit anywhere
			// but back to the dashboard.
			"connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	head.Set("X-Content-Type-Options", "nosniff")
	head.Set("X-Frame-Options", "DENY")
	head.Set("Referrer-Policy", "no-referrer")
}

// --------------------------------------------------------------------------
// Pages
// --------------------------------------------------------------------------

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()

	remediations, err := h.listRemediations(ctx)
	if err != nil {
		h.unavailable(w, r, "list remediations", err)
		return
	}
	strategies, err := h.listStrategies(ctx)
	if err != nil {
		h.unavailable(w, r, "list strategies", err)
		return
	}

	view := buildOverview(remediations, strategies, h.viewPosture(), h.now())
	view.Page = h.page("Overview", navOverview)
	view.Waiting = awaiting(remediations)
	h.render(w, r, overviewTemplate, view)
}

// remediations is the list: every execution, the filters, and the counts.
//
// It exists as its own page because "is anything wrong right now?" and "what
// happened to payments last Tuesday?" are different questions, and the front
// page answering both answered neither well.
func (h *Handler) remediations(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()

	remediations, err := h.listRemediations(ctx)
	if err != nil {
		h.unavailable(w, r, "list remediations", err)
		return
	}

	query := r.URL.Query()
	view := buildRemediations(
		remediations, ParseFilter(query), ParseSort(query), ParsePage(query), h.now())
	view.Page = h.page("Remediations", navRemediations)
	view.Waiting = awaiting(remediations)
	h.render(w, r, remediationsTemplate, view)
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
	view.Page = h.page(rem.Name, navRemediations)
	h.render(w, r, remediationTemplate, view)
}

// namespaces answers "where is this going badly".
//
// It reads the same records the list page does. There is no new permission
// and no new read: the page is an arrangement of what the dashboard already
// had, which is why it costs nothing to serve.
func (h *Handler) namespaces(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()

	remediations, err := h.listRemediations(ctx)
	if err != nil {
		h.unavailable(w, r, "list remediations", err)
		return
	}

	view := buildNamespaces(
		remediations, h.viewPosture(), h.now(), ParseNamespaceFilter(r.URL.Query()))
	view.Page = h.page("Namespaces", navNamespaces)
	view.Waiting = awaiting(remediations)
	h.render(w, r, namespacesTemplate, view)
}

// approvals is the queue with a clock on it.
//
// It reads what every other page reads. The strategies are here for the empty
// case only: "nothing is waiting" and "no strategy asks for approval, so
// nothing ever will" are the same empty page and two different situations.
func (h *Handler) approvals(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()

	remediations, err := h.listRemediations(ctx)
	if err != nil {
		h.unavailable(w, r, "list remediations", err)
		return
	}
	strategies, err := h.listStrategies(ctx)
	if err != nil {
		h.unavailable(w, r, "list strategies", err)
		return
	}

	view := buildApprovals(remediations, strategies, h.now())
	view.Page = h.page("Approvals", navApprovals)
	view.Page.Waiting = len(view.Queue)
	h.render(w, r, approvalsTemplate, view)
}

func (h *Handler) strategies(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()

	strategies, err := h.listStrategies(ctx)
	if err != nil {
		h.unavailable(w, r, "list strategies", err)
		return
	}
	remediations, err := h.listRemediations(ctx)
	if err != nil {
		h.unavailable(w, r, "list remediations", err)
		return
	}

	// Unknown values show everything, for the same reason the record filter
	// keeps them: a mistyped parameter should be a wide answer, not a 400 on
	// a URL somebody pasted into a channel.
	show := r.URL.Query().Get("show")
	if show != ShowNotReady && show != ShowDisabled {
		show = ""
	}

	view := buildStrategies(strategies, remediations, h.now(), show)
	view.Page = h.page("Strategies", navStrategies)
	view.Waiting = awaiting(remediations)
	h.render(w, r, strategiesTemplate, view)
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	h.fail(w, r, http.StatusNotFound,
		"Page not found",
		fmt.Sprintf("The dashboard serves the overview, the remediations, the namespaces, "+
			"the approvals queue, the strategies, and one page per remediation. "+
			"There is nothing at %s.", r.URL.Path))
}

// --------------------------------------------------------------------------
// Reading
// --------------------------------------------------------------------------

// readOnly is passed to every List this package makes.
//
// The manager's cache DeepCopies every object into the list so that a caller
// which mutates cannot corrupt it. That copy is the single largest cost of
// serving a page: at ten thousand records it is around thirteen megabytes and
// a hundred and thirty thousand allocations, paid again on every render — and
// the auto-refresh means every open tab pays it every ten seconds.
//
// This package never writes to a listed object. It is constructed from a
// client.Reader, so it holds no method that could, and the only mutation
// anywhere near the data is sortNewestFirst, which reorders the slice this
// package owns rather than touching an object. TestTheDashboardNeverMutates
// holds that, because the option is only safe while it stays true.
var readOnly = []client.ListOption{client.UnsafeDisableDeepCopy}

// listRemediations reads every record in the operator's namespace.
//
// Four pages need exactly this, and they needed it as four copies of the same
// List call, error wrap and message.
func (h *Handler) listRemediations(ctx context.Context) ([]v1alpha1.Remediation, error) {
	var list v1alpha1.RemediationList
	opts := append([]client.ListOption{client.InNamespace(h.namespace)}, readOnly...)
	if err := h.reader.List(ctx, &list, opts...); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// awaiting counts the records waiting for a person, for the badge every page
// carries. One pass over a list the page already had, so no page pays a read
// for it.
func awaiting(remediations []v1alpha1.Remediation) int {
	var waiting int
	for i := range remediations {
		if remediations[i].Status.State == v1alpha1.RemediationStateAwaitingApproval {
			waiting++
		}
	}
	return waiting
}

// listStrategies reads every strategy. They are cluster-scoped.
func (h *Handler) listStrategies(ctx context.Context) ([]v1alpha1.RemediationStrategy, error) {
	var list v1alpha1.RemediationStrategyList
	if err := h.reader.List(ctx, &list, readOnly...); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// --------------------------------------------------------------------------
// Rendering
// --------------------------------------------------------------------------

// page builds the fields every page's chrome needs.
func (h *Handler) page(title, nav string) Page {
	paused, pauseReason := h.pauseState()
	posture := h.posture
	posture.Paused = paused

	return Page{
		Paused:      paused,
		PauseReason: pauseReason,
		Title:       title,
		Nav:         nav,
		DryRun:      h.posture.DryRun,
		Posture:     h.posture,
		Namespace:   h.namespace,
		Cluster:     h.cluster,
		Version:     h.version,
		Asset:       assetVersion,
		RenderedAt:  FormatClock(h.now()),
	}
}

// pauseState reads the kill switch, or reports running when none is wired.
func (h *Handler) pauseState() (bool, string) {
	if h.paused == nil {
		return false, ""
	}
	return h.paused()
}

// viewPosture is the posture the page builders see: the configured one, with
// the kill switch folded in. Resolved once here so no builder has to remember.
func (h *Handler) viewPosture() Posture {
	paused, _ := h.pauseState()
	posture := h.posture
	posture.Paused = paused
	return posture
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
