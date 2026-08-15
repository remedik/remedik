package engine

// Recorder receives engine telemetry. As in the gateway package, it is an
// interface so the engine carries no Prometheus dependency and stays easy
// to test; the adapter lives in internal/metrics.
//
// Implementations must be safe for concurrent use.
type Recorder interface {
	// Unmatched reports an alert no strategy handled. A high rate means
	// the cookbook does not cover what the cluster actually fires.
	Unmatched()
	// GuardRejected reports a guard refusing an execution. Guard is
	// guards.GuardCooldown or guards.GuardMaxPerHour.
	GuardRejected(strategy, guard string)
	// RemediationStarted reports an execution being created.
	RemediationStarted(strategy string)
	// RemediationFinished reports a terminal outcome: "Succeeded",
	// "Failed" or "Simulated", with how long the execution took.
	RemediationFinished(strategy, outcome string, seconds float64)
}

// NopRecorder discards engine telemetry.
type NopRecorder struct{}

// Unmatched implements Recorder.
func (NopRecorder) Unmatched() {}

// GuardRejected implements Recorder.
func (NopRecorder) GuardRejected(string, string) {}

// RemediationStarted implements Recorder.
func (NopRecorder) RemediationStarted(string) {}

// RemediationFinished implements Recorder.
func (NopRecorder) RemediationFinished(string, string, float64) {}
