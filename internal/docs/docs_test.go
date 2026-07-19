package docs

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
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
		"version: 0.5.0",
		"required: [Id, Team, Slot, IsBot, SpawnPosition]",
		"required: [Id, Team, Slot, IsBot, Pos, MoveDir, AttackDir, Speed, Radius, HP, PressedAttack, IsDead, LastProcessedClientTick]",
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

func TestYAMLTopLevelRequiredFields(t *testing.T) {
	tests := []struct {
		name      string
		schema    string
		want      string
		wantError bool
	}{
		{
			name: "extracts only the schema-level inline list",
			schema: strings.Join([]string{
				"    InputMessage:",
				"      type: object",
				"      required: [MoveDir, ClientTick, AttackDir]",
				"      properties:",
				"        Nested:",
				"          type: object",
				"          required: [NestedField]",
			}, "\n"),
			want: "MoveDir,ClientTick,AttackDir",
		},
		{
			name: "ignores nested required when the schema has none",
			schema: strings.Join([]string{
				"    InputMessage:",
				"      type: object",
				"      properties:",
				"        Nested:",
				"          type: object",
				"          required: [ClientTick]",
			}, "\n"),
			want: "",
		},
		{
			name: "rejects a malformed schema-level list",
			schema: strings.Join([]string{
				"    InputMessage:",
				"      type: object",
				"      required: ClientTick",
			}, "\n"),
			wantError: true,
		},
		{
			name: "rejects duplicate schema-level lists",
			schema: strings.Join([]string{
				"    InputMessage:",
				"      type: object",
				"      required: [MoveDir]",
				"      required: [AttackDir]",
			}, "\n"),
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fields, err := yamlTopLevelRequiredFields(test.schema)
			if test.wantError {
				if err == nil {
					t.Fatalf("expected malformed required list to fail closed, got %v", fields)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse schema-level required fields: %v", err)
			}
			if got := strings.Join(fields, ","); got != test.want {
				t.Fatalf("schema-level required fields=%q want=%q", got, test.want)
			}
		})
	}
}

func TestHandlerServesClientTickACKContract(t *testing.T) {
	handler := Handler()

	asyncAPI := request(handler, http.MethodGet, "/asyncapi.yaml")
	assertStatus(t, asyncAPI, http.StatusOK)
	asyncAPIText := asyncAPI.Body.String()

	info := extractYAMLNamedBlock(t, asyncAPIText, "info:")
	assertStringContains(t, info, "  version: 0.5.0")

	components := extractYAMLNamedBlock(t, asyncAPIText, "components:")
	schemas := extractYAMLNamedBlock(t, components, "  schemas:")
	inputSchema := extractYAMLNamedBlock(t, schemas, "    InputMessage:")
	inputProperties := extractYAMLNamedBlock(t, inputSchema, "      properties:")
	clientTick := extractYAMLNamedBlock(t, inputProperties, "        ClientTick:")
	for _, marker := range []string{"type: integer", "format: int64", "minimum: 0"} {
		assertStringContains(t, clientTick, marker)
	}
	inputRequired, err := yamlTopLevelRequiredFields(inputSchema)
	if err != nil {
		t.Fatalf("parse InputMessage required fields: %v", err)
	}
	if got := exactStringCount(inputRequired, "ClientTick"); got != 0 {
		t.Fatalf("InputMessage must keep ClientTick optional, found it %d times in schema-level required fields %v", got, inputRequired)
	}

	playerSchema := extractYAMLNamedBlock(t, schemas, "    PlayerData:")
	playerRequired, err := yamlTopLevelRequiredFields(playerSchema)
	if err != nil {
		t.Fatalf("parse PlayerData required fields: %v", err)
	}
	if got := exactStringCount(playerRequired, "LastProcessedClientTick"); got != 1 {
		t.Fatalf("PlayerData must require LastProcessedClientTick exactly once, found it %d times in schema-level required fields %v", got, playerRequired)
	}
	playerProperties := extractYAMLNamedBlock(t, playerSchema, "      properties:")
	processedTick := extractYAMLNamedBlock(t, playerProperties, "        LastProcessedClientTick:")
	for _, marker := range []string{"type: integer", "format: int64", "minimum: 0"} {
		assertStringContains(t, processedTick, marker)
	}

	messages := extractYAMLNamedBlock(t, components, "  messages:")
	snapshotMessage := extractYAMLNamedBlock(t, messages, "    SnapshotMessage:")
	starting := extractYAMLNamedBlock(t, snapshotMessage, "        - name: startingSignal")
	started := extractYAMLNamedBlock(t, snapshotMessage, "        - name: startedControl")
	for _, lifecycle := range []struct {
		name   string
		block  string
		status string
	}{
		{name: "starting", block: starting, status: "status: starting"},
		{name: "started", block: started, status: "status: started"},
	} {
		assertStringContains(t, lifecycle.block, lifecycle.status)
		assertStringContains(t, lifecycle.block, "Tick: 0")
		assertStringContains(t, lifecycle.block, "Players: null")
	}

	gameplay := extractYAMLNamedBlock(t, snapshotMessage, "        - name: gameplay")
	gameplayPlayers := extractYAMLSequenceObjects(t, gameplay, "Players")
	if len(gameplayPlayers) == 0 {
		t.Fatal("expected gameplay example player objects")
	}
	hasPositiveHuman := false
	hasBot := false
	for index, player := range gameplayPlayers {
		if got := strings.Count(player, "LastProcessedClientTick:"); got != 1 {
			t.Fatalf("gameplay player %d must include ACK exactly once, got %d in %s", index, got, player)
		}
		ack := yamlIntegerValue(t, player, "LastProcessedClientTick")
		if strings.Contains(player, "IsBot: true") {
			hasBot = true
			if ack != 0 {
				t.Fatalf("bot gameplay ACK=%d want=0 in %s", ack, player)
			}
		} else if strings.Contains(player, "IsBot: false") && ack > 0 {
			hasPositiveHuman = true
		}
	}
	if !hasPositiveHuman || !hasBot {
		t.Fatalf("gameplay example must include a positive human ACK and bot ACK 0: %s", gameplay)
	}

	openAPI := request(handler, http.MethodGet, "/openapi.yaml")
	assertStatus(t, openAPI, http.StatusOK)
	assertStringNotContains(t, openAPI.Body.String(), "\n    PlayerData:")
	assertStringNotContains(t, openAPI.Body.String(), "\n        ClientTick:")

	docsUI := request(handler, http.MethodGet, "/asyncapi")
	assertStatus(t, docsUI, http.StatusOK)
	gameplayArticle := extractYAMLBlock(t, docsUI.Body.String(), "<h3>Gameplay</h3>", "</article>")
	if got := strings.Count(gameplayArticle, `"LastProcessedClientTick":`); got != 2 {
		t.Fatalf("expected served gameplay article to include two ACK fields, got %d", got)
	}
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
	playerSessionToken := extractYAMLBlock(t, openAPI.Body.String(), "    PlayerSessionToken:", "\n    HealthStatus:")
	for _, want := range []string{
		"Unmatched disconnect는 room-owned 10초 fill deadline과 credential을 유지",
		"matched/loading/starting disconnect는 pre-start cancel",
	} {
		assertStringContains(t, playerSessionToken, want)
	}
	assertStringNotContains(t, playerSessionToken, "Pre-start match의 실제 disconnect는 room을 취소")

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
	for _, want := range []string{
		"Bot은 ACK를 보내지 않습니다",
		"중복 ready ACK는 idempotent",
		"Ready quorum을 재증가시키거나 countdown을 재시작하지 않습니다",
	} {
		assertStringContains(t, readyAckOperation, want)
	}

	lifecycleDescription := extractYAMLBlock(t, asyncAPIText, "  roomPlayer:", "\noperations:")
	for _, want := range []string{
		"Unmatched disconnect는 room-owned 10초 fill deadline과 credential을 유지",
		"matched/loading/starting disconnect는 pre-start cancel",
	} {
		assertStringContains(t, lifecycleDescription, want)
	}
	assertStringNotContains(t, lifecycleDescription, "Matchmaking pre-start 연결이 실제로 끊기면 room이 취소")

	playerSessionSecurity := extractYAMLBlock(t, asyncAPIText, "    playerSessionToken:", "\n  messages:")
	for _, want := range []string{
		"Unmatched disconnect는 room-owned 10초 fill deadline과 credential을 유지",
		"matched/loading/starting disconnect는 pre-start cancel",
	} {
		assertStringContains(t, playerSessionSecurity, want)
	}
	assertStringNotContains(t, playerSessionSecurity, "Pre-start match의 실제 disconnect는 room을 취소")

	docsUI := request(handler, http.MethodGet, "/asyncapi")
	assertStatus(t, docsUI, http.StatusOK)
	sessionTokenCard := extractYAMLBlock(t, docsUI.Body.String(), "<h3>Session token</h3>", "</article>")
	for _, want := range []string{
		"Unmatched disconnect는 room-owned 10초 fill deadline과 credential을 유지",
		"matched/loading/starting disconnect는 pre-start cancel",
	} {
		assertStringContains(t, sessionTokenCard, want)
	}
	assertStringNotContains(t, sessionTokenCard, "matchmaking pre-start 연결이 실제로 끊기면 room이 취소")

	readyMessage := extractYAMLBlock(t, asyncAPIText, "    ReadyEventMessage:\n      name: ReadyEventMessage", "\n    ReadyAckMessage:")
	assertStringContains(t, readyMessage, "Fallback spawn은 Wall과 Water를 제외하고 Ground와 Bush를 허용합니다")

	readySchema := extractYAMLBlock(t, asyncAPIText, "    ReadyEventMessage:\n      type: object", "\n    ReadyAckMessage:")
	for _, want := range []string{
		"        Players:\n          oneOf:",
		"            - type: array\n              minItems: 2\n              maxItems: 2\n              items:\n                $ref: \"#/components/schemas/ReadyPlayer\"",
		"            - type: array\n              minItems: 6\n              maxItems: 6\n              items:\n                $ref: \"#/components/schemas/ReadyPlayer\"",
	} {
		assertStringContains(t, readySchema, want)
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

func assertStringNotContains(t *testing.T, body string, unwanted string) {
	t.Helper()

	if strings.Contains(body, unwanted) {
		t.Fatalf("expected body not to contain %q, got %s", unwanted, body)
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

func extractYAMLNamedBlock(t *testing.T, body, marker string) string {
	t.Helper()

	lines := strings.Split(body, "\n")
	start := -1
	for index, line := range lines {
		if line == marker {
			start = index
			break
		}
	}
	if start < 0 {
		t.Fatalf("expected YAML block marker %q", marker)
	}
	indent := len(marker) - len(strings.TrimLeft(marker, " "))
	end := start + 1
	for end < len(lines) {
		line := lines[end]
		if strings.TrimSpace(line) != "" && len(line)-len(strings.TrimLeft(line, " ")) <= indent {
			break
		}
		end++
	}
	return strings.Join(lines[start:end], "\n")
}

func yamlTopLevelRequiredFields(schema string) ([]string, error) {
	lines := strings.Split(schema, "\n")
	rootIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rootIndent = len(line) - len(strings.TrimLeft(line, " "))
		break
	}
	if rootIndent < 0 {
		return nil, fmt.Errorf("empty schema block")
	}

	requiredIndent := rootIndent + 2
	var requiredFields []string
	found := false
	for _, line := range lines[1:] {
		indent := len(line) - len(strings.TrimLeft(line, " "))
		trimmed := strings.TrimSpace(line)
		if indent != requiredIndent || !strings.HasPrefix(trimmed, "required:") {
			continue
		}
		if found {
			return nil, fmt.Errorf("duplicate schema-level required list")
		}
		found = true

		inlineList := strings.TrimSpace(strings.TrimPrefix(trimmed, "required:"))
		if len(inlineList) < 2 || inlineList[0] != '[' || inlineList[len(inlineList)-1] != ']' {
			return nil, fmt.Errorf("schema-level required must be an inline list: %q", trimmed)
		}
		contents := strings.TrimSpace(inlineList[1 : len(inlineList)-1])
		if contents == "" {
			requiredFields = []string{}
			continue
		}
		for _, field := range strings.Split(contents, ",") {
			field = strings.TrimSpace(field)
			if field == "" {
				return nil, fmt.Errorf("schema-level required contains an empty field: %q", trimmed)
			}
			requiredFields = append(requiredFields, field)
		}
	}
	return requiredFields, nil
}

func exactStringCount(values []string, want string) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}

func extractYAMLSequenceObjects(t *testing.T, body, propertyName string) []string {
	t.Helper()

	lines := strings.Split(body, "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) != propertyName+":" {
			continue
		}
		propertyIndent := len(line) - len(strings.TrimLeft(line, " "))
		itemPrefix := strings.Repeat(" ", propertyIndent+2) + "- "
		var objects []string
		var current []string
		for cursor := index + 1; cursor < len(lines); cursor++ {
			candidate := lines[cursor]
			candidateIndent := len(candidate) - len(strings.TrimLeft(candidate, " "))
			if strings.TrimSpace(candidate) != "" && candidateIndent <= propertyIndent {
				break
			}
			if strings.HasPrefix(candidate, itemPrefix) {
				if len(current) > 0 {
					objects = append(objects, strings.Join(current, "\n"))
				}
				current = []string{candidate}
			} else if len(current) > 0 {
				current = append(current, candidate)
			}
		}
		if len(current) > 0 {
			objects = append(objects, strings.Join(current, "\n"))
		}
		return objects
	}
	t.Fatalf("expected YAML sequence property %q", propertyName)
	return nil
}

func yamlIntegerValue(t *testing.T, body, field string) int64 {
	t.Helper()

	prefix := field + ":"
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		value, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)), 10, 64)
		if err != nil {
			t.Fatalf("expected %s integer in %q: %v", field, line, err)
		}
		if value < 0 {
			t.Fatalf("expected non-negative %s, got %d", field, value)
		}
		return value
	}
	t.Fatalf("expected field %s in %s", field, body)
	return 0
}
