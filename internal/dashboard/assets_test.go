package dashboard

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/ratyx/remedik/api/v1alpha1"
)

func TestStaticAssetsAreServedFromTheBinary(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{}})

	tests := []struct {
		path        string
		contentType string
	}{
		{path: "/static/app.css", contentType: "text/css; charset=utf-8"},
		{path: "/static/app.js", contentType: "text/javascript; charset=utf-8"},
		{path: "/static/favicon.svg", contentType: "image/svg+xml"},
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
			for _, directive := range []string{"style-src 'self'", "script-src 'self'"} {
				if !strings.Contains(csp, directive) {
					t.Errorf("Content-Security-Policy = %q, want %q", csp, directive)
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
