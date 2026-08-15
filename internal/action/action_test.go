package action

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeAction is a test double: it records what it was asked to do and never
// touches a cluster.
type fakeAction struct {
	name      string
	resolve   func(map[string]string, Params) (Target, error)
	planned   int
	executed  int
	planErr   error
	execErr   error
	lastPlan  Target
	lastExec  Target
	planValue string
}

func (f *fakeAction) Name() string { return f.name }

func (f *fakeAction) Resolve(labels map[string]string, p Params) (Target, error) {
	if f.resolve != nil {
		return f.resolve(labels, p)
	}
	return Target{Kind: "Deployment", Namespace: labels["namespace"], Name: labels["deployment"]}, nil
}

func (f *fakeAction) Plan(_ context.Context, t Target, _ Params) (string, error) {
	f.planned++
	f.lastPlan = t
	return f.planValue, f.planErr
}

func (f *fakeAction) Execute(_ context.Context, t Target, _ Params) (string, error) {
	f.executed++
	f.lastExec = t
	return f.planValue, f.execErr
}

func TestTarget_String(t *testing.T) {
	tests := []struct {
		name   string
		target Target
		want   string
	}{
		{"namespaced", Target{Kind: "Deployment", Namespace: "payments", Name: "api"}, "deployment/payments/api"},
		{"cluster-scoped", Target{Kind: "Node", Name: "aks-np1-0003"}, "node/aks-np1-0003"},
		{"kind is lowercased", Target{Kind: "StatefulSet", Namespace: "db", Name: "pg"}, "statefulset/db/pg"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.target.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseTarget(t *testing.T) {
	t.Run("round-trips String", func(t *testing.T) {
		for _, want := range []Target{
			{Kind: "deployment", Namespace: "payments", Name: "api"},
			{Kind: "node", Name: "aks-np1-0003"},
		} {
			got, err := ParseTarget(want.String())
			if err != nil {
				t.Fatalf("ParseTarget(%q) error = %v", want.String(), err)
			}
			if got != want {
				t.Errorf("ParseTarget(%q) = %+v, want %+v", want.String(), got, want)
			}
		}
	})

	t.Run("rejects malformed input", func(t *testing.T) {
		for _, in := range []string{"", "deployment", "a/b/c/d", "/payments/api", "deployment//api", "deployment/"} {
			if _, err := ParseTarget(in); err == nil {
				t.Errorf("ParseTarget(%q) error = nil, want an error", in)
			}
		}
	})
}

func TestTarget_IsZero(t *testing.T) {
	if !(Target{}).IsZero() {
		t.Error("empty Target reports IsZero() = false")
	}
	if (Target{Name: "x"}).IsZero() {
		t.Error("populated Target reports IsZero() = true")
	}
}

func TestParams_Get(t *testing.T) {
	p := Params{"set": "value", "empty": ""}

	tests := []struct{ key, fallback, want string }{
		{"set", "fb", "value"},
		{"empty", "fb", "fb"},
		{"absent", "fb", "fb"},
	}
	for _, tc := range tests {
		if got := p.Get(tc.key, tc.fallback); got != tc.want {
			t.Errorf("Get(%q, %q) = %q, want %q", tc.key, tc.fallback, got, tc.want)
		}
	}

	t.Run("nil map is safe", func(t *testing.T) {
		var nilParams Params
		if got := nilParams.Get("k", "fb"); got != "fb" {
			t.Errorf("Get on nil Params = %q, want fallback", got)
		}
	})
}

func TestNewRegistry(t *testing.T) {
	t.Run("indexes actions by name", func(t *testing.T) {
		r, err := NewRegistry(&fakeAction{name: "deployment.restart"}, &fakeAction{name: "node.drain"})
		if err != nil {
			t.Fatalf("NewRegistry() error = %v, want nil", err)
		}
		if r.Len() != 2 {
			t.Errorf("Len() = %d, want 2", r.Len())
		}
		if got := r.Names(); got[0] != "deployment.restart" || got[1] != "node.drain" {
			t.Errorf("Names() = %v, want them sorted", got)
		}
	})

	t.Run("rejects duplicates", func(t *testing.T) {
		_, err := NewRegistry(&fakeAction{name: "dup"}, &fakeAction{name: "dup"})
		if err == nil {
			t.Fatal("NewRegistry() error = nil, want a duplicate-name error")
		}
		if !strings.Contains(err.Error(), "duplicate action") {
			t.Errorf("error = %q, want it to mention the duplicate", err)
		}
	})

	t.Run("rejects an empty name", func(t *testing.T) {
		if _, err := NewRegistry(&fakeAction{name: ""}); err == nil {
			t.Error("NewRegistry() error = nil, want an empty-name error")
		}
	})

	t.Run("empty registry is usable", func(t *testing.T) {
		r, err := NewRegistry()
		if err != nil {
			t.Fatalf("NewRegistry() error = %v, want nil", err)
		}
		if r.Len() != 0 || r.Has("anything") {
			t.Error("empty registry does not behave as empty")
		}
	})
}

func TestRegistry_Get(t *testing.T) {
	want := &fakeAction{name: "deployment.restart"}
	r, err := NewRegistry(want)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	got, err := r.Get("deployment.restart")
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}
	if got != want {
		t.Error("Get() returned a different action than the one registered")
	}

	_, err = r.Get("node.drain")
	if !errors.Is(err, ErrUnknownAction) {
		t.Errorf("Get() error = %v, want it to wrap ErrUnknownAction", err)
	}
	// The message must name what is available: this surfaces on the
	// strategy when someone typos an action.
	if !strings.Contains(err.Error(), "deployment.restart") {
		t.Errorf("error = %q, want it to list the known actions", err)
	}
}

func TestRegistry_ValidateNames(t *testing.T) {
	r, err := NewRegistry(&fakeAction{name: "deployment.restart"})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	if err := r.ValidateNames([]string{"deployment.restart"}); err != nil {
		t.Errorf("ValidateNames() error = %v, want nil", err)
	}
	if err := r.ValidateNames(nil); err != nil {
		t.Errorf("ValidateNames(nil) error = %v, want nil", err)
	}

	err = r.ValidateNames([]string{"deployment.restart", "deployment.restrt"})
	if !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("ValidateNames() error = %v, want ErrUnknownAction", err)
	}
	if !strings.Contains(err.Error(), "step 1") {
		t.Errorf("error = %q, want it to identify the offending step", err)
	}
}

// Dry-run must never reach Execute. The interface split is what guarantees
// it: this test pins the guarantee at the contract level.
func TestAction_PlanDoesNotExecute(t *testing.T) {
	a := &fakeAction{name: "deployment.restart", planValue: "would patch deployment payments/api"}

	target, err := a.Resolve(map[string]string{"namespace": "payments", "deployment": "api"}, nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	plan, err := a.Plan(context.Background(), target, nil)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan == "" {
		t.Error("Plan() returned an empty description")
	}
	if a.executed != 0 {
		t.Errorf("Execute was called %d times during Plan", a.executed)
	}
	if a.lastPlan.String() != "deployment/payments/api" {
		t.Errorf("Plan received target %q", a.lastPlan)
	}
}
