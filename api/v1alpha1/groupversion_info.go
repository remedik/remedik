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
// +kubebuilder:object:generate=true
// +groupName=remedik.dev
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is the group and version of every type in this package.
	GroupVersion = schema.GroupVersion{Group: "remedik.dev", Version: "v1alpha1"}

	// SchemeBuilder registers these types with a runtime scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds these types to a runtime scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
