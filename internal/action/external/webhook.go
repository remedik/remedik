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
	// FormatParam names the shape of the request body.
	FormatParam = "format"
	// AlertNameParam overrides the alertname of an Alertmanager alert.
	AlertNameParam = "alertname"
	// SeverityParam overrides its severity.
	SeverityParam = "severity"
)

// Body formats.
//
// A closed set, deliberately, and never a template. A strategy is read during
// an incident by somebody who did not write it, and a Go template inside one
// is a second language to debug at the worst possible moment. It also means
// this action can state what it sends, which is the question a reviewer asks
// before granting anything webhook access.
const (
	// FormatRemedik is the default: one object describing the remediation.
	// Every service webhook.call was written for takes an arbitrary body.
	FormatRemedik = "remedik"
	// FormatAlertmanager is Alertmanager's POST /api/v2/alerts: an array of
	// alerts. It is the one endpoint that refuses anything else, and the one
	// worth reaching most — the routing tree, the silences, the inhibition
	// rules and the on-call schedule all already live there.
	FormatAlertmanager = "alertmanager"
)

// DefaultEscalationAlertName is the alert raised by FormatAlertmanager.
//
// Not the name of the alert that triggered the remediation: that alert is
// still firing, and reusing its name would have Alertmanager treat the two as
// one. "The remediation failed" is also a different fact from "the pod is
// crash looping", and a receiver should be able to route on it.
const DefaultEscalationAlertName = "RemediationFailed"

// DefaultEscalationSeverity is critical because remediation was attempted and
// did not work, which is worse than the symptom that triggered it.
const DefaultEscalationSeverity = "critical"

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
	// Now supplies the timestamp an Alertmanager body needs. Tests inject a
	// fixed clock so that Plan and Execute can be compared byte for byte.
	Now func() time.Time
}

func (a *WebhookCall) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
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
	format         string
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

	format := req.Params.Get(FormatParam, FormatRemedik)
	var shaped any
	switch format {
	case FormatRemedik:
		shaped = payloadOf(req)
	case FormatAlertmanager:
		shaped = alertmanagerBodyOf(req, a.now())
	default:
		// Refused here, which Plan also runs, so a dry run reports it rather
		// than a live incident being the first time anybody finds out.
		return call{}, fmt.Errorf("parameter %q: %q is not a known format; use %q or %q",
			FormatParam, format, FormatRemedik, FormatAlertmanager)
	}
	c.format = format

	body, err := json.Marshal(shaped)
	if err != nil {
		return call{}, fmt.Errorf("encode the request body: %w", err)
	}
	c.body = body

	return c, nil
}

// alertmanagerBodyOf builds a POST /api/v2/alerts body: an array of one.
//
// The labels are the design, not decoration. An alert Alertmanager cannot
// route reaches nobody, so the raised alert inherits every label of the alert
// that triggered the remediation — which means the routing tree that
// delivered the symptom to a team delivers the failure to the same team, with
// no second copy of that configuration to keep in step.
func alertmanagerBodyOf(req action.Request, now time.Time) []alertmanagerAlert {
	labels := make(map[string]string, len(req.Labels)+3)
	for k, v := range req.Labels {
		labels[k] = v
	}

	// alertname last, and never the triggering alert's: that alert is still
	// firing, and reusing its name would have Alertmanager treat the two as
	// one and drop this one as a duplicate.
	labels["severity"] = req.Params.Get(SeverityParam, DefaultEscalationSeverity)
	labels["alertname"] = req.Params.Get(AlertNameParam, DefaultEscalationAlertName)

	annotations := map[string]string{
		"summary": fmt.Sprintf("remedik could not remediate %s",
			firstNonEmpty(targetOrEmpty(req.Target), req.Strategy)),
		"remediation": req.Remediation,
	}
	if msg := req.Labels["remedik_message"]; msg != "" {
		annotations["description"] = msg
	}
	if req.DryRun {
		annotations["dryRun"] = "this remediation only reported; nothing was changed"
	}

	// startsAt is set and endsAt is not, deliberately: Alertmanager expires
	// an alert it stops hearing about through its own resolve_timeout, and
	// remedik pages once and is never retried. Setting endsAt would resolve
	// the page immediately, which is worse than not sending it.
	return []alertmanagerAlert{{
		Labels:      labels,
		Annotations: annotations,
		StartsAt:    now.UTC().Format(time.RFC3339),
	}}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
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

// alertmanagerAlert is Alertmanager's PostableAlert, which is the only body
// shape it accepts on /api/v2/alerts.
type alertmanagerAlert struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations,omitempty"`
	StartsAt    string            `json:"startsAt,omitempty"`
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
