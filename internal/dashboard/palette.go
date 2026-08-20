package dashboard

// Everything on these pages that has a name, as one list.
//
// The pages are reachable by link and always were. What was missing is the
// interaction an operator already has everywhere else: press a key, type three
// letters of a namespace, arrive. k9s has it, every editor has it, and a
// dashboard read during an incident is exactly where it earns its keep.
//
// It is a route rather than a blob embedded in every page, because the same
// list would otherwise be rendered into all five of them on every ten-second
// refresh. It discloses nothing the pages do not already show, to a request
// that has already passed the same authentication, and it is GET like
// everything else here.

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/remedik/remedik/api/v1alpha1"
)

// palettePath is the list the palette is built from.
const palettePath = "/palette"

const (
	// paletteLimit caps each kind of entry. A cluster with nine hundred
	// namespaces would otherwise send them all, on a route fetched once per
	// page load, to answer a question the reader narrows in three keystrokes.
	paletteLimit = 100
	// paletteRecords is how many of the most recent records are offered by
	// name. Beyond a handful, nobody is looking for a record by its suffix.
	paletteRecords = 20
)

// Kinds, which the palette shows as the group a result belongs to.
const (
	paletteKindPage      = "page"
	paletteKindNamespace = "namespace"
	paletteKindStrategy  = "strategy"
	paletteKindAlert     = "alert"
	paletteKindRecord    = "remediation"
)

// PaletteEntry is one thing worth going to.
type PaletteEntry struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	URL    string `json:"url"`
}

// Palette is the whole list.
type Palette struct {
	Entries []PaletteEntry `json:"entries"`
}

// palettePages are the fixed destinations. They are served rather than built
// into the script so that adding a page is one change, here, in the same place
// the routes are.
var palettePages = []PaletteEntry{
	{Kind: paletteKindPage, Label: "Overview", Detail: "is anything wrong right now", URL: "/"},
	{Kind: paletteKindPage, Label: "Remediations", Detail: "every execution kept", URL: remediationsPath},
	{Kind: paletteKindPage, Label: "Namespaces", Detail: "where it is going badly", URL: namespacesPath},
	{Kind: paletteKindPage, Label: "Approvals", Detail: "waiting for a person", URL: approvalsPath},
	{Kind: paletteKindPage, Label: "Strategies", Detail: "what remedik may do", URL: "/strategies"},
}

func buildPalette(
	remediations []v1alpha1.Remediation,
	strategies []v1alpha1.RemediationStrategy,
	now time.Time,
) Palette {
	palette := Palette{Entries: make([]PaletteEntry, 0, len(palettePages)+paletteLimit)}
	palette.Entries = append(palette.Entries, palettePages...)

	// One pass for the three dimensions, rather than one pass each.
	namespaces := map[string]int{}
	alerts := map[string]int{}
	byStrategy := map[string]int{}
	for i := range remediations {
		rem := &remediations[i]
		if ns := TargetNamespace(rem.Spec.Target); ns != "" {
			namespaces[ns]++
		}
		if name := rem.Spec.Alert.Name; name != "" {
			alerts[name]++
		}
		if name := rem.Spec.StrategyName; name != "" {
			byStrategy[name]++
		}
	}

	palette.Entries = append(palette.Entries,
		counted(paletteKindNamespace, namespaces, func(name string) string {
			return Filter{Namespace: name}.Path()
		})...)

	// Strategies come from the resources, not from the records: one that has
	// never fired is exactly the one somebody goes looking for.
	declared := map[string]int{}
	for i := range strategies {
		declared[strategies[i].Name] = byStrategy[strategies[i].Name]
	}
	for name, count := range byStrategy {
		if _, ok := declared[name]; !ok {
			declared[name] = count
		}
	}
	palette.Entries = append(palette.Entries,
		counted(paletteKindStrategy, declared, func(name string) string {
			return Filter{Strategy: name}.Path()
		})...)

	palette.Entries = append(palette.Entries,
		counted(paletteKindAlert, alerts, func(name string) string {
			return Filter{Alert: name}.Path()
		})...)

	// And the newest records by name, for somebody holding one from a log line
	// or a Slack message.
	recent := newestFirst(remediations)
	for i, rem := range recent {
		if i == paletteRecords {
			break
		}
		palette.Entries = append(palette.Entries, PaletteEntry{
			Kind:  paletteKindRecord,
			Label: rem.Name,
			Detail: displayState(rem.Status.State) + " · " +
				FormatAge(rem.CreationTimestamp.Time, now) + " old",
			URL: "/remediations/" + rem.Name,
		})
	}
	return palette
}

// counted turns a tally into sorted entries, busiest first, capped.
func counted(kind string, tally map[string]int, link func(string) string) []PaletteEntry {
	entries := make([]PaletteEntry, 0, len(tally))
	for name, count := range tally {
		entries = append(entries, PaletteEntry{
			Kind:   kind,
			Label:  name,
			Detail: plural(count, "record"),
			URL:    link(name),
		})
	}
	// Busiest first so the cap keeps what matters, then by name so the list is
	// the same between two fetches of the same data.
	sort.Slice(entries, func(i, j int) bool {
		if tally[entries[i].Label] != tally[entries[j].Label] {
			return tally[entries[i].Label] > tally[entries[j].Label]
		}
		return entries[i].Label < entries[j].Label
	})
	if len(entries) > paletteLimit {
		entries = entries[:paletteLimit]
	}
	return entries
}

// palette serves the list. JSON rather than a page: it is read by the script
// and by nothing else.
func (h *Handler) palette(w http.ResponseWriter, r *http.Request) {
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

	body, err := json.Marshal(buildPalette(remediations, strategies, h.now()))
	if err != nil {
		h.logger.Error("could not render the palette", "err", err)
		http.Error(w, "the dashboard could not build the palette", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(body); err != nil {
		h.logger.Debug("palette response write failed", "err", err)
	}
}
