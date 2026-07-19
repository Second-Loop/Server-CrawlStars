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
	for _, marker := range []string{
		"duel_1v1",
		"Solo 중간 탈락",
		"이전 Lose는 유지",
		"마지막 생존자",
		"Team 일부 사망",
		"패배 team 3명은 Lose, 상대 team 3명은 Win",
		"양 team이 같은 tick에 전멸하면 6명 모두 Draw",
		"ticker를 terminal decision 즉시 중단",
		"terminal snapshot -> GameEnd -> close",
		"connected-client observer는 close lifecycle에서 반영",
		"transport closeDone보다 먼저일 수 있습니다.",
		"앞서 결과가 확정되어 기억한 session의 closeDone을 모두 기다립니다.",
		"current client map에서 이미 빠진 Solo prior loser도 barrier에 남습니다.",
		"active-room observer를 반영한 다음 player ID를 release하고 room_ended log와 resource close",
		"cleanup success signal은 마지막",
		"Hard TTL과 debug removal은 ending room을 제거하지 않습니다.",
		"Shutdown은 forced-teardown 예외",
		"normal cleanup signal을 닫지 않고",
		"room_ended를 기록하지 않습니다.",
	} {
		assertBodyContains(t, asyncAPI, marker)
	}
	for _, marker := range []string{
		"required: [Type, PlayerId, Result]",
		"const: GameEnd",
		"enum: [Win, Lose, Draw]",
	} {
		assertBodyContains(t, asyncAPI, marker)
	}
}

func TestHandlerServesBotIdentityContracts(t *testing.T) {
	handler := Handler()
	openAPI := request(handler, http.MethodGet, "/openapi.yaml")
	assertStatus(t, openAPI, http.StatusOK)
	for _, marker := range []string{
		"required: [id, team, slot, isBot]",
		"HumanPlayer:",
		"const: false",
	} {
		assertBodyContains(t, openAPI, marker)
	}
	if got := strings.Count(openAPI.Body.String(), `$ref: "#/components/schemas/HumanPlayer"`); got != 2 {
		t.Fatalf("expected two credential-bearing HumanPlayer references, got %d", got)
	}

	asyncAPI := request(handler, http.MethodGet, "/asyncapi.yaml")
	assertStatus(t, asyncAPI, http.StatusOK)
	for _, marker := range []string{
		"version: 0.4.0",
		"required: [Id, Team, Slot, IsBot, SpawnPosition]",
		"required: [Id, Team, Slot, IsBot, Pos, MoveDir, AttackDir, Speed, Radius, HP, PressedAttack, IsDead]",
		"IsBot: false",
		"IsBot: true",
	} {
		assertBodyContains(t, asyncAPI, marker)
	}

	docsUI := request(handler, http.MethodGet, "/asyncapi")
	assertStatus(t, docsUI, http.StatusOK)
	assertBodyContains(t, docsUI, `"IsBot": false`)
	assertBodyContains(t, docsUI, `"IsBot": true`)
}

func TestHandlerServesBotFillContractsInTheirTransportBlocks(t *testing.T) {
	handler := Handler()

	openAPI := request(handler, http.MethodGet, "/openapi.yaml")
	assertStatus(t, openAPI, http.StatusOK)
	joinOperation := extractYAMLBlock(t, openAPI.Body.String(), "  /matchmaking/join:", "\n  /")
	for _, want := range []string{
		"첫 human matchmaking join부터 10초",
		"남은 participant slot을 bot으로 충원",
		"late join은 다른 waiting room을 찾거나 만들며",
		"room_cap_reached",
	} {
		assertStringContains(t, joinOperation, want)
	}

	asyncAPI := request(handler, http.MethodGet, "/asyncapi.yaml")
	assertStatus(t, asyncAPI, http.StatusOK)
	asyncAPIText := asyncAPI.Body.String()
	asyncAPIInfo := extractYAMLBlock(t, asyncAPIText, "info:", "\nx-stability:")
	for _, want := range []string{"room_cap_reached", "bot_fill_failed"} {
		if strings.Contains(asyncAPIInfo, want) {
			t.Fatalf("AsyncAPI info must not describe REST or structured-log marker %q", want)
		}
	}

	readyOperation := extractYAMLBlock(t, asyncAPIText, "  receiveReady:", "\n  sendReadyAck:")
	for _, want := range []string{
		"full participant list",
		"human session만 Ready ACK",
	} {
		assertStringContains(t, readyOperation, want)
	}
	readyAckOperation := extractYAMLBlock(t, asyncAPIText, "  sendReadyAck:", "\n  receiveSnapshot:")
	assertStringContains(t, readyAckOperation, "Bot은 ACK를 보내지 않습니다")

	lifecycleDescription := extractYAMLBlock(t, asyncAPIText, "  roomPlayer:", "\noperations:")
	for _, want := range []string{
		"Unmatched disconnect는 room-owned 10초 fill deadline과 credential을 유지",
		"matched/loading/starting disconnect는 pre-start cancel",
	} {
		assertStringContains(t, lifecycleDescription, want)
	}

	readyMessage := extractYAMLBlock(t, asyncAPIText, "    ReadyEventMessage:\n      type: object", "\n    ReadyAckMessage:")
	for _, want := range []string{
		"        Players:\n          oneOf:",
		"            - type: array\n              minItems: 2\n              maxItems: 2\n              items:\n                $ref: \"#/components/schemas/ReadyPlayer\"",
		"            - type: array\n              minItems: 6\n              maxItems: 6\n              items:\n                $ref: \"#/components/schemas/ReadyPlayer\"",
	} {
		assertStringContains(t, readyMessage, want)
	}

	teamEnum := "enum: [red, blue, solo-1, solo-2, solo-3, solo-4, solo-5, solo-6]"
	if got := strings.Count(asyncAPIText, teamEnum); got != 2 {
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

	assertStringContains(t, rec.Body.String(), want)
}

func assertStringContains(t *testing.T, body string, want string) {
	t.Helper()

	if !strings.Contains(body, want) {
		t.Fatalf("expected body to contain %q, got %s", want, body)
	}
}

func extractYAMLBlock(t *testing.T, body, start, end string) string {
	t.Helper()

	startIndex := strings.Index(body, start)
	if startIndex < 0 {
		t.Fatalf("expected YAML block start %q", start)
	}
	block := body[startIndex:]
	endIndex := strings.Index(block, end)
	if endIndex < 0 {
		t.Fatalf("expected YAML block end %q after %q", end, start)
	}
	return block[:endIndex]
}
