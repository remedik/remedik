package engine

import (
	"fmt"
	"sort"
	"strings"
)

// Mode is what remedik is allowed to do in a namespace.
type Mode string

const (
	// ModeLive executes. Actions change the cluster.
	ModeLive Mode = "live"
	// ModeDryRun plans. The mutating path is never called.
	ModeDryRun Mode = "dryRun"
)

// Posture answers "may remedik act here?" for one namespace.
//
// The default is the cluster-wide setting; the overrides are the namespaces
// that differ. Keeping both in one type means the question is answered in one
// place, and the answer is a pure function of configuration and a namespace
// name — which is the property that makes it worth testing.
//
// The zero value is a dry-run posture with no overrides, which is the safe
// reading of "nothing was configured".
type Posture struct {
	// Default applies to any namespace with no entry below, and to targets
	// that have no namespace at all — a node, a webhook. That fallback is
	// safe by construction: the chart ships the default as dry-run.
	Default Mode
	// Overrides are the namespaces that differ from the default.
	Overrides map[string]Mode
}

// NewPosture builds a posture from the cluster-wide flag and the overrides.
func NewPosture(dryRun bool, overrides map[string]Mode) Posture {
	mode := ModeLive
	if dryRun {
		mode = ModeDryRun
	}
	return Posture{Default: mode, Overrides: overrides}
}

// DryRunFor reports whether a remediation targeting this namespace simulates.
//
// An empty namespace is a cluster-scoped or outward-facing target and takes
// the default. Guessing would be worse: there is nothing to guess from.
func (p Posture) DryRunFor(namespace string) bool {
	if namespace != "" {
		if mode, ok := p.Overrides[namespace]; ok {
			return mode == ModeDryRun
		}
	}
	// An unset default is dry-run. A zero-valued Posture that acted would be
	// the worst possible reading of "nobody configured this".
	return p.Default != ModeLive
}

// Mixed reports whether any namespace differs from the default.
//
// The dashboard needs this: a badge saying "dry-run" over a cluster where
// two namespaces are live is the single most misleading thing this feature
// could produce.
func (p Posture) Mixed() bool {
	for _, mode := range p.Overrides {
		if (mode == ModeDryRun) != (p.Default != ModeLive) {
			return true
		}
	}
	return false
}

// Namespaces lists the namespaces running in the given mode, sorted, so the
// dashboard and the logs name them in a stable order.
func (p Posture) Namespaces(mode Mode) []string {
	var out []string
	for namespace, m := range p.Overrides {
		if m == mode {
			out = append(out, namespace)
		}
	}
	sort.Strings(out)
	return out
}

// String describes the posture in one line, for the startup log.
func (p Posture) String() string {
	if len(p.Overrides) == 0 {
		return string(p.Default) + " everywhere"
	}

	parts := []string{string(p.Default) + " by default"}
	for _, mode := range []Mode{ModeLive, ModeDryRun} {
		if names := p.Namespaces(mode); len(names) > 0 && mode != p.Default {
			parts = append(parts, fmt.Sprintf("%s in %s", mode, strings.Join(names, ", ")))
		}
	}
	return strings.Join(parts, "; ")
}

// ParsePosture reads the overrides from "namespace=mode" pairs.
//
// It is strict about the mode. "live" and "dryRun" are the only two answers,
// and a value like "true" or "off" means somebody had a different model in
// mind — which is exactly when guessing is expensive, because the guess
// decides whether remedik modifies their production namespace.
func ParsePosture(pairs []string) (map[string]Mode, error) {
	if len(pairs) == 0 {
		return nil, nil
	}

	overrides := make(map[string]Mode, len(pairs))
	for _, pair := range pairs {
		namespace, value, found := strings.Cut(strings.TrimSpace(pair), "=")
		namespace = strings.TrimSpace(namespace)
		value = strings.TrimSpace(value)

		if !found || namespace == "" || value == "" {
			return nil, fmt.Errorf(
				"namespace posture %q is not in the form \"namespace=live\" or \"namespace=dryRun\"",
				pair)
		}

		mode, err := parseMode(value)
		if err != nil {
			return nil, fmt.Errorf("namespace %q: %w", namespace, err)
		}
		if existing, ok := overrides[namespace]; ok && existing != mode {
			return nil, fmt.Errorf(
				"namespace %q is given two postures, %q and %q", namespace, existing, mode)
		}
		overrides[namespace] = mode
	}
	return overrides, nil
}

func parseMode(value string) (Mode, error) {
	switch strings.ToLower(value) {
	case "live":
		return ModeLive, nil
	case "dryrun":
		return ModeDryRun, nil
	default:
		return "", fmt.Errorf(
			"%q is not a posture; it is %q (remedik acts) or %q (remedik only reports)",
			value, ModeLive, ModeDryRun)
	}
}
