package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/events"

	"github.com/ratyx/remedik/internal/action"
)

// Event reasons published on a remediated object. They are a closed set
// because people select on them: `kubectl get events --field-selector
// reason=Remediated` has to keep working.
const (
	// EventReasonRemediating is published before a step runs.
	EventReasonRemediating = "Remediating"
	// EventReasonRemediated is published when a step completed.
	EventReasonRemediated = "Remediated"
	// EventReasonRemediationFailed is published when a step failed.
	EventReasonRemediationFailed = "RemediationFailed"
)

// The events API also carries an `action` field alongside the reason.
// remedik puts the action's name there, so `kubectl get events -o wide`
// shows which verb touched the object without anyone opening the record.

// TargetEvents publishes step progress on the object being remediated.
//
// The engine holds targets as "kind/namespace/name" strings, and an event
// needs a group, a version and a kind. Resolving that through the manager's
// RESTMapper rather than a table means every action added later gets
// addressable events with no entry to add anywhere.
type TargetEvents struct {
	// Recorder publishes the events.
	Recorder events.EventRecorder
	// Mapper turns a target's kind into an addressable API kind.
	Mapper meta.RESTMapper
	// Remediation names the record this step belongs to, so a reader can
	// get from the event to the full audit trail in one step.
	Remediation string
	// Strategy names the strategy responsible, which is the thing a reader
	// will want to edit or disable.
	Strategy string
	// Namespace is where the Remediation record lives, so the event can say
	// where to look for it.
	Namespace string
	// Logger reports events that could not be published. Required.
	Logger *slog.Logger
}

// Starting implements StepEvents.
func (e *TargetEvents) Starting(ctx context.Context, target action.Target, actionName string, index int) {
	e.publish(ctx, target, corev1.EventTypeNormal, EventReasonRemediating, actionName,
		"step %d: running %s (remediation %s/%s, strategy %s)",
		index+1, actionName, e.Namespace, e.Remediation, e.Strategy)
}

// Succeeded implements StepEvents.
func (e *TargetEvents) Succeeded(
	ctx context.Context, target action.Target, actionName string, index int, summary string,
) {
	if summary == "" {
		summary = actionName + " completed"
	}
	e.publish(ctx, target, corev1.EventTypeNormal, EventReasonRemediated, actionName,
		"step %d: %s (remediation %s/%s, strategy %s)",
		index+1, summary, e.Namespace, e.Remediation, e.Strategy)
}

// Finished implements StepEvents.
func (e *TargetEvents) Finished(
	ctx context.Context, target action.Target, actionName string, index int, err error,
) {
	if err == nil {
		return
	}
	e.publish(ctx, target, corev1.EventTypeWarning, EventReasonRemediationFailed, actionName,
		"step %d: %s failed: %v (remediation %s/%s, strategy %s)",
		index+1, actionName, err, e.Namespace, e.Remediation, e.Strategy)
}

// publish addresses the target and sends one event.
//
// Every failure here is logged and swallowed. An event is an explanation,
// not the remediation: refusing to restart a Deployment because its kind
// could not be resolved for an event would be the tail wagging the dog.
func (e *TargetEvents) publish(
	_ context.Context, target action.Target, eventType, reason, actionName, format string, args ...any,
) {
	if e.Recorder == nil {
		return
	}

	ref, err := e.reference(target)
	if err != nil {
		e.Logger.Debug("not publishing an event: the target could not be addressed",
			"target", target.String(), "reason", reason, "err", err)
		return
	}

	// The events API carries `regarding` and `related` objects. remedik has
	// no second object to name: the remediation record is in the message,
	// where a reader can copy it, rather than as a reference nothing links
	// back from.
	e.Recorder.Eventf(ref, nil, eventType, reason, actionName, format, args...)
}

// reference turns a target into something the event recorder can address.
//
// client-go's reference resolution passes an *ObjectReference straight
// through, which is what lets the engine publish on an object it never
// fetched — the alternative would be a read per event, on a path that runs
// during an incident.
func (e *TargetEvents) reference(target action.Target) (*corev1.ObjectReference, error) {
	if target.IsZero() {
		return nil, fmt.Errorf("empty target")
	}
	if e.Mapper == nil {
		return nil, fmt.Errorf("no RESTMapper")
	}

	// Target kinds are written the way a person says them — "deployment",
	// "node" — so they are resolved as resources rather than kinds, which
	// is what accepts singular, plural and short forms alike.
	gvr, err := e.Mapper.ResourceFor(schema.GroupVersionResource{
		Resource: strings.ToLower(target.Kind),
	})
	if err != nil {
		return nil, fmt.Errorf("resolve kind %q: %w", target.Kind, err)
	}

	kinds, err := e.Mapper.KindFor(gvr)
	if err != nil {
		return nil, fmt.Errorf("resolve kind for %s: %w", gvr, err)
	}

	return &corev1.ObjectReference{
		APIVersion: kinds.GroupVersion().String(),
		Kind:       kinds.Kind,
		Namespace:  target.Namespace,
		Name:       target.Name,
	}, nil
}

// Compile-time proof that the publisher satisfies the contract.
var _ StepEvents = (*TargetEvents)(nil)
