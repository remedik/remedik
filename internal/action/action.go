// Package action defines the contract every remediation verb implements,
// and the registry the engine resolves step names through.
//
// The contract has three parts, and the split is what makes dry-run
// trustworthy rather than aspirational:
//
//	Resolve — work out which object the step acts on, from the alert's
//	          labels and the step's parameters. No cluster access.
//	Plan    — describe what Execute would do, in one human-readable line.
//	          Read-only: dry-run calls Plan and nothing else.
//	Execute — do it, and return what was done.
//
// Because dry-run never reaches Execute, a Simulated remediation cannot
// mutate the cluster even if an action is buggy: the mutating code path is
// not called at all.
//
// This package depends on the standard library only. Concrete actions hold
// whatever client they need, injected when they are constructed.
package action

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrUnknownAction is returned when a step names an action the operator
// does not implement.
var ErrUnknownAction = errors.New("unknown action")

// Target identifies the object a step acts on. Namespace is empty for
// cluster-scoped objects such as nodes.
type Target struct {
	Kind      string
	Namespace string
	Name      string
}

// String renders the target as "kind/namespace/name", or "kind/name" when
// cluster-scoped. This is the form stored on Remediation resources and
// shown in kubectl output, so it must stay stable.
func (t Target) String() string {
	kind := strings.ToLower(t.Kind)
	if t.Namespace == "" {
		return kind + "/" + t.Name
	}
	return kind + "/" + t.Namespace + "/" + t.Name
}

// IsZero reports whether the target is unset.
func (t Target) IsZero() bool { return t.Kind == "" && t.Namespace == "" && t.Name == "" }

// ParseTarget is the inverse of String.
func ParseTarget(s string) (Target, error) {
	switch parts := strings.Split(s, "/"); len(parts) {
	case 2:
		if parts[0] == "" || parts[1] == "" {
			return Target{}, fmt.Errorf("malformed target %q", s)
		}
		return Target{Kind: parts[0], Name: parts[1]}, nil
	case 3:
		if parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return Target{}, fmt.Errorf("malformed target %q", s)
		}
		return Target{Kind: parts[0], Namespace: parts[1], Name: parts[2]}, nil
	default:
		return Target{}, fmt.Errorf("malformed target %q: want kind/name or kind/namespace/name", s)
	}
}

// Params are a step's parameters, taken verbatim from the strategy.
type Params map[string]string

// Get returns the value for key, or fallback when unset or empty.
func (p Params) Get(key, fallback string) string {
	if v, ok := p[key]; ok && v != "" {
		return v
	}
	return fallback
}

// Action is one remediation verb, named "noun.verb" — for example
// "deployment.restart".
//
// Implementations must be safe for concurrent use and idempotent: the
// engine may retry a step, and an action that cannot be repeated safely
// must detect that itself rather than assume it runs once.
type Action interface {
	// Name is the verb as written in a strategy's steps.
	Name() string

	// Resolve determines the object to act on from the alert's labels and
	// the step's parameters, without contacting the cluster. It returns an
	// error when the alert does not carry enough information.
	Resolve(labels map[string]string, params Params) (Target, error)

	// Plan returns a one-line description of what Execute would do. It
	// must not mutate anything; dry-run calls only Plan.
	Plan(ctx context.Context, target Target, params Params) (string, error)

	// Execute performs the action and returns what it did, in the same
	// form Plan describes.
	Execute(ctx context.Context, target Target, params Params) (string, error)
}

// Registry resolves action names to implementations. It is read-only after
// construction, so it is safe to share across reconciles.
type Registry struct {
	actions map[string]Action
}

// NewRegistry indexes actions by name. It returns an error if two actions
// claim the same name or an action has an empty name — a silent collision
// would mean an alert is remediated by something other than the strategy
// author intended.
func NewRegistry(actions ...Action) (*Registry, error) {
	r := &Registry{actions: make(map[string]Action, len(actions))}
	for _, a := range actions {
		name := a.Name()
		if name == "" {
			return nil, fmt.Errorf("action %T has an empty name", a)
		}
		if existing, dup := r.actions[name]; dup {
			return nil, fmt.Errorf("duplicate action %q registered by %T and %T", name, existing, a)
		}
		r.actions[name] = a
	}
	return r, nil
}

// Get returns the action registered under name.
func (r *Registry) Get(name string) (Action, error) {
	if a, ok := r.actions[name]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("%w %q (known actions: %s)",
		ErrUnknownAction, name, strings.Join(r.Names(), ", "))
}

// Has reports whether name is registered.
func (r *Registry) Has(name string) bool {
	_, ok := r.actions[name]
	return ok
}

// Names lists every registered action, sorted, for error messages and
// diagnostics.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.actions))
	for name := range r.actions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Len reports how many actions are registered.
func (r *Registry) Len() int { return len(r.actions) }

// ValidateNames reports the first step naming an unknown action. The engine
// calls it when a strategy is applied, so an unusable strategy is reported
// on the resource rather than discovered mid-incident.
func (r *Registry) ValidateNames(names []string) error {
	for i, name := range names {
		if !r.Has(name) {
			return fmt.Errorf("step %d: %w %q (known actions: %s)",
				i, ErrUnknownAction, name, strings.Join(r.Names(), ", "))
		}
	}
	return nil
}
