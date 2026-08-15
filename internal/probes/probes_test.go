package probes

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeEndpoints(t *testing.T) {
	srv := httptest.NewServer(NewMux())
	defer srv.Close()

	tests := []struct {
		path       string
		wantStatus int
	}{
		{"/healthz", http.StatusOK},
		{"/readyz", http.StatusOK},
		{"/nope", http.StatusNotFound},
	}

	for _, tc := range tests {
		resp, err := srv.Client().Get(srv.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.wantStatus {
			t.Errorf("GET %s: got status %d, want %d", tc.path, resp.StatusCode, tc.wantStatus)
		}
	}
}
