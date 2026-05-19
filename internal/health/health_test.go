package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckReturnsOKStatus(t *testing.T) {
	status := Check("server-crawlstars")

	if status.Status != "ok" {
		t.Fatalf("expected status ok, got %q", status.Status)
	}
	if status.Service != "server-crawlstars" {
		t.Fatalf("expected service server-crawlstars, got %q", status.Service)
	}
}

func TestHandlerReturnsHealthJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	Handler("server-crawlstars").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status code 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected application/json content type, got %q", got)
	}

	var body Status
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "ok" || body.Service != "server-crawlstars" {
		t.Fatalf("unexpected body: %+v", body)
	}
}
