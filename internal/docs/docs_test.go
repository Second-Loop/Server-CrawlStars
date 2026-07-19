package docs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesRawSpecs(t *testing.T) {
	handler := Handler()

	openAPI := request(handler, http.MethodGet, "/openapi.yaml")
	assertStatus(t, openAPI, http.StatusOK)
	assertContentType(t, openAPI, "application/yaml")
	assertBodyContains(t, openAPI, "openapi: 3.1.0")
	assertBodyContains(t, openAPI, "/rooms/{roomID}")
	for _, marker := range []string{
		"MatchmakingJoinRequest:",
		"gameMode:",
		"enum: [duel_1v1, solo, team]",
		"const: \"\"",
		"default: duel_1v1",
		"invalid_game_mode",
		"invalid_request",
		"required: [gameMode, room, player, sessionToken, webSocketPath]",
		"required: [id, gameMode, status, players, maxPlayers, map, latestSnapshot]",
		"enum: [red, blue, solo-1, solo-2, solo-3, solo-4, solo-5, solo-6]",
	} {
		assertBodyContains(t, openAPI, marker)
	}

	asyncAPI := request(handler, http.MethodGet, "/asyncapi.yaml")
	assertStatus(t, asyncAPI, http.StatusOK)
	assertContentType(t, asyncAPI, "application/yaml")
	assertBodyContains(t, asyncAPI, "asyncapi: 3.0.0")
	assertBodyContains(t, asyncAPI, "/rooms/{roomID}/players/{playerID}")
}

func TestHandlerServesModeAwareSixPlayerReadyContract(t *testing.T) {
	asyncAPI := request(Handler(), http.MethodGet, "/asyncapi.yaml")
	assertStatus(t, asyncAPI, http.StatusOK)

	for _, want := range []string{
		"version: 0.3.0",
		"duel_1v1은 2명, solo와 team은 6명",
		"6개의 서로 다른 WebSocket connection",
		"각 player가 보낸 ready ACK",
		"중복 ready ACK",
		"Ready timeout, pre-start reconnect grace, reconnect participant replacement, bot fill은 제공하지 않습니다.",
		"Wall과 Water",
		"Ground와 Bush",
		"        Players:\n          oneOf:",
		"            - type: array\n              minItems: 2\n              maxItems: 2\n              items:\n                $ref: \"#/components/schemas/ReadyPlayer\"",
		"            - type: array\n              minItems: 6\n              maxItems: 6\n              items:\n                $ref: \"#/components/schemas/ReadyPlayer\"",
	} {
		assertBodyContains(t, asyncAPI, want)
	}

	teamEnum := "enum: [red, blue, solo-1, solo-2, solo-3, solo-4, solo-5, solo-6]"
	if got := strings.Count(asyncAPI.Body.String(), teamEnum); got != 2 {
		t.Fatalf("expected served AsyncAPI to expose mode team enum twice, got %d", got)
	}
}

func TestHandlerServesHumanReadableDocsUI(t *testing.T) {
	handler := Handler()

	openAPI := request(handler, http.MethodGet, "/openapi")
	assertStatus(t, openAPI, http.StatusOK)
	assertContentType(t, openAPI, "text/html")
	assertBodyContains(t, openAPI, "OpenAPI")
	assertBodyContains(t, openAPI, "/openapi.yaml")

	asyncAPI := request(handler, http.MethodGet, "/asyncapi")
	assertStatus(t, asyncAPI, http.StatusOK)
	assertContentType(t, asyncAPI, "text/html")
	assertBodyContains(t, asyncAPI, "AsyncAPI")
	assertBodyContains(t, asyncAPI, "/asyncapi.yaml")
}

func TestHandlerRejectsUnknownDocsRoute(t *testing.T) {
	rec := request(Handler(), http.MethodGet, "/docs")

	assertStatus(t, rec, http.StatusNotFound)
}

func request(handler http.Handler, method string, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()

	if rec.Code != status {
		t.Fatalf("expected status %d, got %d with body %s", status, rec.Code, rec.Body.String())
	}
}

func assertContentType(t *testing.T, rec *httptest.ResponseRecorder, wantPrefix string) {
	t.Helper()

	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("expected content type prefix %q, got %q", wantPrefix, got)
	}
}

func assertBodyContains(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()

	if !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("expected body to contain %q, got %s", want, rec.Body.String())
	}
}
