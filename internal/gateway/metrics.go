package gateway

// Recorder receives gateway telemetry. It is an interface rather than a
// direct Prometheus dependency so this package stays free of third-party
// imports and trivially testable; the Prometheus adapter is wired where the
// operator is assembled.
//
// Implementations must be safe for concurrent use.
type Recorder interface {
	// AlertsReceived reports n alerts accepted from one delivery.
	AlertsReceived(n int)
	// AlertsTruncated reports alerts Alertmanager dropped from a delivery
	// before sending it, meaning remedik saw partial information.
	AlertsTruncated(n int)
	// IngestError reports a rejected delivery. Reason is a low-cardinality
	// label such as "malformed_payload" or "body_too_large".
	IngestError(reason string)
	// Unauthorized reports a delivery rejected by authentication.
	Unauthorized()
}

// Reasons reported to Recorder.IngestError. They are constants because they
// become metric label values: keeping the set closed keeps cardinality low.
const (
	reasonMalformedPayload = "malformed_payload"
	reasonBodyTooLarge     = "body_too_large"
	// reasonNotLeader is a replica refusing an alert because it does not
	// hold the lease. A steady rate of these is normal on a deployment with
	// more than one replica; a rate with no leader behind it is not.
	reasonNotLeader = "not_leader"
)

// NopRecorder discards all telemetry. It is the default when no Recorder is
// configured, so callers never need a nil check.
type NopRecorder struct{}

// AlertsReceived implements Recorder.
func (NopRecorder) AlertsReceived(int) {}

// AlertsTruncated implements Recorder.
func (NopRecorder) AlertsTruncated(int) {}

// IngestError implements Recorder.
func (NopRecorder) IngestError(string) {}

// Unauthorized implements Recorder.
func (NopRecorder) Unauthorized() {}
