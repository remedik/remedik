package dashboard

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strconv"
	"sync"
	"time"
)

// The dashboard ships inside the binary. Nothing it renders is fetched at
// runtime: no CDN, no font service, no build step and no second release
// artifact. A cluster with no outbound internet access serves exactly the
// same page as one with.
//
//go:embed templates/*.html assets/*
var files embed.FS

// layoutName is the template every page is executed through. Each page
// template contributes the "content" block the layout calls.
const layoutName = "layout.html"

var (
	overviewTemplate     = mustParsePage("overview.html")
	remediationsTemplate = mustParsePage("remediations.html")
	remediationTemplate  = mustParsePage("remediation.html")
	strategiesTemplate   = mustParsePage("strategies.html")
	errorTemplate        = mustParsePage("error.html")
)

// mustParsePage parses one page together with the layout.
//
// One template set per page, rather than one set holding them all: every
// page defines a block named "content", and a single set could only hold
// one of them.
func mustParsePage(page string) *template.Template {
	return template.Must(template.New(layoutName).Funcs(pageFuncs).ParseFS(files,
		"templates/"+layoutName, "templates/"+page))
}

// pageFuncs are the few helpers a template may call.
//
// Deliberately few. Logic belongs in the view builders, where it is a pure
// function from resources to a struct and can be tested as one; a template
// that can compute is a template that grows behaviour nobody reviews. These
// are here because writing "1 strategies" is a defect a reader notices and
// threading a pre-pluralised string through every struct is worse.
var pageFuncs = template.FuncMap{
	"plural": plural,
	"pct":    pctClass,
}

// pctClass turns a percentage into one of the classes the stylesheet
// defines, rounded to the nearest five.
//
// It exists because the dashboard serves a Content-Security-Policy of
// style-src 'self', which forbids inline style attributes — so every
// `style="width:33%"` in these templates was silently dropped by the
// browser and every bar rendered at its default size. Proportions have to
// arrive as classes, and 5% steps are indistinguishable on a bar while
// keeping the stylesheet to twenty-one rules.
func pctClass(percent int) string {
	switch {
	case percent <= 0:
		return "pct-0"
	case percent >= 100:
		return "pct-100"
	}
	return "pct-" + strconv.Itoa((percent+2)/5*5)
}

// staticAsset is one embedded file, with everything needed to serve it
// computed once at startup rather than per request.
type staticAsset struct {
	body        []byte
	contentType string
	etag        string
}

var (
	staticAssets = loadStaticAssets()
	// assetVersion is a fingerprint of every static asset, appended to
	// their URLs. It lets the assets be cached hard and still change the
	// instant the operator is upgraded: a new build means a new URL.
	assetVersion = fingerprint(staticAssets)
)

func loadStaticAssets() map[string]staticAsset {
	entries, err := fs.ReadDir(files, "assets")
	if err != nil {
		// The files are embedded at compile time; a failure here means the
		// binary was built wrong, which is not something to serve past.
		panic(fmt.Sprintf("dashboard: read embedded assets: %v", err))
	}

	assets := make(map[string]staticAsset, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body, err := files.ReadFile("assets/" + entry.Name())
		if err != nil {
			panic(fmt.Sprintf("dashboard: read embedded asset %s: %v", entry.Name(), err))
		}
		sum := sha256.Sum256(body)
		assets[entry.Name()] = staticAsset{
			body:        body,
			contentType: contentTypeFor(entry.Name()),
			etag:        `"` + hex.EncodeToString(sum[:8]) + `"`,
		}
	}
	return assets
}

// contentTypeFor names the type explicitly rather than sniffing it. The
// responses carry X-Content-Type-Options: nosniff, so a guess that lands
// wrong would not be corrected by the browser — it would simply not load.
func contentTypeFor(name string) string {
	switch path.Ext(name) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	default:
		return "application/octet-stream"
	}
}

// fingerprint hashes every asset into one short version string.
func fingerprint(assets map[string]staticAsset) string {
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)

	sum := sha256.New()
	for _, name := range names {
		sum.Write([]byte(name))
		sum.Write(assets[name].body)
	}
	return hex.EncodeToString(sum.Sum(nil))[:12]
}

// staticHandler serves the embedded CSS and JavaScript.
func staticHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asset, ok := staticAssets[path.Clean(r.URL.Path)]
		if !ok {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", asset.contentType)
		w.Header().Set("ETag", asset.etag)
		// The URL carries a fingerprint of the content, so a cached copy can
		// never be the wrong one: an upgraded operator serves a new URL.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")

		// A zero modification time keeps Last-Modified out of the response:
		// the build time of an embedded file says nothing useful, and the
		// ETag already answers the question.
		http.ServeContent(w, r, path.Base(r.URL.Path), time.Time{}, bytes.NewReader(asset.body))
	})
}

// bufferPool backs render: pages are a few tens of kilobytes and a busy
// incident means a page every few seconds per viewer, so the buffers are
// worth reusing.
var bufferPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

func newBuffer() *bytes.Buffer {
	buf, _ := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	return buf
}

func releaseBuffer(buf *bytes.Buffer) {
	// A page that grew unusually large is not worth keeping around; letting
	// it go bounds the pool's memory at the common case.
	const maxRetained = 1 << 20
	if buf.Cap() > maxRetained {
		return
	}
	bufferPool.Put(buf)
}
