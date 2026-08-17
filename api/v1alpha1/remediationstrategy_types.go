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

	// BlastRadius bounds how degraded the workload may already be. The
	// other two guards ask about time; this one asks about state, and it is
	// what bounds the actions that remove capacity rather than replacing
	// it.
	//
	// It needs to read the workload behind the target, so the chart must
	// grant that with `guards.blastRadius.enabled=true`. Without the
	// permission the guard refuses rather than allows: a guard that permits
	// an execution when it could not evaluate its own condition is not a
	// guard.
	//
	// +optional
	BlastRadius *BlastRadius `json:"blastRadius,omitempty"`
}

// BlastRadius bounds how broken a workload may already be before remedik
// adds to it. Both limits are opt-in; zero means unenforced.
type BlastRadius struct {
	// MinAvailable refuses while the workload has this many available
	// replicas or fewer — "never touch the last one".
	//
	// +kubebuilder:validation:Minimum=0
	// +optional
	MinAvailable int32 `json:"minAvailable,omitempty"`

	// MaxUnavailablePercent refuses while at least this share of the
	// workload is already unavailable — "do not add to something that is
	// already struggling".
	//
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	MaxUnavailablePercent int32 `json:"maxUnavailablePercent,omitempty"`
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

	// Steps is the escalation: what to do once the remediation has failed
	// and no retries remain. It is how "and if that does not work, tell
	// somebody" is written down — usually a "webhook.call" to PagerDuty or
	// Alertmanager, or a "job.run" that hands the incident to a pipeline.
	//
	// Escalation steps are ordinary actions and are recorded separately
	// from the remediation's own, because "the restart failed" and "the
	// page failed" are different facts.
	//
	// Two things about them are unlike every other step, and both are
	// deliberate:
	//
	//   - They run even when the remediation was a dry run. A trial run is
	//     exactly when an operator wants to see the escalation path work,
	//     and the steps are told the run was simulated (see the labels on
	//     RemediationSpec.EscalationSteps) so nobody is paged for an
	//     incident that did not happen. Put nothing here that changes the
	//     cluster.
	//   - They do not change the outcome. The remediation failed; a
	//     successful page does not make it a success, and a failed page
	//     does not make it worse.
	//
	// +optional
	// +kubebuilder:validation:MaxItems=8
	Steps []Step `json:"steps,omitempty"`

	// Mode decides how the escalation steps are run.
	//
	// Unlike the remediation's own plan, a failed escalation step never
	// prevents the ones after it: the steps are alternative ways to reach a
	// person, so a fallback that stops at the first failure is a single point
	// of failure that only shows itself on the night it matters.
	//
	//   - "all" (the default) runs every step. Use it for channels that
	//     should all happen — page somebody and file a ticket.
	//   - "firstSuccess" runs them in order until one gets through, and skips
	//     the rest. Use it for an ordered fallback, so both channels working
	//     does not mean being paged twice.
	//
	// Either way the escalation is Succeeded when at least one step
	// succeeded, because the question the record answers is whether anybody
	// was told.
	//
	// +kubebuilder:default=all
	// +kubebuilder:validation:Enum=all;firstSuccess
	// +optional
	Mode EscalationMode `json:"mode,omitempty"`
}

// EscalationMode is how the escalation's steps are run.
type EscalationMode string

const (
	// EscalationModeAll runs every step. It is the default because it is what
	// a working configuration already does: when every step succeeds, every
	// step runs. Making firstSuccess the default would silently stop running
	// the second step for anybody who wanted a page and a ticket.
	EscalationModeAll EscalationMode = "all"
	// EscalationModeFirstSuccess stops at the first step that gets through.
	EscalationModeFirstSuccess EscalationMode = "firstSuccess"
)

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
