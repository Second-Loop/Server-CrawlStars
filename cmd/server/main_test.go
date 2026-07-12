package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/rooms"
	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func TestNewMuxServesDocsRoutes(t *testing.T) {
	handler := mustNewMux(t, rooms.HandlerConfig{})

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
	handler := mustNewMux(t, rooms.HandlerConfig{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/matchmaking/join", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", rec.Code)
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

func TestNewMuxUsesSecureDebugAPIDefault(t *testing.T) {
	handler := mustNewMux(t, rooms.HandlerConfig{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rooms", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}
}

func TestNewMuxWiresEnabledDebugAPI(t *testing.T) {
	handler := mustNewMux(t, rooms.HandlerConfig{
		EnableDebugAPI: true,
		DebugAPIToken:  "server-debug-test-token",
	})

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/rooms", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", unauthorized.Code)
	}

	authorizedRequest := httptest.NewRequest(http.MethodGet, "/rooms", nil)
	authorizedRequest.Header.Set("Authorization", "Bearer server-debug-test-token")
	authorized := httptest.NewRecorder()
	handler.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", authorized.Code)
	}
}

func TestNewMuxRoutesRoomSecurityBeforeOuterServeMux(t *testing.T) {
	tests := []struct {
		name          string
		config        rooms.HandlerConfig
		path          string
		authorization string
		wantStatus    int
		wantCode      string
	}{
		{name: "default duplicate slash", path: "/rooms//", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "enabled duplicate slash without auth", config: rooms.HandlerConfig{EnableDebugAPI: true, DebugAPIToken: "server-debug-test-token"}, path: "/rooms//", wantStatus: http.StatusUnauthorized, wantCode: "unauthorized"},
		{name: "enabled duplicate slash with auth", config: rooms.HandlerConfig{EnableDebugAPI: true, DebugAPIToken: "server-debug-test-token"}, path: "/rooms//", authorization: "Bearer server-debug-test-token", wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
		{name: "default encoded slash", path: "/rooms%2Fmissing", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "enabled encoded slash without auth", config: rooms.HandlerConfig{EnableDebugAPI: true, DebugAPIToken: "server-debug-test-token"}, path: "/rooms%2Fmissing", wantStatus: http.StatusUnauthorized, wantCode: "unauthorized"},
		{name: "enabled encoded slash with auth", config: rooms.HandlerConfig{EnableDebugAPI: true, DebugAPIToken: "server-debug-test-token"}, path: "/rooms%2Fmissing", authorization: "Bearer server-debug-test-token", wantStatus: http.StatusNotFound, wantCode: "room_not_found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := mustNewMux(t, tt.config)
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("expected application/json content type, got %q", got)
			}
			if location := rec.Header().Get("Location"); location != "" {
				t.Fatalf("expected no redirect Location, got %q", location)
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if body.Error.Code != tt.wantCode {
				t.Fatalf("expected error code %q, got %q", tt.wantCode, body.Error.Code)
			}
		})
	}
}

func TestNewMuxRejectsEnabledDebugAPIWithoutToken(t *testing.T) {
	handler, err := newMux(rooms.HandlerConfig{EnableDebugAPI: true})
	if err == nil {
		t.Fatal("expected debug API configuration error")
	}
	if handler != nil {
		t.Fatal("expected no handler for invalid debug API configuration")
	}
}

func TestLoadRoomHandlerConfig(t *testing.T) {
	tests := []struct {
		name        string
		environment map[string]string
		wantEnabled bool
		wantToken   string
		wantError   bool
	}{
		{name: "default", environment: map[string]string{}},
		{name: "explicit false", environment: map[string]string{"ENABLE_DEBUG_API": "false", "DEBUG_API_TOKEN": "unused"}, wantToken: "unused"},
		{name: "enabled", environment: map[string]string{"ENABLE_DEBUG_API": "true", "DEBUG_API_TOKEN": "configured-token"}, wantEnabled: true, wantToken: "configured-token"},
		{name: "enabled numeric", environment: map[string]string{"ENABLE_DEBUG_API": "1", "DEBUG_API_TOKEN": "configured-token"}, wantEnabled: true, wantToken: "configured-token"},
		{name: "invalid flag", environment: map[string]string{"ENABLE_DEBUG_API": "sometimes"}, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := loadRoomHandlerConfig(func(name string) string {
				return tt.environment[name]
			})
			if tt.wantError {
				if err == nil {
					t.Fatal("expected configuration error")
				}
				return
			}
			if err != nil {
				t.Fatalf("load room handler config: %v", err)
			}
			if config.EnableDebugAPI != tt.wantEnabled {
				t.Fatalf("expected debug enabled %t, got %t", tt.wantEnabled, config.EnableDebugAPI)
			}
			if config.DebugAPIToken != tt.wantToken {
				t.Fatal("expected debug token to match environment")
			}
		})
	}
}

func mustNewMux(t *testing.T, config rooms.HandlerConfig) http.Handler {
	t.Helper()

	handler, err := newMux(config)
	if err != nil {
		t.Fatalf("create server mux: %v", err)
	}
	return handler
}

func assertRandomValue(t *testing.T, value string, prefix string, wantBytes int) {
	t.Helper()

	if !strings.HasPrefix(value, prefix) {
		t.Fatalf("expected random value to use the %q prefix", prefix)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, prefix))
	if err != nil {
		t.Fatalf("decode random value: %v", err)
	}
	if len(decoded) != wantBytes {
		t.Fatalf("expected %d decoded bytes, got %d", wantBytes, len(decoded))
	}
}
