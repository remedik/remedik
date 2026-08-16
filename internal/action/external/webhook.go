// Package external implements the actions that reach outside remedik: a
// webhook, and Jobs that run somebody else's container.
//
// They exist because four built-in verbs will never cover what people need
// at 3am, and "remedik cannot do X" is a reason not to install it. They are
// also the largest trust surface in the project, so each one is deliberate
// about what it can reach: the webhook takes a URL and a Secret the operator
// already holds, and the Job runners create Jobs only in remedik's own
// namespace, under a ServiceAccount the strategy names rather than remedik's
// own.
package external

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/internal/action"
)

// Parameters of webhook.call.
const (
	// URLParam is the endpoint to call. Required.
	URLParam = "url"
	// MethodParam overrides the HTTP method; POST by default.
	MethodParam = "method"
	// SecretParam names a Secret in remedik's own namespace holding a
	// credential to send.
	SecretParam = "secretRef"
	// SecretKeyParam is the key inside that Secret.
	SecretKeyParam = "secretKey"
	// HeaderParam is the header the credential is sent in.
	HeaderParam = "header"
	// HeaderPrefixParam is what precedes the credential in that header.
	HeaderPrefixParam = "headerPrefix"
	// TimeoutParam bounds the call.
	TimeoutParam = "timeout"
)

// DefaultWebhookTimeout bounds a call that does not set one. An execution
// holds the single reconcile worker, so a webhook that never answers is a
// remediation queue that never drains.
const DefaultWebhookTimeout = 10 * time.Second

// MaxWebhookTimeout caps what a step may ask for, for the same reason.
const MaxWebhookTimeout = 2 * time.Minute

// maxResponseBytes bounds what is read back. The response is recorded on a
// Remediation resource, and etcd is not a log store.
const maxResponseBytes = 4 << 10

// WebhookCall posts the incident to an endpoint outside remedik.
//
// It is the cheapest action in the catalogue to build and the most useful
// per line: it reaches every pipeline, runbook service and automation
// remedik will never implement, and it moves the blast radius outside the
// cluster, where somebody else's controls decide what happens next.
type WebhookCall struct {
	client     client.Client
	http       *http.Client
	operatorNS string
}

// NewWebhookCall builds the action. Secrets are read from operatorNamespace
// and nowhere else: reading them from the namespace an alert names would
// let a label decide which credential remedik hands out.
func NewWebhookCall(c client.Client, operatorNamespace string) *WebhookCall {
	return &WebhookCall{
		client:     c,
		http:       &http.Client{},
		operatorNS: operatorNamespace,
	}
}

// Name implements action.Action.
func (a *WebhookCall) Name() string { return "webhook.call" }

// Resolve returns no target: this action acts on nothing in the cluster.
//
// A zero target means the cooldown guard is scoped to the strategy alone,
// which is the correct reading — there is no object to be cooling down.
func (a *WebhookCall) Resolve(map[string]string, action.Params) (action.Target, error) {
	return action.Target{}, nil
}

// Plan describes the call, validating everything Execute needs so that a dry
// run surfaces a missing Secret or a malformed URL rather than promising a
// call that could never be made.
func (a *WebhookCall) Plan(ctx context.Context, req action.Request) (action.Result, error) {
	call, err := a.prepare(ctx, req)
	if err != nil {
		return action.Result{}, err
	}

	result := action.Result{
		Summary: fmt.Sprintf("%s %s with the alert, the strategy and the plan",
			call.method, call.endpoint),
		Kubectl: call.curl(),
	}
	result.Output("url", call.endpoint)
	result.Output("method", call.method)
	if call.credentialFrom != "" {
		result.Output("credentialFrom", call.credentialFrom)
	}
	return result, nil
}

// Execute makes the call.
func (a *WebhookCall) Execute(ctx context.Context, req action.Request) (action.Result, error) {
	call, err := a.prepare(ctx, req)
	if err != nil {
		return action.Result{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, call.timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, call.method, call.endpoint, bytes.NewReader(call.body))
	if err != nil {
		return action.Result{}, fmt.Errorf("build the request to %s: %w", call.endpoint, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "remedik")
	if call.header != "" && call.credential != "" {
		request.Header.Set(call.header, call.headerPrefix+call.credential)
	}

	response, err := a.http.Do(request)
	if err != nil {
		return action.Result{}, fmt.Errorf("call %s: %w", call.endpoint, err)
	}
	defer func() { _ = response.Body.Close() }()

	body := readCapped(response)

	result := action.Result{
		Summary: fmt.Sprintf("%s %s answered %s", call.method, call.endpoint, response.Status),
		Kubectl: call.curl(),
	}
	result.Output("url", call.endpoint)
	result.Output("status", strconv.Itoa(response.StatusCode))
	if body != "" {
		result.Output("response", body)
	}

	// Anything but 2xx is a failure. A webhook that answered 500 did not do
	// the thing, and recording that as a success would put a Succeeded
	// record next to a pipeline that never ran.
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return result, fmt.Errorf("%s %s answered %s: %s",
			call.method, call.endpoint, response.Status, body)
	}
	return result, nil
}

// call is a validated request, built identically by Plan and Execute so that
// a dry run cannot promise something Execute would refuse.
type call struct {
	endpoint       string
	method         string
	timeout        time.Duration
	header         string
	headerPrefix   string
	credential     string
	credentialFrom string
	body           []byte
}

// curl renders the equivalent command. The credential is never included:
// this string is written to a Remediation resource that anyone with read
// access to the namespace can see.
func (c call) curl() string {
	auth := ""
	if c.credentialFrom != "" {
		auth = fmt.Sprintf(" -H '%s: %s<%s>'", c.header, c.headerPrefix, c.credentialFrom)
	}
	return fmt.Sprintf("curl -X %s %s%s -H 'Content-Type: application/json' -d @-",
		c.method, c.endpoint, auth)
}

func (a *WebhookCall) prepare(ctx context.Context, req action.Request) (call, error) {
	endpoint := req.Params.Get(URLParam, "")
	if endpoint == "" {
		return call{}, fmt.Errorf("no %s: the step must name the endpoint to call", URLParam)
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return call{}, fmt.Errorf("parameter %q: %w", URLParam, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return call{}, fmt.Errorf("parameter %q: %q is not an http or https URL", URLParam, endpoint)
	}

	method := strings.ToUpper(req.Params.Get(MethodParam, http.MethodPost))
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return call{}, fmt.Errorf("parameter %q: %q is not one of POST, PUT or PATCH",
			MethodParam, method)
	}

	timeout, err := req.Params.Duration(TimeoutParam, DefaultWebhookTimeout)
	if err != nil {
		return call{}, err
	}
	if timeout > MaxWebhookTimeout {
		return call{}, fmt.Errorf("parameter %q: %s exceeds the maximum of %s",
			TimeoutParam, timeout, MaxWebhookTimeout)
	}

	c := call{
		endpoint:     endpoint,
		method:       method,
		timeout:      timeout,
		header:       req.Params.Get(HeaderParam, "Authorization"),
		headerPrefix: req.Params.Get(HeaderPrefixParam, "Bearer "),
	}

	if name := req.Params.Get(SecretParam, ""); name != "" {
		key := req.Params.Get(SecretKeyParam, "token")
		credential, err := a.credential(ctx, name, key)
		if err != nil {
			return call{}, err
		}
		c.credential = credential
		c.credentialFrom = name + "/" + key
	}

	body, err := json.Marshal(payloadOf(req))
	if err != nil {
		return call{}, fmt.Errorf("encode the request body: %w", err)
	}
	c.body = body

	return c, nil
}

// credential reads the Secret, from remedik's own namespace only.
func (a *WebhookCall) credential(ctx context.Context, name, key string) (string, error) {
	var secret corev1.Secret
	objectKey := client.ObjectKey{Namespace: a.operatorNS, Name: name}

	if err := a.client.Get(ctx, objectKey, &secret); err != nil {
		switch {
		case apierrors.IsNotFound(err):
			return "", fmt.Errorf("secret %s/%s does not exist", a.operatorNS, name)
		case apierrors.IsForbidden(err):
			return "", fmt.Errorf("not permitted to read secret %s/%s: %w", a.operatorNS, name, err)
		default:
			return "", fmt.Errorf("read secret %s/%s: %w", a.operatorNS, name, err)
		}
	}

	value, ok := secret.Data[key]
	if !ok || len(value) == 0 {
		return "", fmt.Errorf("secret %s/%s has no %q key", a.operatorNS, name, key)
	}
	return string(value), nil
}

// payload is what remedik sends. It is a stable shape: something on the
// other end parses it, and changing a field name silently would break
// automation nobody here can see.
type payload struct {
	Remediation string            `json:"remediation"`
	Strategy    string            `json:"strategy"`
	Namespace   string            `json:"namespace"`
	Target      string            `json:"target,omitempty"`
	DryRun      bool              `json:"dryRun"`
	Alert       payloadAlert      `json:"alert"`
	Labels      map[string]string `json:"labels"`
}

type payloadAlert struct {
	Name string `json:"name,omitempty"`
}

func payloadOf(req action.Request) payload {
	return payload{
		Remediation: req.Remediation,
		Strategy:    req.Strategy,
		Namespace:   req.Namespace,
		Target:      targetOrEmpty(req.Target),
		DryRun:      req.DryRun,
		Alert:       payloadAlert{Name: req.Labels["alertname"]},
		Labels:      req.Labels,
	}
}

func targetOrEmpty(t action.Target) string {
	if t.IsZero() {
		return ""
	}
	return t.String()
}

// readCapped reads a bounded amount of the response. The result is recorded
// on a Kubernetes resource, and etcd is not a log store.
func readCapped(response *http.Response) string {
	body, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	return strings.TrimSpace(string(body))
}

// Compile-time proof that the action satisfies the contract.
var _ action.Action = (*WebhookCall)(nil)
