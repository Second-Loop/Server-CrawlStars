package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewMuxServesDocsRoutes(t *testing.T) {
	handler := newMux()

	for _, tc := range []struct {
		path        string
		contentType string
		body        string
	}{
		{path: "/openapi", contentType: "text/html", body: "OpenAPI"},
		{path: "/asyncapi", contentType: "text/html", body: "AsyncAPI"},
		{path: "/openapi.yaml", contentType: "application/yaml", body: "openapi: 3.1.0"},
		{path: "/asyncapi.yaml", contentType: "application/yaml", body: "asyncapi: 3.0.0"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d with body %s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, tc.contentType) {
				t.Fatalf("expected content type prefix %q, got %q", tc.contentType, got)
			}
			if !strings.Contains(rec.Body.String(), tc.body) {
				t.Fatalf("expected body to contain %q, got %s", tc.body, rec.Body.String())
			}
		})
	}
}

func TestNewMuxServesMatchmakingJoin(t *testing.T) {
	handler := newMux()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/matchmaking/join", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d with body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("expected json content type, got %q", got)
	}
	if !strings.Contains(rec.Body.String(), `"webSocketPath":"/rooms/room-1/players/player-1"`) {
		t.Fatalf("expected matchmaking connection info, got %s", rec.Body.String())
	}
}
