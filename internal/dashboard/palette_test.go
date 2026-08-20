package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"io/fs"

	"github.com/remedik/remedik/api/v1alpha1"
)

func decodePalette(t *testing.T, body string) Palette {
	t.Helper()
	var palette Palette
	if err := json.Unmarshal([]byte(body), &palette); err != nil {
		t.Fatalf("palette is not JSON: %v\n%s", err, body)
	}
	return palette
}

// Everything on these pages that has a name, in one list — including the
// strategy that has never fired, which is exactly the one somebody goes
// looking for.
func TestPalette_OffersThePagesAndWhatIsInThem(t *testing.T) {
	h, reader := newHandler(t, Config{})
	reader.remediations = []v1alpha1.Remediation{
		succeededRemediation("run-1", 5),
		failedRemediation("run-2", 20),
	}
	reader.strategies = []v1alpha1.RemediationStrategy{enabledStrategy(), disabledStrategy()}

	palette := decodePalette(t, get(t, h, palettePath, nil).Body.String())

	kinds := map[string][]string{}
	for _, entry := range palette.Entries {
		kinds[entry.Kind] = append(kinds[entry.Kind], entry.Label)
		if entry.URL == "" {
			t.Errorf("entry %q has no URL to go to", entry.Label)
		}
	}

	for _, want := range []string{"Overview", "Remediations", "Namespaces", "Approvals", "Strategies"} {
		if !strings.Contains(strings.Join(kinds[paletteKindPage], " "), want) {
			t.Errorf("the palette does not offer the %s page", want)
		}
	}
	// node-drain has never run, and is in the list because it is declared.
	if !strings.Contains(strings.Join(kinds[paletteKindStrategy], " "), "node-drain") {
		t.Errorf("strategies = %v, want the one that has never fired", kinds[paletteKindStrategy])
	}
	for _, kind := range []string{paletteKindNamespace, paletteKindAlert, paletteKindRecord} {
		if len(kinds[kind]) == 0 {
			t.Errorf("nothing of kind %q in the palette", kind)
		}
	}
}

// It discloses what the pages disclose, behind the same authentication, and it
// is GET like everything else here.
func TestPalette_IsReadOnlyAndAuthenticated(t *testing.T) {
	h, _ := newHandler(t, Config{Token: testToken})

	if code := get(t, h, palettePath, nil).Code; code != http.StatusUnauthorized {
		t.Errorf("unauthenticated GET %s = %d, want 401", palettePath, code)
	}

	rec := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, palettePath, nil)
	request.Header.Set("Authorization", "Bearer "+testToken)
	h.ServeHTTP(rec, request)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST %s = %d, want 405", palettePath, rec.Code)
	}

	ok := get(t, h, palettePath, map[string]string{"Authorization": "Bearer " + testToken})
	if ok.Code != http.StatusOK {
		t.Fatalf("authenticated GET %s = %d", palettePath, ok.Code)
	}
	if kind := ok.Header().Get("Content-Type"); !strings.HasPrefix(kind, "application/json") {
		t.Errorf("Content-Type = %q", kind)
	}
	if cache := ok.Header().Get("Cache-Control"); cache != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: this is a live view", cache)
	}
}

// A cluster with nine hundred namespaces must not send them all to answer a
// question the reader narrows in three keystrokes.
func TestPalette_CapsEachKind(t *testing.T) {
	records := bigCluster(paletteLimit*2, 3, paletteLimit*4)

	palette := buildPalette(records, nil, testNow())

	counts := map[string]int{}
	for _, entry := range palette.Entries {
		counts[entry.Kind]++
	}
	if counts[paletteKindNamespace] != paletteLimit {
		t.Errorf("namespaces = %d, want them capped at %d",
			counts[paletteKindNamespace], paletteLimit)
	}
	if counts[paletteKindRecord] != paletteRecords {
		t.Errorf("records = %d, want %d", counts[paletteKindRecord], paletteRecords)
	}
}

// Busiest first, so the cap keeps what matters, and stable so two fetches of
// the same data agree.
func TestPalette_BusiestFirstAndStable(t *testing.T) {
	records := []v1alpha1.Remediation{
		succeededRemediation("a", 1), // deployment/checkout/web
		succeededRemediation("b", 2), // the same target again
		failedRemediation("c", 3),    // deployment/payments/api
	}

	first := buildPalette(records, nil, testNow())
	second := buildPalette(records, nil, testNow())

	var namespaces []string
	for _, entry := range first.Entries {
		if entry.Kind == paletteKindNamespace {
			namespaces = append(namespaces, entry.Label)
		}
	}
	if len(namespaces) < 2 || namespaces[0] != "checkout" {
		t.Errorf("namespaces = %v, want the busiest first", namespaces)
	}
	if len(first.Entries) != len(second.Entries) {
		t.Fatal("two builds of the same records disagreed in length")
	}
	for i := range first.Entries {
		if first.Entries[i] != second.Entries[i] {
			t.Fatalf("entry %d differs between builds: %+v vs %+v",
				i, first.Entries[i], second.Entries[i])
		}
	}
}

// The templates have this check; the script needs it more, because a class it
// names is invisible to every test that reads the HTML — the overlay does not
// exist until somebody presses a key.
func TestEveryScriptClassIsStyled(t *testing.T) {
	css, err := fs.ReadFile(files, "assets/app.css")
	if err != nil {
		t.Fatalf("ReadFile(app.css) error = %v", err)
	}
	script, err := fs.ReadFile(files, "assets/app.js")
	if err != nil {
		t.Fatalf("ReadFile(app.js) error = %v", err)
	}

	defined := map[string]bool{}
	for _, match := range regexp.MustCompile(`\.([a-z][a-z0-9-]*)`).
		FindAllStringSubmatch(string(css), -1) {
		defined[match[1]] = true
	}

	// The class names the script builds elements with, as string literals.
	used := regexp.MustCompile(`"((?:palette|copyable|copy|is-live)[a-z0-9 -]*)"`)
	for _, match := range used.FindAllStringSubmatch(string(script), -1) {
		for _, class := range strings.Fields(match[1]) {
			if !defined[class] {
				t.Errorf("app.js builds class %q, which the stylesheet does not define", class)
			}
		}
	}
}
