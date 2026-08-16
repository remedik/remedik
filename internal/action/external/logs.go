package external

import (
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

// PodLogs reads container logs through a Kubernetes clientset.
//
// It exists because the controller-runtime client cannot: logs are a
// subresource it does not model. The tail of what a script printed before it
// stopped is most of what makes a Job worth running from a remediation
// rather than from a terminal, so it is worth the second client.
type PodLogs struct {
	clientset kubernetes.Interface
}

// NewPodLogs builds the reader.
func NewPodLogs(clientset kubernetes.Interface) *PodLogs {
	return &PodLogs{clientset: clientset}
}

// TailLogs returns the last lines of a pod's output.
func (p *PodLogs) TailLogs(ctx context.Context, namespace, pod string, lines int64) (string, error) {
	stream, err := p.clientset.CoreV1().
		Pods(namespace).
		GetLogs(pod, &corev1.PodLogOptions{TailLines: &lines}).
		Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("open the log stream for %s/%s: %w", namespace, pod, err)
	}
	defer func() { _ = stream.Close() }()

	// Bounded twice: the API returns at most `lines`, and this caps a single
	// very long line. The result is written to a Kubernetes resource.
	body, err := io.ReadAll(io.LimitReader(stream, maxLogBytes*2))
	if err != nil {
		return "", fmt.Errorf("read the logs of %s/%s: %w", namespace, pod, err)
	}
	return string(body), nil
}

// Compile-time proof that the reader satisfies what the Job actions need.
var _ logReader = (*PodLogs)(nil)
