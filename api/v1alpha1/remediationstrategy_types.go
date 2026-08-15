package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ExecutionMode decides how much autonomy a strategy has.
//
// +kubebuilder:validation:Enum=auto
type ExecutionMode string

const (
	// ExecutionModeAuto remediates without asking. It is the only mode
	// implemented in v1alpha1; "approval" and "manual" arrive with the
	// Slack change, and the enum is widened then so that a manifest
	// written against a newer remedik fails loudly on an older one
	// instead of silently remediating without approval.
	ExecutionModeAuto ExecutionMode = "auto"
)

// RemediationStrategySpec defines what a strategy matches and what it does.
type RemediationStrategySpec struct {
	// Enabled is the per-strategy gate. A disabled strategy never matches
	// an alert; it stays in the cluster so history and intent are kept.
	//
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Trigger selects the alerts this strategy handles.
	//
	// +kubebuilder:validation:Required
	Trigger Trigger `json:"trigger"`

	// Execution controls how the strategy runs.
	//
	// +optional
	Execution Execution `json:"execution,omitempty"`

	// Guards bound how often the strategy may act.
	//
	// +optional
	Guards Guards `json:"guards,omitempty"`

	// Steps are the remediation actions, executed in order. Execution
	// stops at the first failed step.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Steps []Step `json:"steps"`

	// OnFailure decides what happens when a step fails.
	//
	// +optional
	OnFailure OnFailure `json:"onFailure,omitempty"`
}

// Trigger selects alerts by label.
type Trigger struct {
	// Match is a set of label equality matchers. Every entry must equal
	// the alert's corresponding label for the strategy to match.
	//
	// At least one matcher is required: a strategy that matched every
	// alert would turn one mistake into cluster-wide remediation. Matching
	// is exact — no regular expressions — so the outcome stays predictable
	// at 3am. When several strategies match, the most specific one wins,
	// with ties broken by name.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinProperties=1
	Match map[string]string `json:"match"`
}

// Execution controls autonomy and notifications.
type Execution struct {
	// Mode is how the strategy runs. Only "auto" is supported in
	// v1alpha1.
	//
	// +kubebuilder:default=auto
	// +optional
	Mode ExecutionMode `json:"mode,omitempty"`
}

// Guards bound how often a strategy may act. Both limits are opt-in: zero
// means the limit is not enforced. Stopping a strategy is `enabled: false`,
// never a zero limit — otherwise an unset field would silently disable
// remediation.
type Guards struct {
	// Cooldown is the minimum time between one execution completing on a
	// target and the next starting on that same target. It is what stops
	// a flapping alert from restarting a deployment in a loop.
	//
	// +optional
	Cooldown *metav1.Duration `json:"cooldown,omitempty"`

	// MaxPerHour caps how many executions this strategy may start in the
	// trailing hour, so an alert storm cannot amplify into a cluster-wide
	// event.
	//
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxPerHour int32 `json:"maxPerHour,omitempty"`
}

// Step is one remediation action.
type Step struct {
	// Action is the verb to run, in "noun.verb" form — for example
	// "deployment.restart". Unknown actions fail validation at admission
	// time when possible, and at execution time otherwise.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Action string `json:"action"`

	// With holds the action's parameters. Values are strings so the schema
	// stays stable across actions; each action documents and validates its
	// own keys.
	//
	// +optional
	With map[string]string `json:"with,omitempty"`
}

// OnFailure decides what happens after a step fails.
type OnFailure struct {
	// Retries is how many additional attempts the whole strategy gets
	// after a failure, with exponential backoff between them. Zero means
	// one attempt and no retry.
	//
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10
	// +optional
	Retries int32 `json:"retries,omitempty"`
}

// RemediationStrategyStatus reports observed state.
type RemediationStrategyStatus struct {
	// Conditions follow the standard Kubernetes convention. "Ready"
	// reports whether the strategy is usable: a strategy referencing an
	// unknown action is accepted by the schema but not Ready.
	//
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastExecutionTime is when this strategy last started an execution.
	//
	// +optional
	LastExecutionTime *metav1.Time `json:"lastExecutionTime,omitempty"`

	// ExecutionCount is how many executions this strategy has started
	// since the resource was created. It is a coarse counter for humans;
	// metrics remain the source for rates.
	//
	// +optional
	ExecutionCount int64 `json:"executionCount,omitempty"`

	// ObservedGeneration is the .metadata.generation this status reflects.
	//
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// IsEnabled reports whether the strategy may match alerts. The field is a
// pointer so that "unset" is distinguishable from "false"; unset means
// enabled.
func (s *RemediationStrategy) IsEnabled() bool {
	return s.Spec.Enabled == nil || *s.Spec.Enabled
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=rstrat;rs
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Enabled",type=boolean,JSONPath=`.spec.enabled`
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.execution.mode`
// +kubebuilder:printcolumn:name="Runs",type=integer,JSONPath=`.status.executionCount`
// +kubebuilder:printcolumn:name="Last Run",type=date,JSONPath=`.status.lastExecutionTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RemediationStrategy maps alerts to remediation behavior.
type RemediationStrategy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RemediationStrategySpec   `json:"spec,omitempty"`
	Status RemediationStrategyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RemediationStrategyList contains a list of RemediationStrategy.
type RemediationStrategyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RemediationStrategy `json:"items"`
}
