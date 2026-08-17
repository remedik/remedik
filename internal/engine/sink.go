package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/api/v1alpha1"
	"github.com/remedik/remedik/internal/action"
	"github.com/remedik/remedik/internal/alert"
	"github.com/remedik/remedik/internal/guards"
	"github.com/remedik/remedik/internal/matching"
)

// Sink turns alerts into Remediation resources.
//
// It is the gateway's downstream: match a strategy, check the guards, and
// either record an execution to run or explain why nothing will happen.
// The Sink itself never executes anything — creating the resource is the
// whole job, and the reconciler takes it from there. That separation is
// what keeps an alert storm cheap: the HTTP handler does one list from
// cache and at most one create.
type Sink struct {
	// Client reads strategies and creates Remediation resources.
	Client client.Client
	// Registry resolves a strategy's first action, to work out the target
	// the cooldown guard is scoped by.
	Registry *action.Registry
	// History backs the time-based guards.
	History *guards.MemoryHistory
	// Workloads backs the blastRadius guard, which is the only one that has
	// to look at the cluster. Optional: without it, a strategy configuring
	// blastRadius is refused rather than allowed, because a guard that
	// cannot evaluate must not permit.
	Workloads guards.WorkloadReader
	// Namespace is where Remediation resources are created.
	Namespace string
	// Posture decides whether a remediation acts or only reports, from the
	// namespace it targets. It is resolved here, once, and recorded on the
	// resource — for the same reason the steps and the retry budget are: an
	// execution keeps the behaviour it started with, and the record explains
	// itself without the reader having to know what the chart said at the
	// time.
	Posture Posture
	// Metrics receives telemetry; defaults to NopRecorder.
	Metrics Recorder
	// Events publishes Kubernetes events on the strategy, so that
	// `kubectl describe remediationstrategy` answers "why did nothing
	// happen?" without anyone having to find the operator's logs.
	// Optional: nil disables event publishing.
	Events events.EventRecorder
	// Logger is required.
	Logger *slog.Logger
	// Now supplies timestamps; tests inject a fixed clock.
	Now func() time.Time

	// Pause forces dry-run everywhere while it is set, without a restart.
	//
	// It does not silence remedik. The one time an operator most wants to know
	// what remediation would have done is the moment they stopped it, so a
	// paused remedik keeps recording — Simulated, with the plan, and with the
	// pause named on the record. Optional; nil is never paused.
	Pause *Pause
}

// Consume implements gateway.Sink.
func (s *Sink) Consume(alerts []alert.Alert) {
	// The gateway calls this from its HTTP handler, which has its own
	// request context; a remediation must not be abandoned because the
	// sender hung up, so this work gets a fresh bounded context.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// The strategies are read once for the whole delivery, not once per alert.
	//
	// Alertmanager groups: a single POST carries what accumulated during
	// group_wait, which during the storm remedik exists to absorb is hundreds
	// of alerts. Reading inside the loop listed every strategy hundreds of
	// times — measured at 17MB and 41,000 allocations for one delivery of two
	// hundred, against 88kB for one alert.
	//
	// It is also more correct. A delivery is one decision made against one
	// view of the strategies; reading per alert meant the first and the two
	// hundredth could see different sets, so the same batch could be handled
	// under two different configurations with nothing recording that it had.
	rules, byName, err := s.rules(ctx)
	if err != nil {
		s.Logger.Error("could not list strategies; the delivery is dropped", "err", err)
		return
	}

	for _, a := range alerts {
		if err := s.consumeOne(ctx, a, rules, byName); err != nil {
			s.Logger.Error("could not process alert",
				"alert", a.String(), "err", err)
		}
	}
}

func (s *Sink) consumeOne(
	ctx context.Context,
	a alert.Alert,
	rules []matching.Rule,
	byName map[string]*v1alpha1.RemediationStrategy,
) error {
	log := s.Logger.With("alert", a.String())

	// Resolved alerts close an incident; they are not a request to act.
	// Auto-close of in-flight remediations is deliberately out of scope
	// for this version.
	if !a.IsFiring() {
		log.Debug("ignoring resolved alert")
		return nil
	}

	rule, ok := matching.Select(a, rules)
	if !ok {
		s.metrics().Unmatched()
		log.Info("no strategy matches this alert", "labels", labelsOf(a))
		explainNoMatch(log, a, rules)
		return nil
	}

	strategy := byName[rule.Name]
	log = log.With("strategy", rule.Name)

	// A manual strategy never starts from an alert. Refused before the target
	// is resolved and before any guard is consulted, because the answer does
	// not depend on either.
	if strategy.Spec.Execution.Mode == v1alpha1.ExecutionModeManual {
		s.metrics().GuardRejected(rule.Name, reasonManual)
		log.Info("strategy is manual; alerts never start it")
		s.recordManualRefusal(strategy, a)
		return nil
	}

	// A target that cannot be resolved is a misconfiguration for this
	// alert. The execution is still created, with an empty target: the
	// reconciler will fail it with the reason recorded on the step, and a
	// record an operator can read beats silence, which is the one outcome
	// nobody can debug. An empty target simply never matches a cooldown.
	target, err := s.resolveTarget(a, strategy)
	if err != nil {
		log.Warn("cannot resolve the target; the execution will be recorded as failed", "err", err)
	}

	decision := guards.Evaluate(guardConfig(strategy), s.History, rule.Name, targetString(target), s.now())
	if decision.Allowed {
		// blastRadius last: it is the only guard that reads the cluster, so
		// the cheap answers are given a chance to refuse first.
		decision = guards.EvaluateBlastRadius(
			ctx, blastRadiusConfig(strategy), s.Workloads, targetString(target))
	}
	if !decision.Allowed {
		s.metrics().GuardRejected(rule.Name, decision.Guard)
		log.Info("guard rejected the execution",
			"guard", decision.Guard, "reason", decision.Reason,
			"retry_after", decision.RetryAfter)
		s.recordRejection(strategy, a, decision)

		// Every other guard refuses into an event and a metric. That is right
		// for "not yet" and wrong for "I have stopped helping", which is the
		// state with the least visibility and the most consequence — so this
		// one leaves a record and pages.
		if decision.Guard == guards.GuardGiveUp {
			return s.recordGiveUp(ctx, a, strategy, target, decision, log)
		}
		return nil
	}

	if err := s.create(ctx, a, strategy, target); err != nil {
		return err
	}

	s.History.RecordStart(rule.Name, s.now())
	s.metrics().RemediationStarted(rule.Name)
	log.Info("remediation created", "target", target.String())
	return nil
}

// rules lists the enabled strategies as matcher rules, keeping the source
// resources indexed by name.
func (s *Sink) rules(ctx context.Context) ([]matching.Rule, map[string]*v1alpha1.RemediationStrategy, error) {
	// Read-only: the rules are built from the strategies and the resources are
	// only ever read, never written, so the manager's cache does not need to
	// copy them. It keeps a pointer into the cache, which is why nothing below
	// may modify a strategy.
	var list v1alpha1.RemediationStrategyList
	if err := s.Client.List(ctx, &list, client.UnsafeDisableDeepCopy); err != nil {
		return nil, nil, err
	}

	rules := make([]matching.Rule, 0, len(list.Items))
	byName := make(map[string]*v1alpha1.RemediationStrategy, len(list.Items))

	for i := range list.Items {
		strategy := &list.Items[i]
		rules = append(rules, matching.Rule{
			Name:    strategy.Name,
			Enabled: strategy.IsEnabled(),
			Match:   strategy.Spec.Trigger.Match,
		})
		byName[strategy.Name] = strategy
	}
	return rules, byName, nil
}

// resolveTarget derives the object the cooldown guard is scoped by, from
// the strategy's first step. A strategy whose steps span several objects is
// still guarded by its first one — the one that names the incident.
func (s *Sink) resolveTarget(a alert.Alert, strategy *v1alpha1.RemediationStrategy) (action.Target, error) {
	if len(strategy.Spec.Steps) == 0 {
		return action.Target{}, fmt.Errorf("strategy %q has no steps", strategy.Name)
	}

	first := strategy.Spec.Steps[0]
	act, err := s.Registry.Get(first.Action)
	if err != nil {
		return action.Target{}, err
	}
	return act.Resolve(a.Labels, action.Params(first.With))
}

// create writes the Remediation resource.
//
// No status is written here. The reconciler owns the lifecycle, and an
// empty state is exactly what it reads as "not started yet" — so there is
// no second write to race with the controller's first one.
func (s *Sink) create(
	ctx context.Context,
	a alert.Alert,
	strategy *v1alpha1.RemediationStrategy,
	target action.Target,
) error {
	startsAt := metav1.NewTime(a.StartsAt)

	rem := &v1alpha1.Remediation{
		ObjectMeta: metav1.ObjectMeta{
			// GenerateName keeps repeated firings from colliding; the
			// guards, not the name, are what stop repetition.
			GenerateName: strategy.Name + "-",
			Namespace:    s.Namespace,
			Labels: map[string]string{
				v1alpha1.LabelStrategy:    strategy.Name,
				v1alpha1.LabelFingerprint: a.Fingerprint,
			},
		},
		Spec: v1alpha1.RemediationSpec{
			StrategyName: strategy.Name,
			Target:       targetString(target),
			Alert: v1alpha1.AlertRef{
				Fingerprint: a.Fingerprint,
				Name:        a.Name(),
				Labels:      a.Labels,
				StartsAt:    &startsAt,
			},
			// The plan and the retry budget are copied so the record
			// still explains the run after the strategy is edited or
			// deleted, and so an in-flight execution keeps the behaviour
			// it started with.
			Steps:           strategy.Spec.Steps,
			Retries:         strategy.Spec.OnFailure.Retries,
			EscalationSteps: strategy.Spec.OnFailure.Steps,
			EscalationMode:  escalationMode(strategy),
			Mode:            executionMode(strategy),
			DryRun:          s.Posture.DryRunFor(target.Namespace) || s.Pause.Paused(),
		},
	}

	if rem.Spec.Mode == v1alpha1.ExecutionModeApproval {
		rem.Spec.ApprovalDeadline = approvalDeadline(strategy, s.now())
	}

	// Why it only simulated, on the record rather than only in a log line: a
	// run of unexplained simulations a week later is indistinguishable from a
	// dry-run trial nobody remembers configuring.
	if s.Pause.Paused() {
		rem.Labels[v1alpha1.LabelPaused] = "true"
		if reason := s.Pause.Reason(); reason != "" {
			if rem.Annotations == nil {
				rem.Annotations = map[string]string{}
			}
			rem.Annotations[v1alpha1.AnnotationPauseReason] = reason
		}
	}

	if err := s.Client.Create(ctx, rem); err != nil {
		return fmt.Errorf("create remediation: %w", err)
	}
	return nil
}

// recordGiveUp creates the record that says remedik has stopped.
//
// It has no steps: nothing is remediated. What it does is run the strategy's
// own onFailure.steps, so the page goes wherever that strategy's pages already
// go, and leave an entry somebody can find later — on the list, on
// /namespaces, and in `kubectl get remediations`.
//
// One per trip. Alertmanager repeats, and a record and a page for every repeat
// would turn "stop paging about this" into a source of pages.
func (s *Sink) recordGiveUp(
	ctx context.Context,
	a alert.Alert,
	strategy *v1alpha1.RemediationStrategy,
	target action.Target,
	decision guards.Decision,
	log *slog.Logger,
) error {
	window := time.Duration(0)
	if g := strategy.Spec.Guards.GiveUpAfter; g != nil {
		window = g.Within.Duration
	}

	already, err := s.gaveUpRecently(ctx, strategy.Name, targetString(target), window)
	if err != nil {
		return fmt.Errorf("look for an existing give-up record: %w", err)
	}
	if already {
		log.Debug("already gave up on this target inside the window; not paging again")
		return nil
	}

	startsAt := metav1.NewTime(a.StartsAt)
	rem := &v1alpha1.Remediation{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: strategy.Name + "-gaveup-",
			Namespace:    s.Namespace,
			Labels: map[string]string{
				v1alpha1.LabelStrategy:    strategy.Name,
				v1alpha1.LabelFingerprint: a.Fingerprint,
				v1alpha1.LabelGaveUp:      "true",
			},
			Annotations: map[string]string{
				v1alpha1.AnnotationGaveUpReason: decision.Reason,
			},
		},
		Spec: v1alpha1.RemediationSpec{
			StrategyName: strategy.Name,
			Target:       targetString(target),
			Alert: v1alpha1.AlertRef{
				Fingerprint: a.Fingerprint,
				Name:        a.Name(),
				Labels:      a.Labels,
				StartsAt:    &startsAt,
			},
			// No Steps and no Retries: nothing is attempted. The escalation is
			// the entire content of this record.
			EscalationSteps: strategy.Spec.OnFailure.Steps,
			EscalationMode:  escalationMode(strategy),
			Mode:            executionMode(strategy),
			// Never a dry run. Giving up is a report, and the escalation is
			// the one thing that runs for real during a trial anyway.
			DryRun: false,
		},
	}

	if err := s.Client.Create(ctx, rem); err != nil {
		return fmt.Errorf("create give-up record: %w", err)
	}

	// Deliberately no RecordStart: remedik started nothing, and counting this
	// as an execution would extend the window that produced it.
	log.Warn("giving up on this target; remediation is not resolving it",
		"target", target.String(), "reason", decision.Reason)
	return nil
}

// gaveUpRecently reports whether a give-up record for this target already
// exists inside the window.
func (s *Sink) gaveUpRecently(
	ctx context.Context, strategy, target string, window time.Duration,
) (bool, error) {
	var list v1alpha1.RemediationList
	if err := s.Client.List(ctx, &list,
		client.InNamespace(s.Namespace),
		client.MatchingLabels{
			v1alpha1.LabelStrategy: strategy,
			v1alpha1.LabelGaveUp:   "true",
		},
		client.UnsafeDisableDeepCopy,
	); err != nil {
		return false, err
	}

	cutoff := s.now().Add(-window)
	for i := range list.Items {
		rem := &list.Items[i]
		if rem.Spec.Target == target && !rem.CreationTimestamp.Time.Before(cutoff) {
			return true, nil
		}
	}
	return false, nil
}

// reasonManual labels the refusal of an alert for a manual strategy. It goes
// through the guard-rejection metric because it is the same question — "why did
// nothing happen" — and an operator should not have to know that this particular
// no came from somewhere else.
const reasonManual = "manual"

// executionMode reads the strategy's mode, defaulting for a resource created
// before the field had more than one value.
func executionMode(strategy *v1alpha1.RemediationStrategy) v1alpha1.ExecutionMode {
	switch strategy.Spec.Execution.Mode {
	case v1alpha1.ExecutionModeApproval:
		return v1alpha1.ExecutionModeApproval
	case v1alpha1.ExecutionModeManual:
		return v1alpha1.ExecutionModeManual
	default:
		return v1alpha1.ExecutionModeAuto
	}
}

// approvalDeadline is when an approval-mode remediation stops waiting.
//
// Absolute and set once, at creation. A duration would restart on every
// reconcile, so a remediation would wait for ever as long as anything requeued
// it — which is the bug this shape exists to make impossible.
func approvalDeadline(strategy *v1alpha1.RemediationStrategy, now time.Time) *metav1.Time {
	timeout := v1alpha1.DefaultApprovalTimeout
	if d := strategy.Spec.Execution.ApprovalTimeout; d != nil && d.Duration > 0 {
		timeout = d.Duration
	}
	deadline := metav1.NewTime(now.Add(timeout))
	return &deadline
}

// EventReasonManualStrategy is the reason on the event published when an alert
// matches a manual strategy.
const EventReasonManualStrategy = "ManualStrategy"

// recordManualRefusal publishes the refusal on the strategy, so that
// `kubectl describe remediationstrategy` answers "why did nothing happen?" for
// this case in the same place it answers it for a guard.
func (s *Sink) recordManualRefusal(strategy *v1alpha1.RemediationStrategy, a alert.Alert) {
	if s.Events == nil {
		return
	}
	s.Events.Eventf(strategy, nil, corev1.EventTypeNormal, EventReasonManualStrategy,
		"manual", "did not start %s: this strategy is manual and never runs from an alert",
		a.String())
}

// EventReasonGuardRejected is the reason on the event published when a
// guard refuses an execution. It is a single stable value rather than one
// per guard, which is what makes `--field-selector reason=GuardRejected`
// useful; the guard itself is named in the message, and metrics carry it
// as a label for counting.
const EventReasonGuardRejected = "GuardRejected"

// recordRejection publishes the guard decision on the strategy, so that
// `kubectl describe remediationstrategy` answers "why did nothing happen?"
// without anyone having to find the operator's logs.
func (s *Sink) recordRejection(
	strategy *v1alpha1.RemediationStrategy, a alert.Alert, decision guards.Decision,
) {
	if s.Events == nil {
		return
	}
	s.Events.Eventf(strategy, nil, corev1.EventTypeNormal, EventReasonGuardRejected,
		decision.Guard, "refused %s: guard %q: %s", a.String(), decision.Guard, decision.Reason)
}

func (s *Sink) metrics() Recorder {
	if s.Metrics == nil {
		return NopRecorder{}
	}
	return s.Metrics
}

func (s *Sink) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func targetString(t action.Target) string {
	if t.IsZero() {
		return ""
	}
	return t.String()
}

// guardConfig converts a strategy's guards into the evaluator's config.
func guardConfig(strategy *v1alpha1.RemediationStrategy) guards.Config {
	cfg := guards.Config{MaxPerHour: int(strategy.Spec.Guards.MaxPerHour)}
	if d := strategy.Spec.Guards.Cooldown; d != nil {
		cfg.Cooldown = d.Duration
	}
	if g := strategy.Spec.Guards.GiveUpAfter; g != nil {
		cfg.GiveUpAfter = guards.GiveUp{Count: int(g.Count), Within: g.Within.Duration}
	}
	return cfg
}

// blastRadiusConfig converts the third guard's settings. An unset block is
// the zero value, which the guard reads as unenforced.
func blastRadiusConfig(strategy *v1alpha1.RemediationStrategy) guards.BlastRadius {
	b := strategy.Spec.Guards.BlastRadius
	if b == nil {
		return guards.BlastRadius{}
	}
	return guards.BlastRadius{
		MinAvailable:          int(b.MinAvailable),
		MaxUnavailablePercent: int(b.MaxUnavailablePercent),
	}
}

// escalationMode reads the strategy's mode, defaulting for a resource created
// before the field existed. The CRD default covers new objects; this covers
// the ones already in etcd, and a zero value here would mean "no mode" rather
// than "every channel", which is the wrong way for this to fail.
func escalationMode(strategy *v1alpha1.RemediationStrategy) v1alpha1.EscalationMode {
	if strategy.Spec.OnFailure.Mode == v1alpha1.EscalationModeFirstSuccess {
		return v1alpha1.EscalationModeFirstSuccess
	}
	return v1alpha1.EscalationModeAll
}

// explainNoMatch says, per strategy, why it was not the one — at debug level,
// because on a busy cluster this is one line per strategy per unmatched alert.
//
// "no strategy matches this alert" is true and unhelpful when nine strategies
// exist and one was meant to handle it. The cause is nearly always a label: a
// strategy matching `namespace` against an alert that carries
// `exported_namespace`, or a value with a trailing space that YAML shows and
// nobody sees. This is the answer to "why did nothing happen", which is the
// question this product gets asked most.
func explainNoMatch(log *slog.Logger, a alert.Alert, rules []matching.Rule) {
	if !log.Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	for _, rule := range rules {
		if why := matching.WhyNot(a, rule); why != "" {
			log.Debug("a strategy did not match", "strategy", rule.Name, "why", why)
		}
	}
}

// labelsOf renders an alert's labels in a stable order, so the line that says
// nothing matched carries what to compare a strategy against. Without it, the
// next step is always "go and find the alert in Alertmanager".
func labelsOf(a alert.Alert) string {
	keys := make([]string, 0, len(a.Labels))
	for key := range a.Labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	for i, key := range keys {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(a.Labels[key])
	}
	return b.String()
}
