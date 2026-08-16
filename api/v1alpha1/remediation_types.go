package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RemediationState is the lifecycle state of one execution.
//
// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed;Simulated
type RemediationState string

const (
	// RemediationStatePending means the execution was created but has not
	// started running.
	RemediationStatePending RemediationState = "Pending"
	// RemediationStateRunning means steps are executing.
	RemediationStateRunning RemediationState = "Running"
	// RemediationStateSucceeded means every step completed.
	RemediationStateSucceeded RemediationState = "Succeeded"
	// RemediationStateFailed means a step failed and no retries remain.
	RemediationStateFailed RemediationState = "Failed"
	// RemediationStateSimulated means dry-run was active: the plan was
	// produced and recorded, and nothing was mutated.
	RemediationStateSimulated RemediationState = "Simulated"
)

// IsTerminal reports whether no further work will happen in this state.
func (s RemediationState) IsTerminal() bool {
	switch s {
	case RemediationStateSucceeded, RemediationStateFailed, RemediationStateSimulated:
		return true
	case RemediationStatePending, RemediationStateRunning:
		return false
	default:
		return false
	}
}

// Failure reasons recorded on a Remediation. They are a closed set because
// they surface as metric labels and Kubernetes event reasons.
const (
	// ReasonInterrupted means the operator restarted mid-execution. The
	// execution is failed rather than resumed: resuming a mutating step
	// safely needs per-action idempotency guarantees that do not exist
	// yet, and repeating an action silently is the worse outcome.
	ReasonInterrupted = "Interrupted"
	// ReasonStepFailed means an action returned an error.
	ReasonStepFailed = "StepFailed"
	// ReasonGuardRejected means a guard refused the execution.
	ReasonGuardRejected = "GuardRejected"
	// ReasonUnknownAction means a step referenced an action the operator
	// does not implement.
	ReasonUnknownAction = "UnknownAction"
)

// RemediationSpec is the immutable record of what triggered this execution
// and what it intends to do. The engine writes it once, at creation.
type RemediationSpec struct {
	// StrategyName is the RemediationStrategy that matched.
	//
	// +kubebuilder:validation:Required
	StrategyName string `json:"strategyName"`

	// Target is the object being remediated, as "kind/namespace/name" —
	// for example "deployment/payments/api". It is the key the cooldown
	// guard is scoped by.
	//
	// +optional
	Target string `json:"target,omitempty"`

	// Alert is the event that triggered this execution.
	//
	// +kubebuilder:validation:Required
	Alert AlertRef `json:"alert"`

	// Steps is the plan: the steps resolved from the strategy at creation
	// time. Keeping a copy means the audit trail still explains the run
	// after the strategy is edited or deleted.
	//
	// +optional
	Steps []Step `json:"steps,omitempty"`

	// Retries is the retry budget copied from the strategy at creation
	// time, for the same reason Steps is: an execution already under way
	// must stay explainable, and must not change behaviour because someone
	// edited or deleted the strategy while it was running.
	//
	// +kubebuilder:validation:Minimum=0
	// +optional
	Retries int32 `json:"retries,omitempty"`

	// DryRun records whether the operator was in dry-run mode. It is kept
	// on the record so a Simulated result is self-explanatory.
	//
	// +optional
	DryRun bool `json:"dryRun,omitempty"`
}

// AlertRef identifies the alert that triggered a remediation.
type AlertRef struct {
	// Fingerprint identifies the alert series.
	//
	// +kubebuilder:validation:Required
	Fingerprint string `json:"fingerprint"`

	// Name is the "alertname" label.
	//
	// +optional
	Name string `json:"name,omitempty"`

	// Labels are the alert's labels, kept verbatim so the record explains
	// itself without the original alert.
	//
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// StartsAt is when the alert began firing.
	//
	// +optional
	StartsAt *metav1.Time `json:"startsAt,omitempty"`
}

// RemediationStatus is the execution's progress and outcome.
type RemediationStatus struct {
	// State is the current lifecycle state.
	//
	// +optional
	State RemediationState `json:"state,omitempty"`

	// Attempt is the 1-based attempt number currently running or last run.
	//
	// +optional
	Attempt int32 `json:"attempt,omitempty"`

	// Steps records the outcome of each step of the current attempt, in
	// order.
	//
	// +optional
	Steps []StepStatus `json:"steps,omitempty"`

	// Reason is a short, machine-readable explanation of a terminal state,
	// such as "Interrupted" or "StepFailed". Empty on success.
	//
	// +optional
	Reason string `json:"reason,omitempty"`

	// Message is the human-readable detail behind Reason.
	//
	// +optional
	Message string `json:"message,omitempty"`

	// StartedAt is when the first step began.
	//
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// CompletedAt is when the execution reached a terminal state.
	//
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// Conditions follow the standard Kubernetes convention.
	//
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// StepPhase is the outcome of a single step.
//
// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed;Skipped;Simulated
type StepPhase string

const (
	// StepPhasePending means the step has not started.
	StepPhasePending StepPhase = "Pending"
	// StepPhaseRunning means the step is executing.
	StepPhaseRunning StepPhase = "Running"
	// StepPhaseSucceeded means the step completed.
	StepPhaseSucceeded StepPhase = "Succeeded"
	// StepPhaseFailed means the step returned an error.
	StepPhaseFailed StepPhase = "Failed"
	// StepPhaseSkipped means an earlier step failed, so this one never ran.
	StepPhaseSkipped StepPhase = "Skipped"
	// StepPhaseSimulated means dry-run produced this step's plan without
	// executing it.
	StepPhaseSimulated StepPhase = "Simulated"
)

// StepStatus records what happened to one step.
type StepStatus struct {
	// Index is the step's position in the plan, starting at 0.
	Index int32 `json:"index"`

	// Action is the verb that ran.
	Action string `json:"action"`

	// Target is the object this step acted on, as "kind/namespace/name".
	// It is recorded per step because a plan may touch several objects, and
	// the target on the spec names only the one the guards are scoped by.
	//
	// +optional
	Target string `json:"target,omitempty"`

	// Phase is the step's outcome.
	Phase StepPhase `json:"phase"`

	// Plan is what the step did, or in dry-run would have done, in one
	// human-readable line — for example "patch deployment payments/api
	// restartedAt annotation".
	//
	// +optional
	Plan string `json:"plan,omitempty"`

	// Kubectl is the equivalent command a human would have typed. It is
	// recorded so the change is reviewable by someone who has never read
	// remedik's source; nothing executes or parses it.
	//
	// +optional
	Kubectl string `json:"kubectl,omitempty"`

	// Outputs are whatever the action specifically knows — replicas before
	// and after, an exit code, the revision rolled back to. Structured, so
	// a machine can read them and a person is not made to parse prose.
	//
	// +optional
	Outputs map[string]string `json:"outputs,omitempty"`

	// Verified is what the action's post-condition check found, when it has
	// one — "3/3 replicas updated, available and ready". Empty means the
	// action does not check its own work, or the operator was in dry-run,
	// where nothing was executed to verify.
	//
	// +optional
	Verified string `json:"verified,omitempty"`

	// Message carries the error detail when Phase is Failed.
	//
	// +optional
	Message string `json:"message,omitempty"`

	// StartedAt is when the step began.
	//
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// CompletedAt is when the step finished.
	//
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

// IsTerminal reports whether the remediation has reached a final state.
func (r *Remediation) IsTerminal() bool { return r.Status.State.IsTerminal() }

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=rem
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Strategy",type=string,JSONPath=`.spec.strategyName`
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.target`
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="Attempt",type=integer,JSONPath=`.status.attempt`,priority=1
// +kubebuilder:printcolumn:name="Alert",type=string,JSONPath=`.spec.alert.name`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Remediation is the record of one remediation execution: what triggered
// it, what ran, and how it ended. It is the audit trail — including for
// dry-run, where the record exists and nothing was mutated.
type Remediation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RemediationSpec   `json:"spec,omitempty"`
	Status RemediationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RemediationList contains a list of Remediation.
type RemediationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Remediation `json:"items"`
}
