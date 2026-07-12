package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
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
	var joined struct {
		Room struct {
			ID         string             `json:"id"`
			MaxPlayers int                `json:"maxPlayers"`
			Map        simulation.MapData `json:"map"`
		} `json:"room"`
		Player struct {
			ID string `json:"id"`
		} `json:"player"`
		SessionToken  string `json:"sessionToken"`
		WebSocketPath string `json:"webSocketPath"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &joined); err != nil {
		t.Fatalf("decode matchmaking response: %v", err)
	}
	assertRandomValue(t, joined.Room.ID, "room_", 16)
	assertRandomValue(t, joined.Player.ID, "player_", 16)
	assertRandomValue(t, joined.SessionToken, "", 32)
	wantWebSocketPath := "/rooms/" + joined.Room.ID + "/players/" + joined.Player.ID + "?token=" + joined.SessionToken
	if joined.WebSocketPath != wantWebSocketPath {
		t.Fatal("expected websocket path to match the issued room, player, and session")
	}
	fixture, err := simulation.LoadDefaultMapFixture()
	if err != nil {
		t.Fatalf("load default map fixture: %v", err)
	}
	if joined.Room.MaxPlayers != fixture.MaxPlayers {
		t.Fatalf("expected default fixture max players %d, got %d", fixture.MaxPlayers, joined.Room.MaxPlayers)
	}
	if joined.Room.Map.Width != fixture.Width || joined.Room.Map.Height != fixture.Height {
		t.Fatalf("expected default fixture map size %dx%d, got %dx%d", fixture.Width, fixture.Height, joined.Room.Map.Width, joined.Room.Map.Height)
	}
}

func assertRandomValue(t *testing.T, value string, prefix string, wantBytes int) {
	t.Helper()

	if !strings.HasPrefix(value, prefix) {
		t.Fatalf("expected %q prefix, got %q", prefix, value)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, prefix))
	if err != nil {
		t.Fatalf("decode random value: %v", err)
	}
	if len(decoded) != wantBytes {
		t.Fatalf("expected %d decoded bytes, got %d", wantBytes, len(decoded))
	}
}
