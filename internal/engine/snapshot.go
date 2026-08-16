package engine

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/api/v1alpha1"
)

// Posture is what the cluster looks like right now.
//
// It is declared here rather than taken from internal/metrics because the
// dependency runs the other way throughout this project: the engine says
// what it can report, and the metrics package adapts. Its fields match
// metrics.Snapshot exactly, so the adaptation is a conversion rather than a
// copy loop.
type Posture struct {
	// StrategiesEnabled and StrategiesDisabled count RemediationStrategy
	// resources. Zero enabled is the difference between "nothing has
	// happened" and "nothing can happen".
	StrategiesEnabled  int
	StrategiesDisabled int

	// RecordsByState counts the Remediation resources that exist now.
	RecordsByState map[string]int
}

// Snapshotter answers "what does the cluster look like right now?" for the
// metrics that are questions about the present rather than counts of the
// past.
//
// It reads through the manager's cache, so a scrape costs no API call. That
// matters more than it sounds: Prometheus polls on its own schedule, and an
// endpoint that reached the API server would turn a scrape interval into
// load on the control plane, at exactly the moment a cluster in trouble can
// least afford it.
type Snapshotter struct {
	// Reader is the cached client.
	Reader client.Reader
	// Namespace is where Remediation records live.
	Namespace string
}

// Snapshot reads the current posture.
func (s *Snapshotter) Snapshot(ctx context.Context) (Posture, error) {
	var strategies v1alpha1.RemediationStrategyList
	if err := s.Reader.List(ctx, &strategies); err != nil {
		return Posture{}, fmt.Errorf("list strategies: %w", err)
	}

	var remediations v1alpha1.RemediationList
	if err := s.Reader.List(ctx, &remediations, client.InNamespace(s.Namespace)); err != nil {
		return Posture{}, fmt.Errorf("list remediations: %w", err)
	}

	snapshot := Posture{RecordsByState: make(map[string]int, 5)}

	for i := range strategies.Items {
		if strategies.Items[i].IsEnabled() {
			snapshot.StrategiesEnabled++
		} else {
			snapshot.StrategiesDisabled++
		}
	}

	for i := range remediations.Items {
		state := string(remediations.Items[i].Status.State)
		if state == "" {
			// A record the reconciler has not reached yet is Pending to
			// everyone reading it, so it should not appear under an empty
			// label that nothing can query for.
			state = string(v1alpha1.RemediationStatePending)
		}
		snapshot.RecordsByState[state]++
	}

	return snapshot, nil
}
