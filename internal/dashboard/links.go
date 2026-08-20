package dashboard

// Links out of the dashboard.
//
// A fingerprint printed as text is a dead end. The reader correlating this
// record with Grafana, Loki or Alertmanager is doing it by hand, in another
// tab, at the moment they have least patience for it — and remedik knows the
// namespace, the target, the alert and the window, which is everything those
// links need.
//
// So the operator is told, once, what to link to. It is configuration rather
// than a built-in list of vendors: the URL of somebody's Grafana is not
// something this project can guess, and a fixed set of integrations is a fixed
// set of things to keep up to date.
//
// The person writing values.yaml is trusted with the cluster. The template
// they write is still treated as hostile, because it is rendered into a page:
// an unchecked scheme there is a javascript: URL waiting for a reader who
// trusts this dashboard.

import (
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/remedik/remedik/api/v1alpha1"
)

// linkPad is how much either side of a record a time window covers.
//
// Fifteen minutes, because a dashboard opened at exactly the moment something
// happened shows it against no context at all, and the question being asked is
// always "and what was going on around it".
const linkPad = 15 * time.Minute

// Link is one destination, as configured.
type Link struct {
	// Name is what the link says.
	Name string
	// URL is a template over the placeholders below.
	URL string
}

// Placeholders a template may carry. Anything else is left alone, so a URL
// that happens to contain braces is not mangled.
const (
	placeholderNamespace   = "{namespace}"
	placeholderTarget      = "{target}"
	placeholderName        = "{name}"
	placeholderAlert       = "{alert}"
	placeholderFingerprint = "{fingerprint}"
	placeholderFrom        = "{from}"
	placeholderTo          = "{to}"
)

// ResolvedLink is a link with this record's own values in it.
type ResolvedLink struct {
	Name string
	URL  string
}

// validLinks keeps the links that may be rendered and logs the rest.
//
// Dropped rather than fatal: a mistyped link is not a reason to refuse to
// remediate a cluster. It is a reason to say so loudly, once, at startup,
// where somebody is looking.
func validLinks(links []Link, logger *slog.Logger) []Link {
	kept := make([]Link, 0, len(links))
	for _, link := range links {
		switch {
		case strings.TrimSpace(link.Name) == "":
			logger.Warn("ignoring a dashboard link with no name", "url", link.URL)
		case hasControlChars(link.Name), hasControlChars(link.URL):
			// A newline in a chart value used to end its own list item and
			// start another, which is not a malformed link — it is an extra
			// flag on the operator's command line. The chart quotes its
			// arguments now; this refuses to render what got through anyway,
			// because a control character in a name has no meaning here and
			// every reason to be somebody's idea.
			logger.Warn("ignoring a dashboard link containing control characters",
				"name", strconv.Quote(link.Name), "url", strconv.Quote(link.URL))
		case !safeLinkTemplate(link.URL):
			// The whole point of the check. A javascript: URL here would run
			// with the reader's session on a page they trust.
			logger.Warn("ignoring a dashboard link that is not http or https",
				"name", link.Name, "url", link.URL)
		default:
			kept = append(kept, link)
		}
	}
	return kept
}

// hasControlChars reports whether a string carries anything that is not
// printable text — a newline, a tab, an escape.
func hasControlChars(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return r < 0x20 || r == 0x7f
	})
}

// safeLinkTemplate reports whether a template may be rendered into a page.
//
// The scheme is checked on the template rather than on the result, so a link
// is refused at startup rather than on the one page that happens to trigger
// it. Placeholders are not substituted first: none of them can introduce a
// scheme, because a scheme has to come first and a template that starts with
// one has already been checked.
func safeLinkTemplate(template string) bool {
	parsed, err := url.Parse(template)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// resolveLinks fills the templates in with one record's values.
func resolveLinks(links []Link, rem *v1alpha1.Remediation, now time.Time) []ResolvedLink {
	if len(links) == 0 {
		return nil
	}

	from, to := linkWindow(rem, now)
	replacer := strings.NewReplacer(
		// Every value is escaped on the way in. A workload called
		// "api&admin=1" must not become two query parameters, and a target
		// carries slashes that mean something in a path and nothing in a
		// value.
		placeholderNamespace, url.QueryEscape(TargetNamespace(rem.Spec.Target)),
		placeholderTarget, url.QueryEscape(rem.Spec.Target),
		placeholderName, url.QueryEscape(rem.Name),
		placeholderAlert, url.QueryEscape(rem.Spec.Alert.Name),
		placeholderFingerprint, url.QueryEscape(rem.Spec.Alert.Fingerprint),
		placeholderFrom, url.QueryEscape(from.UTC().Format(time.RFC3339)),
		placeholderTo, url.QueryEscape(to.UTC().Format(time.RFC3339)),
	)

	resolved := make([]ResolvedLink, 0, len(links))
	for _, link := range links {
		resolved = append(resolved, ResolvedLink{
			Name: link.Name, URL: replacer.Replace(link.URL),
		})
	}
	return resolved
}

// linkWindow is the span a record happened in, padded either side.
//
// From the alert firing where there is one, because that is when the thing
// being investigated started; from the record otherwise.
func linkWindow(rem *v1alpha1.Remediation, now time.Time) (from, to time.Time) {
	from = rem.CreationTimestamp.Time
	if firing := rem.Spec.Alert.StartsAt; firing != nil && !firing.IsZero() &&
		firing.Time.Before(from) {
		from = firing.Time
	}

	to = now
	if done := rem.Status.CompletedAt; done != nil && !done.IsZero() {
		to = done.Time
	}
	if to.Before(from) {
		to = from
	}
	return from.Add(-linkPad), to.Add(linkPad)
}
