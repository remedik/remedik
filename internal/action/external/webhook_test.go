package external

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/internal/action"
)

const operatorNamespace = "remedik"

// secretClient serves the one Secret the webhook may read.
type secretClient struct {
	client.Client

	secret *corev1.Secret
	// lastNamespace records where a read was attempted, so a test can prove
	// the action never reaches outside remedik's own namespace.
	lastNamespace string
}

func (c *secretClient) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	c.lastNamespace = key.Namespace

	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, key.Name)
	}
	if c.secret == nil || c.secret.Name != key.Name {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, key.Name)
	}
	*secret = *c.secret
	return nil
}

func tokenSecret(name, key, value string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: operatorNamespace, Name: name},
		Data:       map[string][]byte{key: []byte(value)},
	}
}

func webhookRequest(params action.Params) action.Request {
	return action.Request{
		Params:      params,
		Labels:      map[string]string{"alertname": "KubePodCrashLooping", "namespace": "payments"},
		Remediation: "pod-crashloop-x7k2q",
		Strategy:    "pod-crashloop",
		Namespace:   operatorNamespace,
	}
}

func TestWebhookCall_PostsTheIncident(t *testing.T) {
	var got payload
	var gotAuth, gotContentType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"queued":true}`))
	}))
	defer server.Close()

	c := &secretClient{secret: tokenSecret("pipeline-token", "token", "s3cr3t")}
	a := NewWebhookCall(c, operatorNamespace)

	result, err := a.Execute(context.Background(), webhookRequest(action.Params{
		URLParam:    server.URL,
		SecretParam: "pipeline-token",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}

	if got.Strategy != "pod-crashloop" || got.Remediation != "pod-crashloop-x7k2q" {
		t.Errorf("payload = %+v, want it to name the strategy and the record", got)
	}
	if got.Labels["alertname"] != "KubePodCrashLooping" {
		t.Errorf("payload labels = %v, want the alert's labels", got.Labels)
	}
	if gotAuth != "Bearer s3cr3t" {
		t.Errorf("Authorization = %q, want the Secret's token", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	if result.Outputs["status"] != "202" {
		t.Errorf("outputs = %v, want the response status", result.Outputs)
	}

	// The credential must never reach the record: a Remediation is readable
	// by anyone with get on the namespace.
	if strings.Contains(result.Kubectl, "s3cr3t") {
		t.Errorf("the kubectl equivalent leaks the credential: %q", result.Kubectl)
	}
	for key, value := range result.Outputs {
		if strings.Contains(value, "s3cr3t") {
			t.Errorf("output %q leaks the credential: %q", key, value)
		}
	}
}

func TestWebhookCall_ANonSuccessResponseFailsTheStep(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("pipeline unavailable"))
	}))
	defer server.Close()

	a := NewWebhookCall(&secretClient{}, operatorNamespace)

	result, err := a.Execute(context.Background(), webhookRequest(action.Params{URLParam: server.URL}))
	if err == nil {
		t.Fatal("Execute() error = nil; a webhook that answered 500 did not do the thing")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, want the status", err)
	}
	// The response is still recorded: what the far end said is the most
	// useful thing on the page when it refused.
	if result.Outputs["response"] != "pipeline unavailable" {
		t.Errorf("outputs = %v, want the response body", result.Outputs)
	}
}

func TestWebhookCall_ReadsSecretsFromItsOwnNamespaceOnly(t *testing.T) {
	c := &secretClient{secret: tokenSecret("pipeline-token", "token", "s3cr3t")}
	a := NewWebhookCall(c, operatorNamespace)

	req := webhookRequest(action.Params{URLParam: "https://example.invalid", SecretParam: "pipeline-token"})
	// The alert claims a different namespace; it must not decide which
	// credential remedik hands out.
	req.Labels["namespace"] = "someone-elses"

	if _, err := a.Plan(context.Background(), req); err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if c.lastNamespace != operatorNamespace {
		t.Errorf("read a Secret from %q, want %q", c.lastNamespace, operatorNamespace)
	}
}

func TestWebhookCall_RefusesWhatItCannotSend(t *testing.T) {
	tests := []struct {
		name    string
		params  action.Params
		secret  *corev1.Secret
		wantErr string
	}{
		{name: "no url", params: action.Params{}, wantErr: "no url"},
		{
			name:    "not http",
			params:  action.Params{URLParam: "file:///etc/passwd"},
			wantErr: "not an http or https URL",
		},
		{
			name:    "a method that is not a submission",
			params:  action.Params{URLParam: "https://example.invalid", MethodParam: "DELETE"},
			wantErr: "not one of POST, PUT or PATCH",
		},
		{
			name:    "a missing secret",
			params:  action.Params{URLParam: "https://example.invalid", SecretParam: "absent"},
			wantErr: "does not exist",
		},
		{
			name:    "a secret without the key",
			params:  action.Params{URLParam: "https://example.invalid", SecretParam: "pipeline-token", SecretKeyParam: "nope"},
			secret:  tokenSecret("pipeline-token", "token", "s3cr3t"),
			wantErr: `has no "nope" key`,
		},
		{
			name:    "a timeout beyond the cap",
			params:  action.Params{URLParam: "https://example.invalid", TimeoutParam: "10m"},
			wantErr: "exceeds the maximum",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := NewWebhookCall(&secretClient{secret: tc.secret}, operatorNamespace)

			// Both paths refuse identically, so a dry run cannot promise a
			// call Execute would not make.
			if _, err := a.Plan(context.Background(), webhookRequest(tc.params)); err == nil ||
				!strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Plan error = %v, want it to mention %q", err, tc.wantErr)
			}
			if _, err := a.Execute(context.Background(), webhookRequest(tc.params)); err == nil ||
				!strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Execute error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestWebhookCall_ActsOnNothingInTheCluster(t *testing.T) {
	a := NewWebhookCall(&secretClient{}, operatorNamespace)

	target, err := a.Resolve(map[string]string{"namespace": "payments"}, nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	// No object means the cooldown guard is scoped to the strategy alone,
	// which is the honest reading: there is nothing here to be cooling down.
	if !target.IsZero() {
		t.Errorf("target = %q, want none", target)
	}
}
