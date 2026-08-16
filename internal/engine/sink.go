package engine

import (
	"context"
	"fmt"
	"log/slog"
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
}

// Consume implements gateway.Sink.
func (s *Sink) Consume(alerts []alert.Alert) {
	// The gateway calls this from its HTTP handler, which has its own
	// request context; a remediation must not be abandoned because the
	// sender hung up, so this work gets a fresh bounded context.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, a := range alerts {
		if err := s.consumeOne(ctx, a); err != nil {
			s.Logger.Error("could not process alert",
				"alert", a.String(), "err", err)
		}
	}
}

func (s *Sink) consumeOne(ctx context.Context, a alert.Alert) error {
	log := s.Logger.With("alert", a.String())

	// Resolved alerts close an incident; they are not a request to act.
	// Auto-close of in-flight remediations is deliberately out of scope
	// for this version.
	if !a.IsFiring() {
		log.Debug("ignoring resolved alert")
		return nil
	}

	rules, byName, err := s.rules(ctx)
	if err != nil {
		return fmt.Errorf("list strategies: %w", err)
	}

	rule, ok := matching.Select(a, rules)
	if !ok {
		s.metrics().Unmatched()
		log.Info("no strategy matches this alert")
		return nil
	}

	strategy := byName[rule.Name]
	log = log.With("strategy", rule.Name)

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
	var list v1alpha1.RemediationStrategyList
	if err := s.Client.List(ctx, &list); err != nil {
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
			DryRun:          s.Posture.DryRunFor(target.Namespace),
		},
	}

	if err := s.Client.Create(ctx, rem); err != nil {
		return fmt.Errorf("create remediation: %w", err)
	}
	return nil
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
