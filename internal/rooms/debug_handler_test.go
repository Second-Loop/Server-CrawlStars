package rooms

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nhooyr.io/websocket"
)

const testDebugAPIToken = "debug-api-test-token"

func TestDebugAPIDisabledByDefault(t *testing.T) {
	store := NewStore(5)
	defer store.Close()
	room, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	player, err := store.addPlayer(room.ID)
	if err != nil {
		t.Fatalf("add player: %v", err)
	}
	handler := Handler(store)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "list rooms", method: http.MethodGet, path: "/rooms"},
		{name: "create room", method: http.MethodPost, path: "/rooms"},
		{name: "clear rooms", method: http.MethodDelete, path: "/rooms"},
		{name: "room collection head", method: http.MethodHead, path: "/rooms"},
		{name: "room collection method fallback", method: http.MethodPut, path: "/rooms"},
		{name: "room collection trailing slash", method: http.MethodGet, path: "/rooms/"},
		{name: "room detail", method: http.MethodGet, path: "/rooms/" + room.ID},
		{name: "delete room", method: http.MethodDelete, path: "/rooms/" + room.ID},
		{name: "room detail head", method: http.MethodHead, path: "/rooms/" + room.ID},
		{name: "room detail method fallback", method: http.MethodPut, path: "/rooms/" + room.ID},
		{name: "add player", method: http.MethodPost, path: "/rooms/" + room.ID + "/players"},
		{name: "player collection get", method: http.MethodGet, path: "/rooms/" + room.ID + "/players"},
		{name: "player collection head", method: http.MethodHead, path: "/rooms/" + room.ID + "/players"},
		{name: "player collection method fallback", method: http.MethodPut, path: "/rooms/" + room.ID + "/players"},
		{name: "start room", method: http.MethodPost, path: "/rooms/" + room.ID + "/start"},
		{name: "start get", method: http.MethodGet, path: "/rooms/" + room.ID + "/start"},
		{name: "start head", method: http.MethodHead, path: "/rooms/" + room.ID + "/start"},
		{name: "start method fallback", method: http.MethodPut, path: "/rooms/" + room.ID + "/start"},
		{name: "websocket head", method: http.MethodHead, path: "/rooms/" + room.ID + "/players/" + player.Player.ID},
		{name: "websocket method fallback", method: http.MethodPut, path: "/rooms/" + room.ID + "/players/" + player.Player.ID},
		{name: "missing room detail", method: http.MethodGet, path: "/rooms/missing"},
		{name: "debug token cannot enable route", method: http.MethodGet, path: "/rooms/" + room.ID},
		{name: "player identity remains private REST", method: http.MethodPost, path: "/rooms/" + room.ID + "/players/" + player.Player.ID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.name == "debug token cannot enable route" {
				req.Header.Set("Authorization", "Bearer "+testDebugAPIToken)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("expected status 404, got %d", rec.Code)
			}
			assertError(t, rec, "not_found")
		})
	}
}

func TestDebugAPIConfigurationRequiresToken(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{name: "missing"},
		{name: "empty whitespace", token: "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(5)
			defer store.Close()
			handler, err := HandlerWithConfig(store, HandlerConfig{
				EnableDebugAPI: true,
				DebugAPIToken:  tt.token,
			})
			if err == nil {
				t.Fatal("expected debug API configuration error")
			}
			if handler != nil {
				t.Fatal("expected no handler for invalid debug API configuration")
			}
		})
	}
}

func TestDebugAPIRequiresBearerTokenBeforeRouteDispatch(t *testing.T) {
	store := NewStore(5)
	defer store.Close()
	handler, err := HandlerWithConfig(store, HandlerConfig{
		EnableDebugAPI: true,
		DebugAPIToken:  testDebugAPIToken,
	})
	if err != nil {
		t.Fatalf("create debug handler: %v", err)
	}

	tests := []struct {
		name          string
		authorization []string
	}{
		{name: "missing"},
		{name: "empty bearer", authorization: []string{"Bearer "}},
		{name: "wrong bearer", authorization: []string{"Bearer wrong"}},
		{name: "wrong scheme", authorization: []string{"Basic " + testDebugAPIToken}},
		{name: "multiple", authorization: []string{"Bearer " + testDebugAPIToken, "Bearer " + testDebugAPIToken}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/rooms", nil)
			for _, value := range tt.authorization {
				req.Header.Add("Authorization", value)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected status 401, got %d", rec.Code)
			}
			if strings.Contains(rec.Body.String(), testDebugAPIToken) {
				t.Fatal("expected unauthorized response to omit the configured token")
			}
			assertError(t, rec, "unauthorized")
		})
	}

	request := httptest.NewRequest(http.MethodPut, "/rooms", nil)
	request.Header.Set("Authorization", "Bearer "+testDebugAPIToken)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected authenticated route dispatch to return 405, got %d", recorder.Code)
	}
	assertError(t, recorder, "method_not_allowed")
}

func TestDebugAPICorrectBearerPreservesRESTBehavior(t *testing.T) {
	store := NewStore(5)
	defer store.Close()
	handler, err := HandlerWithConfig(store, HandlerConfig{
		EnableDebugAPI: true,
		DebugAPIToken:  testDebugAPIToken,
	})
	if err != nil {
		t.Fatalf("create debug handler: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/rooms", nil)
	req.Header.Set("Authorization", "Bearer "+testDebugAPIToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", rec.Code)
	}
}

func TestDebugAPIDoesNotGateAuthenticatedWebSocket(t *testing.T) {
	store := NewStore(5)
	defer store.Close()
	room, err := store.createRoom()
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	player, err := store.addPlayer(room.ID)
	if err != nil {
		t.Fatalf("add player: %v", err)
	}
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	conn := dialIssuedPlayer(t, server.URL, player.WebSocketPath)
	defer conn.Close(websocket.StatusNormalClosure, "")
	waitForAttachedClient(t, store, room.ID, player.Player.ID)
}
