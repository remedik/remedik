package dashboard

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/remedik/remedik/api/v1alpha1"
)

func TestStaticAssetsAreServedFromTheBinary(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{}})

	tests := []struct {
		path        string
		contentType string
	}{
		{path: "/static/app.css", contentType: "text/css; charset=utf-8"},
		{path: "/static/app.js", contentType: "text/javascript; charset=utf-8"},
		{path: "/static/favicon.png", contentType: "image/png"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			rec := get(t, h, tc.path, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", tc.path, rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != tc.contentType {
				t.Errorf("Content-Type = %q, want %q", got, tc.contentType)
			}
			if rec.Body.Len() == 0 {
				t.Error("the asset was served empty")
			}
			etag := rec.Header().Get("ETag")
			if etag == "" {
				t.Fatal("the asset carried no ETag")
			}

			// A second request with the ETag must not re-send the body: the
			// dashboard refreshes itself every ten seconds, and re-sending
			// the stylesheet each time would be the operator's bandwidth
			// spent on nothing.
			again := get(t, h, tc.path, map[string]string{"If-None-Match": etag})
			if again.Code != http.StatusNotModified {
				t.Errorf("conditional GET %s = %d, want 304", tc.path, again.Code)
			}
		})
	}
}

func TestUnknownStaticAssetIs404(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{}})

	if rec := get(t, h, "/static/nope.css", nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET /static/nope.css = %d, want 404", rec.Code)
	}
}

func TestAssetURLsCarryTheBuildFingerprint(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{}})

	body := get(t, h, "/", nil).Body.String()
	for _, asset := range []string{"app.css", "app.js"} {
		want := "/static/" + asset + "?v=" + assetVersion
		if !strings.Contains(body, want) {
			t.Errorf("the page does not reference %s; an upgraded operator would be "+
				"read through a cached copy of the old one", want)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{}, Token: testToken})

	tests := []struct {
		name   string
		path   string
		header map[string]string
	}{
		{
			name:   "a rendered page",
			path:   "/",
			header: map[string]string{"Authorization": "Bearer " + testToken},
		},
		{
			// The headers must be on the refusals too: those are pages a
			// browser renders as well.
			name: "an unauthenticated refusal",
			path: "/",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(t, h, tc.path, tc.header)

			csp := rec.Header().Get("Content-Security-Policy")
			if !strings.Contains(csp, "default-src 'none'") {
				t.Errorf("Content-Security-Policy = %q, want a default-src of 'none'", csp)
			}
			for _, directive := range []string{
				"style-src 'self'", "script-src 'self'",
				// 'self' rather than 'none', and this is the whole reason the
				// filter's select works: it is a GET form posting back here,
				// and 'none' blocked every submission. The control looked
				// merely unresponsive and the only evidence was a console
				// message, so it was reported as broken four times.
				"form-action 'self'",
			} {
				if !strings.Contains(csp, directive) {
					t.Errorf("Content-Security-Policy = %q, want %q", csp, directive)
				}
			}
			// Everything the page is allowed to reach is its own origin, and
			// that has to stay true: a policy that grew a host would be the
			// dashboard fetching from somewhere, which the offline promise
			// forbids.
			for _, forbidden := range []string{"http:", "https:", "*"} {
				if strings.Contains(csp, forbidden) {
					t.Errorf("Content-Security-Policy = %q reaches outside its own origin (%q)",
						csp, forbidden)
				}
			}
			if strings.Contains(csp, "unsafe-inline") {
				t.Errorf("Content-Security-Policy = %q allows inline code", csp)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
				t.Errorf("X-Frame-Options = %q, want DENY", got)
			}
			if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
				t.Errorf("Referrer-Policy = %q, want no-referrer", got)
			}
		})
	}
}

// externalOrigin matches anything that would make the browser talk to a host
// other than the operator: an absolute URL in an attribute, a CSS url() or
// @import pointing off-host, or a subresource-integrity attribute, which
// only exists for resources loaded from somewhere else.
var externalOrigin = regexp.MustCompile(
	`(?i)(src|href|action|formaction)\s*=\s*["']?\s*(https?:)?//|` +
		`url\(\s*["']?\s*(https?:)?//|` +
		`@import|integrity\s*=|crossorigin`)

// TestNothingIsFetchedFromOutsideTheCluster is the test behind the spec's
// promise that an air-gapped cluster renders the same page. It reads the
// embedded files rather than a rendered page, because a template that never
// happens to be exercised is exactly where an external reference would
// survive.
func TestNothingIsFetchedFromOutsideTheCluster(t *testing.T) {
	err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}

		body, err := files.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(body), "\n") {
			if match := externalOrigin.FindString(line); match != "" {
				t.Errorf("%s:%d references something outside the cluster (%q): %s",
					path, i+1, match, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the embedded files: %v", err)
	}
}

// TestRenderedPagesFetchNothingExternal repeats the check on the output,
// which is what a browser actually sees. Every value on these pages comes
// from an alert label the operator does not control, so the fixture puts a
// URL and a script tag in one and asserts neither becomes markup.
func TestRenderedPagesFetchNothingExternal(t *testing.T) {
	hostile := simulatedRemediation("sim-hostile", "deployment/payments/api", 5)
	hostile.Spec.Alert.Labels = map[string]string{
		"runbook":  "https://evil.example/steal",
		"injected": `"><script src="//evil.example/x.js"></script>`,
	}

	h, _ := newHandler(t, Config{
		Reader: &fakeReader{
			remediations: []v1alpha1.Remediation{hostile},
			strategies:   []v1alpha1.RemediationStrategy{enabledStrategy(), disabledStrategy()},
		},
		Posture: Posture{DryRun: true},
	})

	for _, path := range []string{"/", "/strategies", "/remediations/sim-hostile", "/nope"} {
		t.Run(path, func(t *testing.T) {
			body := get(t, h, path, nil).Body.String()

			for i, line := range strings.Split(body, "\n") {
				if match := externalOrigin.FindString(line); match != "" {
					t.Errorf("%s line %d would load from outside the cluster (%q): %s",
						path, i+1, match, strings.TrimSpace(line))
				}
			}
			if strings.Contains(body, "<script src=\"//evil.example") {
				t.Error("an alert label became markup instead of text")
			}
		})
	}
}

func TestAssetFingerprintIsDeterministic(t *testing.T) {
	if len(assetVersion) != 12 {
		t.Errorf("assetVersion = %q, want 12 hex characters", assetVersion)
	}
	if got := fingerprint(staticAssets); got != assetVersion {
		t.Errorf("fingerprint recomputed to %q, want the stable %q", got, assetVersion)
	}
}

func TestEveryPageIsCompleteHTML(t *testing.T) {
	h, _ := newHandler(t, Config{
		Reader: &fakeReader{
			remediations: []v1alpha1.Remediation{succeededRemediation("ok-1", 5)},
			strategies:   []v1alpha1.RemediationStrategy{enabledStrategy()},
		},
	})

	// A half-written page behind a 200 is the one failure that looks like
	// success, so every route is checked for the end of the document as
	// well as the beginning.
	for _, path := range []string{"/", "/strategies", "/remediations/ok-1", "/nope"} {
		body := get(t, h, path, nil).Body.String()
		for _, marker := range []string{"<!doctype html>", `<main id="content"`, "</html>"} {
			if !strings.Contains(body, marker) {
				t.Errorf("GET %s produced a page without %q", path, marker)
			}
		}
	}
}

// Filtering must hold no state between choosing and applying. A <select>
// plus a submit button does, and that state was destroyed by the ten-second
// refresh — twice, in two different ways. Links have nothing to lose, which
// is why the controls are links and why this test exists to keep them that
// way.
func TestFilteringUsesLinksAndNoForm(t *testing.T) {
	payments := simulatedRemediation("sim-payments", "deployment/payments/api", 20)
	checkout := succeededRemediation("ok-checkout", 10)
	checkout.Spec.Target = "deployment/checkout/web"

	h, _ := newHandler(t, Config{
		Reader: &fakeReader{
			remediations: []v1alpha1.Remediation{payments, checkout},
			strategies:   []v1alpha1.RemediationStrategy{enabledStrategy()},
		},
		Posture: Posture{DryRun: true},
	})

	for _, path := range []string{"/", "/remediations", "/remediations?namespace=payments"} {
		t.Run(path, func(t *testing.T) {
			body := get(t, h, path, nil).Body.String()

			if strings.Contains(body, "<form") {
				t.Error("the page has a form; filtering must be navigation, with no state to lose")
			}
			if strings.Contains(body, "<select") || strings.Contains(body, "<input") {
				t.Error("the page has an input; filtering must be navigation")
			}
		})
	}

	// And the links are really there.
	body := get(t, h, "/remediations", nil).Body.String()
	mustContain(t, body, `href="/remediations?namespace=payments"`,
		"offer a link that filters by namespace")
}

// The dashboard serves style-src 'self' with no 'unsafe-inline', so a
// style attribute is dropped by the browser and the element falls back to
// its default size. Four bar charts rendered at full width for exactly that
// reason, silently, from the day each was written — a defect no handler
// test can see, because the markup was correct and the browser refused it.
func TestTemplatesCarryNoInlineStyles(t *testing.T) {
	entries, err := fs.Glob(files, "templates/*.html")
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no templates found")
	}

	for _, name := range entries {
		body, err := fs.ReadFile(files, name)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", name, err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			if strings.Contains(line, `style="`) {
				t.Errorf("%s line %d has an inline style, which the CSP drops: %s",
					name, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// Every class a template names must exist in the stylesheet. Renaming one
// half of a pair leaves markup that renders with no styling at all, which
// looks like a layout bug and is invisible to every test that checks the
// HTML — it happened twice in one evening: share-track against .bar, and
// mode-dryrun against .mode-dry.
func TestEveryTemplateClassIsStyled(t *testing.T) {
	css, err := fs.ReadFile(files, "assets/app.css")
	if err != nil {
		t.Fatalf("ReadFile(app.css) error = %v", err)
	}
	defined := map[string]bool{}
	for _, match := range regexp.MustCompile(`\.([a-z][a-z0-9-]*)`).FindAllStringSubmatch(string(css), -1) {
		defined[match[1]] = true
	}

	entries, _ := fs.Glob(files, "templates/*.html")
	class := regexp.MustCompile(`class="([^"{}]*)"`)

	for _, name := range entries {
		body, _ := fs.ReadFile(files, name)
		for _, match := range class.FindAllStringSubmatch(string(body), -1) {
			for _, used := range strings.Fields(match[1]) {
				if !defined[used] {
					t.Errorf("%s uses class %q, which the stylesheet does not define", name, used)
				}
			}
		}
	}
}

// Every custom property the stylesheet reads is one it also defines.
//
// A `var(--surface-sunken)` that nothing declares is not an error anywhere: the
// browser drops the declaration and the element renders without it. That is
// the same failure mode as the inline styles the Content-Security-Policy
// discarded — correct-looking markup, silently ignored — and it happened
// twice while the namespaces page was written, once for a border and once for
// a text colour.
func TestEveryCustomPropertyIsDefined(t *testing.T) {
	css, err := files.ReadFile("assets/app.css")
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}
	sheet := string(css)

	// Definitions are found after the reads are removed, so that `var(--x)`
	// is never mistaken for a declaration of --x. A declaration can sit on
	// the same line as its selector — `.pct-0 { --pct: 0%; }` — so anchoring
	// to the start of a line would miss the generated ones.
	reads := regexp.MustCompile(`var\(\s*--[a-zA-Z0-9-]+`)
	declarations := reads.ReplaceAllString(sheet, "var(")

	defined := map[string]bool{}
	for _, m := range regexp.MustCompile(`(--[a-zA-Z0-9-]+)\s*:`).FindAllStringSubmatch(declarations, -1) {
		defined[m[1]] = true
	}
	if len(defined) < 20 {
		t.Fatalf("found only %d custom properties; the pattern is wrong, not the stylesheet",
			len(defined))
	}

	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`var\(\s*(--[a-zA-Z0-9-]+)`).FindAllStringSubmatch(sheet, -1) {
		name := m[1]
		if defined[name] || seen[name] {
			continue
		}
		seen[name] = true
		t.Errorf("var(%s) is read but never defined, so every declaration using "+
			"it is silently dropped by the browser", name)
	}
}

// The select applies on change because the script finds it by class. Rename
// the class in one place and the control silently needs its button back — and
// the button is hidden by the same class, so it would need a gesture that is
// no longer visible. That is worse than either state on its own.
func TestTheFilterSelectIsWiredToItsScript(t *testing.T) {
	js, err := files.ReadFile("assets/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	css, err := files.ReadFile("assets/app.css")
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}
	page, err := files.ReadFile("templates/remediations.html")
	if err != nil {
		t.Fatalf("read remediations.html: %v", err)
	}

	for what, checks := range map[string][]struct {
		in   string
		body string
	}{
		`the script's selector`: {{`form.filter-select`, string(js)}},
		`the markup's class`:    {{`class="filter-select"`, string(page)}},
		`the button's rule`:     {{`.filter-select.is-live button`, string(css)}},
		`the script's flag`:     {{`"is-live"`, string(js)}},
	} {
		for _, c := range checks {
			if !strings.Contains(c.body, c.in) {
				t.Errorf("%s is missing: %q not found", what, c.in)
			}
		}
	}

	// It has to keep working with the script off, which is the only reason the
	// button exists at all.
	for _, want := range []string{`method="get"`, `type="submit"`} {
		if !strings.Contains(string(page), want) {
			t.Errorf("the filter form no longer works without JavaScript: %q missing", want)
		}
	}
}
