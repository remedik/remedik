// Package action defines the contract every remediation verb implements,
// and the registry the engine resolves step names through.
//
// The contract is split so that dry-run is trustworthy rather than
// aspirational:
//
//	Resolve — work out which object the step acts on, from the alert's
//	          labels and the step's parameters. No cluster access.
//	Plan    — describe what Execute would do. Read-only: dry-run calls Plan
//	          and nothing else.
//	Execute — do it, and report what was done.
//	Verify  — optional, read-only: did it actually work? Called after
//	          Execute, never in dry-run. See Verifier.
//
// Because dry-run never reaches Execute, a Simulated remediation cannot
// mutate the cluster even if an action is buggy: the mutating code path is
// not called at all.
//
// Plan, Execute and Verify all report a Result rather than a string, so
// that an action can say what it specifically knows — a replica count, an
// exit code, the equivalent kubectl command — without burying it in prose.
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
	"time"
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

// Duration returns the value for key parsed as a Go duration, or fallback
// when unset. An unparseable value is an error rather than a silent
// fallback: a step that asked for "30" and got the default would be a
// remediation behaving differently from what its author wrote.
func (p Params) Duration(key string, fallback time.Duration) (time.Duration, error) {
	raw := p.Get(key, "")
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parameter %q: %w (want a Go duration such as \"90s\" or \"2m\")", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("parameter %q: %s is not a positive duration", key, raw)
	}
	return d, nil
}

// Result is what an action reports about one step.
//
// It is a struct rather than a longer return signature because the
// catalogue is going to grow: adding a field here is a compile-safe change
// for every action that does not set it, and adding a return value is not.
type Result struct {
	// Summary is the one human-readable line describing what was done, or
	// in dry-run what would be done. It is what the record and the
	// dashboard lead with, so it should read as a sentence and name the
	// object it acted on.
	Summary string

	// Kubectl is the equivalent command a human would have typed. It is
	// never executed and nothing parses it: it exists so that someone who
	// has never read remedik's source can tell exactly what happened, which
	// is the cheapest trust an automation can buy.
	Kubectl string

	// Outputs carries whatever this action specifically knows — replicas
	// before and after, an exit code, the revision rolled back to. Keeping
	// it out of Summary means a machine can read it and a person is not
	// made to parse prose.
	Outputs map[string]string
}

// Output records one structured value, allocating the map on first use so
// that actions do not each have to.
func (r *Result) Output(key, value string) {
	if r.Outputs == nil {
		r.Outputs = make(map[string]string, 4)
	}
	r.Outputs[key] = value
}

// Request is everything an action is given about the work it is doing.
//
// It is a struct for the same reason Result is: the catalogue grows, and a
// new field is a compile-safe change for every action that ignores it. The
// alert's labels are here because a verb whose job is handing the incident
// to something outside the cluster — a webhook, a Job — is useless without
// them, while a verb that restarts a Deployment simply never reads them.
type Request struct {
	// Target is the object this step acts on. It may be zero for an action
	// that acts on nothing in the cluster.
	Target Target

	// Params are the step's parameters, verbatim from the strategy.
	Params Params

	// Labels are the triggering alert's labels. An action passing them
	// outward must treat them as untrusted: they are whatever the alert
	// carried.
	Labels map[string]string

	// Remediation and Strategy name the record and the manifest
	// responsible, so an action can tell something outside the cluster
	// where this came from.
	Remediation string
	Strategy    string

	// Namespace is where the Remediation record lives — and, for actions
	// that create objects, the only namespace they may create them in.
	Namespace string

	// DryRun reports the operator's posture. Execute is never called in
	// dry-run, so this is for Plan: an action can say what it would have
	// sent, including that it would have said "this was a simulation".
	DryRun bool
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

	// Plan describes what Execute would do. It must not mutate anything;
	// dry-run calls only Plan.
	Plan(ctx context.Context, req Request) (Result, error)

	// Execute performs the action and reports what it did, in the same form
	// Plan describes.
	Execute(ctx context.Context, req Request) (Result, error)
}

// Verifier is an action that can check its own work.
//
// It is deliberately separate from Action. Some verbs have nothing to
// verify — a cordon either applied or returned an error — and forcing them
// to implement a check would produce one that always succeeds, which is
// worse than no check because it looks like one.
//
// The engine calls Verify after Execute and never in dry-run, where nothing
// was executed for it to verify. A failed Verify fails the step: if the
// rollout did not complete, the remediation did not work, and the retry
// budget is the mechanism for trying again.
type Verifier interface {
	// Verify reports whether the action achieved what it set out to do.
	//
	// It receives what Execute reported, because a check usually needs it:
	// an eviction is confirmed by the pod's UID changing, and only Execute
	// knows which UID it evicted.
	//
	// It must be read-only, and must return within the deadline on ctx: an
	// attempt runs to completion inside a single reconcile, so a check that
	// waits forever holds every other remediation behind it.
	Verify(ctx context.Context, req Request, executed Result) (Result, error)
}

// VerifyTimeoutParam is the step parameter bounding a post-condition check.
const VerifyTimeoutParam = "verifyTimeout"

// DefaultVerifyTimeout bounds a check that does not set one. It covers a
// rolling restart of a small workload and is short enough that a stuck
// check is not mistaken for a stuck operator.
const DefaultVerifyTimeout = 60 * time.Second

// MaxVerifyTimeout caps what a step may ask for. Executions are serialised,
// so a long check is time no other remediation can use.
const MaxVerifyTimeout = 10 * time.Minute

// WithVerifyDeadline guarantees a post-condition check cannot run forever.
//
// The engine always bounds Verify, so this is defence in depth: an action
// polling with no deadline would hold the single reconcile worker for good,
// which is the one failure an operator cannot recover from without
// restarting the process. A caller that already set a deadline keeps it.
func WithVerifyDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, DefaultVerifyTimeout)
}

// VerifyTimeout reads the bound for a step's post-condition check.
func VerifyTimeout(params Params) (time.Duration, error) {
	d, err := params.Duration(VerifyTimeoutParam, DefaultVerifyTimeout)
	if err != nil {
		return 0, err
	}
	if d > MaxVerifyTimeout {
		return 0, fmt.Errorf("parameter %q: %s exceeds the maximum of %s",
			VerifyTimeoutParam, d, MaxVerifyTimeout)
	}
	return d, nil
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
