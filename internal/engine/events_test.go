package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/ratyx/remedik/internal/action"
)

// capturedEvent is one event as the API server would have stored it.
type capturedEvent struct {
	object    *corev1.ObjectReference
	eventType string
	reason    string
	action    string
	message   string
}

// capturingRecorder implements events.EventRecorder, keeping the object
// reference as well as the text — the reference is the part that decides
// whether `kubectl describe deployment` will show anything at all.
type capturingRecorder struct {
	mu     sync.Mutex
	events []capturedEvent
}

func (r *capturingRecorder) Eventf(
	object runtime.Object, _ runtime.Object, eventType, reason, actionName, format string, args ...any,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ref, _ := object.(*corev1.ObjectReference)
	r.events = append(r.events, capturedEvent{
		object: ref, eventType: eventType, reason: reason,
		action: actionName, message: sprintf(format, args...),
	})
}

func (r *capturingRecorder) snapshot() []capturedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]capturedEvent(nil), r.events...)
}

func sprintf(format string, args ...any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

// errAlwaysFails stands in for whatever an action returned.
var errAlwaysFails = errors.New("conflict on the pod template")

// testMapper knows the handful of kinds the tests address.
func testMapper() meta.RESTMapper {
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		{Group: "apps", Version: "v1"},
		{Group: "", Version: "v1"},
	})
	mapper.Add(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, meta.RESTScopeNamespace)
	mapper.Add(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}, meta.RESTScopeNamespace)
	mapper.Add(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Node"}, meta.RESTScopeRoot)
	return mapper
}

func newTargetEvents(recorder *capturingRecorder, mapper meta.RESTMapper) *TargetEvents {
	return &TargetEvents{
		Recorder:    recorder,
		Mapper:      mapper,
		Remediation: "pod-crashloop-x7k2q",
		Strategy:    "pod-crashloop",
		Namespace:   "remedik",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestTargetEventsAddressTheRemediatedObject(t *testing.T) {
	tests := []struct {
		name           string
		target         action.Target
		wantAPIVersion string
		wantKind       string
		wantNamespace  string
	}{
		{
			name:           "a namespaced workload",
			target:         action.Target{Kind: "Deployment", Namespace: "payments", Name: "api"},
			wantAPIVersion: "apps/v1",
			wantKind:       "Deployment",
			wantNamespace:  "payments",
		},
		{
			// Nodes are cluster-scoped: an event on one must not claim a
			// namespace it does not have.
			name:           "a cluster-scoped object",
			target:         action.Target{Kind: "Node", Name: "aks-np1-0003"},
			wantAPIVersion: "v1",
			wantKind:       "Node",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &capturingRecorder{}
			events := newTargetEvents(recorder, testMapper())

			events.Starting(context.Background(), tc.target, "deployment.restart", 0)

			got := recorder.snapshot()
			if len(got) != 1 {
				t.Fatalf("published %d events, want 1", len(got))
			}
			ref := got[0].object
			if ref == nil {
				t.Fatal("the event carried no object reference, so kubectl describe would show nothing")
			}
			if ref.APIVersion != tc.wantAPIVersion || ref.Kind != tc.wantKind {
				t.Errorf("reference = %s %s, want %s %s",
					ref.APIVersion, ref.Kind, tc.wantAPIVersion, tc.wantKind)
			}
			if ref.Namespace != tc.wantNamespace {
				t.Errorf("reference namespace = %q, want %q", ref.Namespace, tc.wantNamespace)
			}
			if ref.Name != tc.target.Name {
				t.Errorf("reference name = %q, want %q", ref.Name, tc.target.Name)
			}
		})
	}
}

func TestTargetEventsNameTheRemediationAndStrategy(t *testing.T) {
	recorder := &capturingRecorder{}
	events := newTargetEvents(recorder, testMapper())
	target := action.Target{Kind: "Deployment", Namespace: "payments", Name: "api"}

	events.Starting(context.Background(), target, "deployment.restart", 0)
	events.Succeeded(context.Background(), target, "deployment.restart", 0, "restarted deployment/payments/api")
	events.Finished(context.Background(), target, "deployment.restart", 1, errAlwaysFails)

	got := recorder.snapshot()
	if len(got) != 3 {
		t.Fatalf("published %d events, want 3", len(got))
	}

	// Every event has to carry the way back to the full record and to the
	// manifest that decided this: without them the reader knows something
	// happened and nothing about why.
	for i, ev := range got {
		if !strings.Contains(ev.message, "pod-crashloop-x7k2q") {
			t.Errorf("event %d = %q, want it to name the remediation", i, ev.message)
		}
		if !strings.Contains(ev.message, "strategy pod-crashloop") {
			t.Errorf("event %d = %q, want it to name the strategy", i, ev.message)
		}
	}

	if got[0].reason != EventReasonRemediating || got[0].eventType != corev1.EventTypeNormal {
		t.Errorf("first event = %s/%s, want Normal/%s", got[0].eventType, got[0].reason, EventReasonRemediating)
	}
	if got[1].reason != EventReasonRemediated {
		t.Errorf("second event reason = %s, want %s", got[1].reason, EventReasonRemediated)
	}
	if got[2].reason != EventReasonRemediationFailed || got[2].eventType != corev1.EventTypeWarning {
		t.Errorf("third event = %s/%s, want Warning/%s",
			got[2].eventType, got[2].reason, EventReasonRemediationFailed)
	}
	// Steps are numbered from one on the page, not from zero.
	if !strings.Contains(got[2].message, "step 2") {
		t.Errorf("third event = %q, want it to say step 2", got[2].message)
	}
}

func TestTargetEventsSkipWhatTheyCannotAddress(t *testing.T) {
	tests := []struct {
		name   string
		target action.Target
		mapper meta.RESTMapper
	}{
		{
			name:   "an unknown kind",
			target: action.Target{Kind: "Widget", Namespace: "payments", Name: "api"},
			mapper: testMapper(),
		},
		{
			name:   "an empty target",
			target: action.Target{},
			mapper: testMapper(),
		},
		{
			name:   "no mapper at all",
			target: action.Target{Kind: "Deployment", Namespace: "payments", Name: "api"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &capturingRecorder{}
			events := newTargetEvents(recorder, tc.mapper)

			// An event is an explanation, not the remediation. Failing to
			// publish one must never reach the caller.
			events.Starting(context.Background(), tc.target, "deployment.restart", 0)
			events.Succeeded(context.Background(), tc.target, "deployment.restart", 0, "done")
			events.Finished(context.Background(), tc.target, "deployment.restart", 0, errAlwaysFails)

			if got := recorder.snapshot(); len(got) != 0 {
				t.Errorf("published %d events for an unaddressable target, want 0", len(got))
			}
		})
	}
}

func TestTargetEventsIgnoreASuccessfulFinish(t *testing.T) {
	recorder := &capturingRecorder{}
	events := newTargetEvents(recorder, testMapper())

	events.Finished(context.Background(), action.Target{
		Kind: "Deployment", Namespace: "payments", Name: "api",
	}, "deployment.restart", 0, nil)

	if got := recorder.snapshot(); len(got) != 0 {
		t.Errorf("published %d events for a nil error, want 0 — success is announced by Succeeded", len(got))
	}
}
