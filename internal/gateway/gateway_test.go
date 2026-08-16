package gateway

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/remedik/remedik/internal/alert"
)

const testToken = "s3cr3t-token"

// recordingSink captures what the gateway forwards downstream.
type recordingSink struct {
	mu     sync.Mutex
	calls  int
	alerts []alert.Alert
}

func (s *recordingSink) Consume(alerts []alert.Alert) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.alerts = append(s.alerts, alerts...)
}

func (s *recordingSink) snapshot() (calls int, alerts []alert.Alert) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, append([]alert.Alert(nil), s.alerts...)
}

// countingRecorder records telemetry so tests can assert on it.
type countingRecorder struct {
	mu           sync.Mutex
	received     int
	truncated    int
	errors       map[string]int
	unauthorized int
}

func newCountingRecorder() *countingRecorder {
	return &countingRecorder{errors: map[string]int{}}
}

func (r *countingRecorder) AlertsReceived(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.received += n
}

func (r *countingRecorder) AlertsTruncated(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.truncated += n
}

func (r *countingRecorder) IngestError(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors[reason]++
}

func (r *countingRecorder) Unauthorized() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unauthorized++
}

// quietLogger keeps expected warnings and errors out of the test output.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type testHarness struct {
	server   *httptest.Server
	sink     *recordingSink
	recorder *countingRecorder
}

func newHarness(t *testing.T, cfg Config) *testHarness {
	t.Helper()

	sink := &recordingSink{}
	recorder := newCountingRecorder()

	if cfg.Sink == nil {
		cfg.Sink = sink
	}
	if cfg.Metrics == nil {
		cfg.Metrics = recorder
	}
	if cfg.Logger == nil {
		cfg.Logger = quietLogger()
	}

	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}

	srv := httptest.NewServer(h.Mux())
	t.Cleanup(srv.Close)

	return &testHarness{server: srv, sink: sink, recorder: recorder}
}

// post sends a delivery. An empty token omits the Authorization header.
func (h *testHarness) post(t *testing.T, path, token, body string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, h.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

const firingPayload = `{
  "version": "4",
  "alerts": [
    {"status": "firing", "labels": {"alertname": "KubePodCrashLooping", "namespace": "payments", "pod": "api-0"},
     "annotations": {"summary": "crash looping"}, "startsAt": "2026-08-15T09:00:00Z", "fingerprint": "f1"},
    {"status": "firing", "labels": {"alertname": "KubePodCrashLooping", "namespace": "payments", "pod": "api-1"},
     "annotations": {}, "startsAt": "2026-08-15T09:01:00Z", "fingerprint": "f2"},
    {"status": "resolved", "labels": {"alertname": "KubePodCrashLooping", "namespace": "checkout", "pod": "web-3"},
     "annotations": {}, "startsAt": "2026-08-15T08:30:00Z", "fingerprint": "f3"}
  ]
}`

func TestHandler_AcceptsAuthenticatedDelivery(t *testing.T) {
	h := newHarness(t, Config{Token: testToken})

	resp := h.post(t, DefaultPath, testToken, firingPayload)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		Received int `json:"received"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Received != 3 {
		t.Errorf("received = %d, want 3", body.Received)
	}

	calls, alerts := h.sink.snapshot()
	if calls != 1 {
		t.Errorf("sink called %d times, want 1", calls)
	}
	if len(alerts) != 3 {
		t.Fatalf("sink got %d alerts, want 3", len(alerts))
	}
	if alerts[0].Label("pod") != "api-0" || alerts[2].Status != alert.StatusResolved {
		t.Errorf("alerts were not forwarded intact: %+v", alerts)
	}
	if h.recorder.received != 3 {
		t.Errorf("recorder.received = %d, want 3", h.recorder.received)
	}
}

func TestHandler_Authentication(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		wantStatus int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong token", "Bearer wrong-token", http.StatusUnauthorized},
		{"empty bearer", "Bearer ", http.StatusUnauthorized},
		{"missing scheme", testToken, http.StatusUnauthorized},
		{"wrong scheme", "Basic " + testToken, http.StatusUnauthorized},
		{"correct token", "Bearer " + testToken, http.StatusOK},
		{"scheme is case-insensitive", "bearer " + testToken, http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, Config{Token: testToken})

			req, err := http.NewRequest(http.MethodPost,
				h.server.URL+DefaultPath, strings.NewReader(firingPayload))
			if err != nil {
				t.Fatalf("NewRequest() error = %v", err)
			}
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}

			resp, err := h.server.Client().Do(req)
			if err != nil {
				t.Fatalf("Do() error = %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != tc.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}

			calls, _ := h.sink.snapshot()
			if tc.wantStatus == http.StatusUnauthorized {
				// The body must not be processed for a rejected sender.
				if calls != 0 {
					t.Errorf("sink was called %d times for an unauthorized request", calls)
				}
				if h.recorder.unauthorized != 1 {
					t.Errorf("recorder.unauthorized = %d, want 1", h.recorder.unauthorized)
				}
				if got := resp.Header.Get("WWW-Authenticate"); got == "" {
					t.Error("WWW-Authenticate header is missing on 401")
				}
			} else if calls != 1 {
				t.Errorf("sink was called %d times, want 1", calls)
			}
		})
	}
}

func TestHandler_AuthDisabledWhenNoTokenConfigured(t *testing.T) {
	h := newHarness(t, Config{}) // no token

	resp := h.post(t, DefaultPath, "", firingPayload)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if calls, _ := h.sink.snapshot(); calls != 1 {
		t.Errorf("sink called %d times, want 1", calls)
	}
}

func TestHandler_RejectsMalformedPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{"invalid json", `{"version": "4", "alerts": [`},
		{"empty body", ``},
		{"unsupported version", `{"version": "5", "alerts": []}`},
		{"unknown status", `{"version": "4", "alerts": [{"status": "pending", "labels": {"alertname": "X"}}]}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, Config{Token: testToken})

			resp := h.post(t, DefaultPath, testToken, tc.payload)

			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
			if calls, _ := h.sink.snapshot(); calls != 0 {
				t.Errorf("sink called %d times for a malformed payload", calls)
			}
			if h.recorder.errors[reasonMalformedPayload] != 1 {
				t.Errorf("malformed_payload count = %d, want 1",
					h.recorder.errors[reasonMalformedPayload])
			}
		})
	}
}

// An understood delivery that matches nothing is still a 200: Alertmanager
// retries non-2xx responses, and "nothing matched" is a normal outcome.
func TestHandler_ReturnsOKWhenNothingMatches(t *testing.T) {
	h := newHarness(t, Config{
		Token: testToken,
		Sink:  SinkFunc(func([]alert.Alert) { /* engine finds no strategy */ }),
	})

	resp := h.post(t, DefaultPath, testToken, firingPayload)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestHandler_EmptyAlertListIsAccepted(t *testing.T) {
	h := newHarness(t, Config{Token: testToken})

	resp := h.post(t, DefaultPath, testToken, `{"version": "4", "alerts": []}`)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if h.recorder.received != 0 {
		t.Errorf("recorder.received = %d, want 0", h.recorder.received)
	}
}

func TestHandler_RejectsOversizedBody(t *testing.T) {
	h := newHarness(t, Config{Token: testToken, MaxBodyBytes: 128})

	big := `{"version": "4", "alerts": [{"status": "firing", "labels": {"alertname": "` +
		strings.Repeat("x", 512) + `"}}]}`
	resp := h.post(t, DefaultPath, testToken, big)

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusRequestEntityTooLarge)
	}
	if h.recorder.errors[reasonBodyTooLarge] != 1 {
		t.Errorf("body_too_large count = %d, want 1", h.recorder.errors[reasonBodyTooLarge])
	}
	if calls, _ := h.sink.snapshot(); calls != 0 {
		t.Errorf("sink called %d times for an oversized body", calls)
	}
}

func TestHandler_RejectsWrongMethodAndPath(t *testing.T) {
	h := newHarness(t, Config{Token: testToken})

	t.Run("GET is not allowed", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, h.server.URL+DefaultPath, nil)
		if err != nil {
			t.Fatalf("NewRequest() error = %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+testToken)

		resp, err := h.server.Client().Do(req)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
		}
		if got := resp.Header.Get("Allow"); got != http.MethodPost {
			t.Errorf("Allow = %q, want %q", got, http.MethodPost)
		}
	})

	t.Run("unknown path is 404", func(t *testing.T) {
		resp := h.post(t, "/nope", testToken, firingPayload)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
		}
	})
}

func TestHandler_ReportsTruncatedDeliveries(t *testing.T) {
	h := newHarness(t, Config{Token: testToken})

	payload := `{"version": "4", "truncatedAlerts": 12, "alerts": [
		{"status": "firing", "labels": {"alertname": "X"}}]}`
	resp := h.post(t, DefaultPath, testToken, payload)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if h.recorder.truncated != 12 {
		t.Errorf("recorder.truncated = %d, want 12", h.recorder.truncated)
	}
}

func TestHandler_CustomPath(t *testing.T) {
	const custom = "/hooks/am"
	h := newHarness(t, Config{Token: testToken, Path: custom})

	if resp := h.post(t, custom, testToken, firingPayload); resp.StatusCode != http.StatusOK {
		t.Errorf("custom path status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if resp := h.post(t, DefaultPath, testToken, firingPayload); resp.StatusCode != http.StatusNotFound {
		t.Errorf("default path status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestNew_Validation(t *testing.T) {
	t.Run("sink is required", func(t *testing.T) {
		if _, err := New(Config{}); err == nil {
			t.Error("New() error = nil, want an error when Sink is missing")
		}
	})

	t.Run("defaults are applied", func(t *testing.T) {
		h, err := New(Config{
			Sink:   SinkFunc(func([]alert.Alert) {}),
			Logger: quietLogger(),
		})
		if err != nil {
			t.Fatalf("New() error = %v, want nil", err)
		}
		if h.Path() != DefaultPath {
			t.Errorf("Path() = %q, want %q", h.Path(), DefaultPath)
		}
		if h.maxBodyBytes != DefaultMaxBodyBytes {
			t.Errorf("maxBodyBytes = %d, want %d", h.maxBodyBytes, DefaultMaxBodyBytes)
		}
		if h.metrics == nil {
			t.Error("metrics = nil, want NopRecorder")
		}
	})
}

func TestNopRecorder_IsSafeToCall(_ *testing.T) {
	var r Recorder = NopRecorder{}
	r.AlertsReceived(1)
	r.AlertsTruncated(1)
	r.IngestError("x")
	r.Unauthorized()
}
