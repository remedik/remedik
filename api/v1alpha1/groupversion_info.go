// Package v1alpha1 contains the remedik API types.
//
// The API is alpha: it may change between releases, and every change is
// specified in openspec/ before it lands. Two kinds live here:
//
//   - RemediationStrategy — the declarative contract that maps alerts to
//     remediation behavior. Cluster-scoped, because alerts arrive with a
//     namespace label rather than from a namespace.
//   - Remediation — one execution record. Namespaced to the operator's own
//     namespace, so the audit trail lives in one predictable place and its
//     RBAC stays narrow.
//
// The package depends on k8s.io/apimachinery only — deliberately not on
// controller-runtime. An API package is imported by clients, controllers
// and tools alike, so it must stay cheap to import.
//
// +kubebuilder:object:generate=true
// +groupName=remedik.dev
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is the group and version of every type in this package.
	GroupVersion = schema.GroupVersion{Group: "remedik.dev", Version: "v1alpha1"}

	// SchemeBuilder collects the functions that register these types.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds these types to a runtime scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// Resource returns a GroupResource in this API group, for error messages
// and RESTMapper lookups.
func Resource(resource string) schema.GroupResource {
	return GroupVersion.WithResource(resource).GroupResource()
}

// addKnownTypes registers every kind in this package. Keeping the list in
// one place, rather than an init() per file, makes the API surface
// reviewable at a glance.
func addKnownTypes(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion,
		&RemediationStrategy{}, &RemediationStrategyList{},
		&Remediation{}, &RemediationList{},
	)
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}

// Labels applied by the engine to Remediation resources. They are part of
// the API surface: users select on them, so they must stay stable.
const (
	// LabelStrategy carries the RemediationStrategy that produced the
	// record, so `kubectl get rem -l remedik.dev/strategy=<name>` works and
	// history lookups stay cheap.
	LabelStrategy = "remedik.dev/strategy"
	// LabelFingerprint carries the triggering alert's fingerprint.
	LabelFingerprint = "remedik.dev/fingerprint"

	// LabelGaveUp marks a Remediation that records remedik giving up rather
	// than remediating: the giveUpAfter guard tripped, so this record has no
	// steps, exists to run the escalation and to be found later, and counts
	// toward no guard.
	//
	// A label rather than a status field because it is queried: the sink asks
	// whether it has already given up on this target inside the window, so
	// that a page happens once per trip rather than on every repeat delivery.
	LabelGaveUp = "remedik.dev/gave-up"

	// LabelPaused marks a record made while the kill switch was on, so a run
	// of simulations in the audit trail is distinguishable from a dry-run
	// trial nobody remembers configuring.
	LabelPaused = "remedik.dev/paused"

	// AnnotationPauseReason carries the note left with the pause, so somebody
	// reading the record later learns why rather than only that.
	AnnotationPauseReason = "remedik.dev/pause-reason"

	// AnnotationGaveUpReason carries the guard's explanation from the sink,
	// which creates the record, to the reconciler, which writes the status.
	// An annotation because it is prose for a person, not an identifier.
	AnnotationGaveUpReason = "remedik.dev/gave-up-reason"
)
