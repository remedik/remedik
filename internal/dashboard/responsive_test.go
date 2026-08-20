package dashboard

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// A card is a row with its column names in it, taken from each cell's own
// data-label. A cell without one renders as a value with nothing saying what
// it is — which is invisible at desktop width, where the header row is still
// doing that job, and is the whole content of the card at phone width.
func TestCardTablesLabelEveryCell(t *testing.T) {
	entries, err := fs.Glob(files, "templates/*.html")
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}

	cell := regexp.MustCompile(`<td[^>]*>`)
	var checked int

	for _, name := range entries {
		body, err := fs.ReadFile(files, name)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", name, err)
		}
		if !strings.Contains(string(body), "table-cards") {
			continue
		}
		checked++

		for _, tag := range cell.FindAllString(string(body), -1) {
			if !strings.Contains(tag, "data-label=") {
				t.Errorf("%s has a cell with no column name: %s", name, tag)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no template opts into cards; this test is checking nothing")
	}
}

// The stylesheet must keep the posture readable at phone width: "is it live"
// is the question somebody woken up asks first, and the header is where it is
// answered.
func TestTheNarrowLayoutKeepsThePosture(t *testing.T) {
	css, err := fs.ReadFile(files, "assets/app.css")
	if err != nil {
		t.Fatalf("ReadFile(app.css) error = %v", err)
	}

	narrow := strings.SplitN(string(css), "@media (width <= 720px) {", 2)
	if len(narrow) != 2 {
		t.Fatal("no narrow-screen block in the stylesheet")
	}
	// Up to the end of that block, which is where the rules that only apply to
	// a phone live.
	block := narrow[1]
	if end := strings.Index(block, "\n}\n"); end > 0 {
		block = block[:end]
	}

	for _, hidden := range []string{".mode", ".topbar"} {
		if regexp.MustCompile(regexp.QuoteMeta(hidden) + `\s*\{[^}]*display:\s*none`).
			MatchString(block) {
			t.Errorf("the narrow layout hides %s, which carries the posture", hidden)
		}
	}
}
