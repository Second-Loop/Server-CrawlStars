import { readFile } from "node:fs/promises";
import { createHash } from "node:crypto";

const openAPIText = await readFile(new URL("../../api/openapi.yaml", import.meta.url), "utf8");
const asyncAPIText = await readFile(new URL("../../api/asyncapi.yaml", import.meta.url), "utf8");
const apiDocsText = await readFile(new URL("../../ai-docs/api-docs.md", import.meta.url), "utf8");
const apiReferenceText = await readFile(new URL("../../ai-docs/api-reference.md", import.meta.url), "utf8");
const protocolText = await readFile(new URL("../../ai-docs/protocol.md", import.meta.url), "utf8");
const architectureText = await readFile(new URL("../../ai-docs/architecture.md", import.meta.url), "utf8");
const projectMapText = await readFile(new URL("../../ai-docs/project-map.md", import.meta.url), "utf8");
const decisionsText = await readFile(new URL("../../ai-docs/decisions.md", import.meta.url), "utf8");
const docsBuildText = await readFile(new URL("./build.mjs", import.meta.url), "utf8");
const clientGameConfigBytes = await readFile(new URL("../../client-config/game-config.json", import.meta.url));
const clientGameConfigText = clientGameConfigBytes.toString("utf8");
const clientGameConfig = JSON.parse(clientGameConfigText);
const serverGameConfigText = await readFile(new URL("../../server-config/game-config.json", import.meta.url), "utf8");
const serverGameConfig = JSON.parse(serverGameConfigText);

const requiredRESTPaths = [
  "/health",
  "/matchmaking/join",
  "/rooms",
  "/rooms/{roomID}",
  "/rooms/{roomID}/players",
  "/rooms/{roomID}/start",
];
const requiredWebSocketFields = [
  "MoveDir",
  "AttackDir",
  "PressedAttack",
  "ReadyEventMessage",
  "ReadyAckMessage",
  "SpawnPosition",
  "MapData",
  "Type",
  "Snapshot",
  "status",
  "countdown",
  "GameEndMessage",
  "PlayerId",
  "Result",
  "Win",
  "Lose",
  "Draw",
  "Error",
  "Id",
  "OwnerId",
  "HP",
];

assert(hasLine(openAPIText, "openapi: 3.1.0"), "api/openapi.yaml must use OpenAPI 3.1.0");
assert(hasLine(openAPIText, "x-stability: e1-debug"), "api/openapi.yaml must mark x-stability: e1-debug");
assert(hasLine(openAPIText, "  - url: /"), "api/openapi.yaml must default Swagger UI to the current server origin");
assert(hasLine(openAPIText, "  - url: http://localhost:8080"), "api/openapi.yaml must keep localhost as a local development server");
assert(
  openAPIText.indexOf("  - url: /") < openAPIText.indexOf("  - url: http://localhost:8080"),
  "api/openapi.yaml must list the current server origin before localhost",
);
for (const path of requiredRESTPaths) {
  assert(hasLine(openAPIText, `  ${path}:`), `api/openapi.yaml is missing ${path}`);
}
assert(hasLine(openAPIText, "    ErrorResponse:"), "api/openapi.yaml is missing ErrorResponse schema");
assert(openAPIText.includes("operationId: clearRooms"), "api/openapi.yaml must document DELETE /rooms");
assert(openAPIText.includes("operationId: deleteRoom"), "api/openapi.yaml must document DELETE /rooms/{roomID}");
assert(hasLine(openAPIText, "    MapData:"), "api/openapi.yaml is missing MapData schema");
assertSchemaContains(openAPIText, "MapData", ["enum: [0, 1, 2, 3, 4]"]);
assertCanonicalMatchmakingMapDimensions();
assert(openAPIText.includes("room_full"), "api/openapi.yaml must document room_full");
assert(hasLine(openAPIText, "    DebugBearer:"), "api/openapi.yaml must define DebugBearer");
assertNamedBlockContains(openAPIText, "    DebugBearer:", ["type: http", "scheme: bearer", "401 `unauthorized`", "404 `not_found`"]);
assert(
  countOccurrences(openAPIText, "- DebugBearer: []") === 7,
  "api/openapi.yaml must apply DebugBearer to exactly seven debug operations",
);

const debugOperationIDs = [
  "listRooms",
  "createRoom",
  "clearRooms",
  "getRoom",
  "deleteRoom",
  "createRoomPlayer",
  "startRoom",
];
for (const operationID of debugOperationIDs) {
  const operation = extractOpenAPIOperation(openAPIText, operationID);
  assert(operation.includes("- DebugBearer: []"), `${operationID} must require DebugBearer`);
  assert(operation.includes('"401":'), `${operationID} must document 401`);
  assert(operation.includes('"404":'), `${operationID} must document disabled-default 404 behavior`);
  assert(operation.includes("기본 비활성화"), `${operationID} must say that debug API is disabled by default`);
  assert(operation.includes("not_found"), `${operationID} disabled 404 must name not_found`);
}
const startRoomOperation = extractOpenAPIOperation(openAPIText, "startRoom");
assert(
  startRoomOperation.includes("선택 mode의 participant capacity") && startRoomOperation.includes("human WebSocket client"),
  "startRoom must distinguish participant capacity from the human Ready quorum",
);
assert(
  !startRoomOperation.includes("두 WebSocket client"),
  "startRoom must not hard-code the duel Ready client count",
);

for (const operationID of ["joinMatchmaking", "createRoom", "createRoomPlayer"]) {
  const operation = extractOpenAPIOperation(openAPIText, operationID);
  assert(operation.includes('"500":'), `${operationID} must document 500 internal_error`);
  assert(operation.includes("internal_error"), `${operationID} must name internal_error`);
}

const createRoomPlayerOperation = extractOpenAPIOperation(openAPIText, "createRoomPlayer");
assert(
  createRoomPlayerOperation.includes("matchmaking lifecycle이 이미 잠겼습니다"),
  "createRoomPlayer must document that matched rooms reject debug players",
);
assert(createRoomPlayerOperation.includes("room_full"), "createRoomPlayer 409 must name room_full");

const matchmakingJoinOperation = extractOpenAPIOperation(openAPIText, "joinMatchmaking");
assert(
  matchmakingJoinOperation.includes("1024 bytes"),
  "joinMatchmaking must document the raw 1024-byte request body limit",
);
for (const marker of [
  "첫 human matchmaking join부터 10초",
  "남은 participant slot을 bot으로 충원",
  "late join은 다른 waiting room을 찾거나 만들며",
  "room_cap_reached",
]) {
  assert(matchmakingJoinOperation.includes(marker), `joinMatchmaking must document ${marker}`);
}
const matchmakingJoinRequestBody = extractYAMLNamedBlock(matchmakingJoinOperation, "      requestBody:");
assert(
  !matchmakingJoinRequestBody.includes("required: true"),
  "joinMatchmaking request body must remain optional",
);
assert(
  matchmakingJoinRequestBody.includes('$ref: "#/components/schemas/MatchmakingJoinRequest"'),
  "joinMatchmaking request body must use MatchmakingJoinRequest",
);
const matchmakingJoinBadRequest = extractYAMLNamedBlock(matchmakingJoinOperation, '        "400":');
const invalidGameModeExample = extractYAMLNamedBlock(matchmakingJoinBadRequest, "                invalidGameMode:");
const invalidRequestExample = extractYAMLNamedBlock(matchmakingJoinBadRequest, "                invalidRequest:");
for (const [example, errorCode] of [
  [invalidGameModeExample, "invalid_game_mode"],
  [invalidRequestExample, "invalid_request"],
]) {
  assert(hasTrimmedLine(example, "value:"), `${errorCode} example must include value`);
  assert(hasTrimmedLine(example, "error:"), `${errorCode} example must include error`);
  assert(hasTrimmedLine(example, `code: ${errorCode}`), `${errorCode} example must use its exact error code`);
}
assert(matchmakingJoinOperation.includes('"429":'), "joinMatchmaking must document 429");
assert(matchmakingJoinOperation.includes("Retry-After"), "joinMatchmaking 429 must document Retry-After");
assert(matchmakingJoinOperation.includes("rate_limited"), "joinMatchmaking 429 must name rate_limited");
assert(
  matchmakingJoinOperation.includes("request body decode와 store join보다 먼저"),
  "joinMatchmaking must document quota-before-body-decode ordering",
);
assert(matchmakingJoinOperation.includes("store join보다 먼저"), "joinMatchmaking must document quota-before-store ordering");
assert(matchmakingJoinOperation.includes("409/500"), "joinMatchmaking must document 429 precedence over 409/500");

const redactedSessionTokenSentinel = "example_redacted_session_token_000000000000";
assert(redactedSessionTokenSentinel.length === 43, "redacted session token sentinel must be exactly 43 characters");
assert(/^[A-Za-z0-9_-]{43}$/.test(redactedSessionTokenSentinel), "redacted session token sentinel must be exactly 43 allowed characters");
const canonicalJoinResponse = extractYAMLNamedBlock(matchmakingJoinOperation, '        "201":');
assert(canonicalJoinResponse.includes(`sessionToken: ${redactedSessionTokenSentinel}`), "canonical join response must use the schema-valid redacted session token sentinel");
assert(canonicalJoinResponse.includes(`?token=${redactedSessionTokenSentinel}`), "canonical join response webSocketPath must use the redacted session token sentinel");
assert(countOccurrences(openAPIText, redactedSessionTokenSentinel) === 2, "redacted session token sentinel must appear only in canonical join response fields");

const debugPlayerResponse = extractDelimitedText(
  apiReferenceText,
  "`POST /rooms/{roomID}/players`의 인증된 debug 응답",
  "\n\nError response:",
  "debug player response example",
);
assert(debugPlayerResponse.includes('"characterType": 0'), "debug-created player example must remain Shelly characterType 0");
assert(!debugPlayerResponse.includes('"characterType": 1'), "debug-created player example must not claim Colt characterType 1");
assert(apiReferenceText.includes("Join error priority는 조건부입니다: `429 rate_limited`가 항상 먼저이고, JSON framing/body shape 오류는 Store 진입 전 400 `invalid_request`입니다. 문법적으로 유효한 request는 closed Store면 semantic mode/character 해석보다 먼저 500 `internal_error`를 반환합니다. Store가 열린 경우에만 semantic 순서는 400 `invalid_game_mode` 다음 400 `invalid_character_type`입니다."), "api reference must document conditional join error priority");
for (const [text, name] of [
  [protocolText, "protocol"],
  [architectureText, "architecture"],
  [apiReferenceText, "api reference"],
  [projectMapText, "project map"],
]) {
  for (const marker of ["0=Shelly", "1=Colt", "2=Lily", "4000/3100/4100", "3/3/2"]) {
    assert(text.includes(marker), `${name} must document current character catalog marker ${marker}`);
  }
}
assert(!protocolText.includes("DefaultPlayerHP = 100"), "protocol must not present default HP 100 as current");
assert(!architectureText.includes("player speed/radius/HP = `2`, `0.5`, `100`"), "architecture must not present default HP 100 as current");
assert(!apiReferenceText.includes("- player HP: 100"), "api reference must not present default HP 100 as current");
assert(projectMapText.includes("`characters`"), "project map must document the client v2 characters catalog");

assertSchemaContains(openAPIText, "OpaqueRoomID", ['pattern: "^room_[A-Za-z0-9_-]{22}$"']);
assertSchemaContains(openAPIText, "OpaquePlayerID", ['pattern: "^player_[A-Za-z0-9_-]{22}$"']);
const openAPIPlayerSessionToken = extractYAMLSchema(openAPIText, "PlayerSessionToken");
for (const marker of [
  'pattern: "^[A-Za-z0-9_-]{43}$"',
  "sessionToken",
  "tokenized `webSocketPath`",
  "Unmatched disconnect는 room-owned 10초 fill deadline과 credential을 유지",
  "matched/loading/starting disconnect는 pre-start cancel",
  "Failed upgrade",
]) {
  assert(openAPIPlayerSessionToken.includes(marker), `PlayerSessionToken must document ${marker}`);
}
assert(
  !openAPIPlayerSessionToken.includes("Pre-start match의 실제 disconnect는 room을 취소"),
  "PlayerSessionToken must not collapse unmatched and matched disconnect lifecycle",
);
assertSchemaContains(openAPIText, "PlayerSessionResponse", ["required: [player, sessionToken, webSocketPath]"]);
assertSchemaContains(openAPIText, "MatchmakingJoinRequest", [
  "gameMode:",
  "enum: [duel_1v1, solo, team]",
  'const: ""',
  "default: duel_1v1",
]);
assertSchemaContains(openAPIText, "MatchmakingJoin", [
  "required: [gameMode, room, player, sessionToken, webSocketPath]",
  "gameMode:",
  "enum: [duel_1v1, solo, team]",
]);
assertSchemaContains(openAPIText, "Room", [
  "required: [id, gameMode, status, players, maxPlayers, map, latestSnapshot]",
  "gameMode:",
  "enum: [duel_1v1, solo, team]",
]);
assertSchemaContains(openAPIText, "Player", [
  "enum: [red, blue, solo-1, solo-2, solo-3, solo-4, solo-5, solo-6]",
]);
assertSchemaContains(openAPIText, "APIError", [
  "invalid_game_mode",
  "invalid_request",
  "unauthorized",
  "rate_limited",
  "internal_error",
]);
for (const errorCode of ["invalid_game_mode", "invalid_request"]) {
  assert(
    hasLine(openAPIText, `            - ${errorCode}`),
    `APIError enum must list ${errorCode} at the schema enum indentation`,
  );
}
assert(
  openAPIText.includes("?token=<player-session-token>"),
  "api/openapi.yaml must show a redacted tokenized webSocketPath",
);
for (const schemaName of ["Room", "RoomList", "Player", "SnapshotSummary"]) {
  assertNoSecretFields(extractYAMLSchema(openAPIText, schemaName), `OpenAPI ${schemaName}`);
}
assertNoSequentialIDs(openAPIText, "api/openapi.yaml");
assertOpaqueIDExamples(openAPIText, "api/openapi.yaml");
assertNoBacktickStartedPlainScalars(openAPIText, "api/openapi.yaml");
assertNoColonSpacePlainScalars(openAPIText, "api/openapi.yaml");

assert(hasLine(asyncAPIText, "asyncapi: 3.0.0"), "api/asyncapi.yaml must use AsyncAPI 3.0.0");
assert(hasLine(asyncAPIText, "x-stability: e1-debug"), "api/asyncapi.yaml must mark x-stability: e1-debug");
assert(hasLine(asyncAPIText, "    address: /rooms/{roomID}/players/{playerID}"), "api/asyncapi.yaml must document room player channel");
assert(
  !/^\s*address:.*\?/m.test(asyncAPIText),
  "api/asyncapi.yaml channel address must remain path-only",
);
assert(hasLine(asyncAPIText, "        method: GET"), "api/asyncapi.yaml WebSocket binding must use GET");
assert(hasLine(asyncAPIText, "          required: [token]"), "api/asyncapi.yaml WebSocket query must require token");
assert(hasLine(asyncAPIText, '        bindingVersion: "0.1.0"'), "api/asyncapi.yaml must pin WebSocket bindingVersion 0.1.0");
assert(!asyncAPIText.includes("additionalProperties: false"), "api/asyncapi.yaml must allow ordinary extra query keys");
const asyncAPIPlayerSessionToken = extractYAMLNamedBlock(asyncAPIText, "    playerSessionToken:");
for (const marker of [
  "type: httpApiKey",
  "name: token",
  "in: query",
  "Unmatched disconnect는 room-owned 10초 fill deadline과 credential을 유지",
  "matched/loading/starting disconnect는 pre-start cancel",
]) {
  assert(asyncAPIPlayerSessionToken.includes(marker), `playerSessionToken security must document ${marker}`);
}
assert(
  !asyncAPIPlayerSessionToken.includes("Pre-start match의 실제 disconnect는 room을 취소"),
  "playerSessionToken security must not collapse unmatched and matched disconnect lifecycle",
);
const localServer = extractYAMLNamedBlock(asyncAPIText, "  local:");
assert(
  localServer.includes('$ref: "#/components/securitySchemes/playerSessionToken"'),
  "api/asyncapi.yaml local server must reference playerSessionToken security",
);
assert(asyncAPIText.includes("재연결"), "api/asyncapi.yaml must document reconnect token reuse");
assert(asyncAPIText.includes("raw token과 전체 query"), "api/asyncapi.yaml must prohibit raw token/query logging");
assert(asyncAPIText.includes("pre-start cancel"), "api/asyncapi.yaml must bound reconnect by pre-start cancellation");
assert(asyncAPIText.includes("failed upgrade"), "api/asyncapi.yaml must document failed-upgrade retry");
assert(asyncAPIText.includes("malformed"), "api/asyncapi.yaml must document malformed query rejection");
assert(asyncAPIText.includes("in-flight reservation"), "api/asyncapi.yaml must document reservation conflicts");
assert(asyncAPIText.includes("secret-bearing surface"), "api/asyncapi.yaml must identify every secret-bearing surface");
for (const marker of ["30초 heartbeat", "90초 deadline", "latest-only", "reliable control", "terminal snapshot -> GameEnd -> close"]) {
  assert(asyncAPIText.includes(marker), `api/asyncapi.yaml must document ${marker}`);
}
const gameEndSchema = extractYAMLSchema(asyncAPIText, "GameEndMessage");
const gameEndDescription = extractYAMLNamedBlock(gameEndSchema, "      description: |");
for (const marker of [
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
]) {
  assert(gameEndDescription.includes(marker), `GameEndMessage description must include ${marker}`);
}
assertSchemaContains(asyncAPIText, "GameEndMessage", [
  "required: [Type, PlayerId, Result]",
  "const: GameEnd",
  "enum: [Win, Lose, Draw]",
]);
for (const field of requiredWebSocketFields) {
	assert(asyncAPIText.includes(field), `api/asyncapi.yaml is missing ${field}`);
}
assertSchemaContains(asyncAPIText, "MapData", ["enum: [0, 1, 2, 3, 4]"]);
const projectileDataSchema = extractYAMLSchema(asyncAPIText, "ProjectileData");
const projectileDataDescription = extractYAMLNamedBlock(projectileDataSchema, "      description: |");
for (const marker of [
  "Solo",
  "Team",
  "friendlyFire=false",
  "join/배정 순서",
  "PlayerID 오름차순 input",
]) {
  assert(projectileDataDescription.includes(marker), `ProjectileData description must include ${marker}`);
}
for (const schemaName of ["ReadyPlayer", "PlayerData"]) {
  assertSchemaContains(asyncAPIText, schemaName, [
    "enum: [red, blue, solo-1, solo-2, solo-3, solo-4, solo-5, solo-6]",
  ]);
}
const asyncAPIInfo = extractYAMLNamedBlock(asyncAPIText, "info:");
assert(hasLine(asyncAPIInfo, "  version: 0.6.0"), "api/asyncapi.yaml must publish version 0.6.0");
for (const marker of ["room_cap_reached", "bot_fill_failed"]) {
  assert(!asyncAPIInfo.includes(marker), `AsyncAPI info must not document REST or structured-log marker ${marker}`);
}
const asyncAPIChannels = extractYAMLNamedBlock(asyncAPIText, "channels:");
const roomPlayerChannel = extractYAMLNamedBlock(asyncAPIChannels, "  roomPlayer:");
for (const marker of [
  "Unmatched disconnect는 room-owned 10초 fill deadline과 credential을 유지",
  "matched/loading/starting disconnect는 pre-start cancel",
]) {
  assert(roomPlayerChannel.includes(marker), `roomPlayer lifecycle must document ${marker}`);
}
assert(
  !roomPlayerChannel.includes("Matchmaking pre-start 연결이 실제로 끊기면 room이 취소"),
  "roomPlayer lifecycle must not collapse unmatched and matched disconnect lifecycle",
);
const asyncAPIOperations = extractYAMLNamedBlock(asyncAPIText, "operations:");
const receiveReadyOperation = extractYAMLNamedBlock(asyncAPIOperations, "  receiveReady:");
for (const marker of ["full participant list", "human session만 Ready ACK"]) {
  assert(receiveReadyOperation.includes(marker), `receiveReady must document ${marker}`);
}
const sendReadyAckOperation = extractYAMLNamedBlock(asyncAPIOperations, "  sendReadyAck:");
for (const marker of [
  "Bot은 ACK를 보내지 않습니다",
  "중복 ready ACK는 idempotent",
  "Ready quorum을 재증가시키거나 countdown을 재시작하지 않습니다",
]) {
  assert(sendReadyAckOperation.includes(marker), `sendReadyAck must document ${marker}`);
}
const asyncAPIComponents = extractYAMLNamedBlock(asyncAPIText, "components:");
const asyncAPIMessages = extractYAMLNamedBlock(asyncAPIComponents, "  messages:");
const readyEventMessage = extractYAMLNamedBlock(asyncAPIMessages, "    ReadyEventMessage:");
for (const marker of [
  "full participant assignment",
  "Fallback spawn은 Wall과 Water를 제외하고 Ground와 Bush를 허용합니다",
]) {
  assert(readyEventMessage.includes(marker), `ReadyEventMessage must document ${marker}`);
}
const modeTeamEnum = "enum: [red, blue, solo-1, solo-2, solo-3, solo-4, solo-5, solo-6]";
assert(
  countOccurrences(asyncAPIText, modeTeamEnum) === 2,
  "ReadyPlayer and PlayerData must expose every mode team value exactly once",
);
const readyEventSchema = extractYAMLSchema(asyncAPIText, "ReadyEventMessage");
const readyPlayers = extractYAMLNamedBlock(readyEventSchema, "        Players:");
assert(hasLine(readyPlayers, "          oneOf:"), "Ready Players must use oneOf exact-cardinality array branches");
const exactReadyArrayBranch = /^            - type: array\n              minItems: (2|6)\n              maxItems: \1\n              items:\n                \$ref: "#\/components\/schemas\/ReadyPlayer"$/gm;
const readyPlayerCounts = [...readyPlayers.matchAll(exactReadyArrayBranch)]
  .map(([, count]) => Number(count))
  .sort((left, right) => left - right);
const readyPlayerDirectBranches = readyPlayers.match(/^            - /gm) ?? [];
assert(
  JSON.stringify(readyPlayerCounts) === "[2,6]" && readyPlayerDirectBranches.length === 2,
  "Ready Players must allow only exact array cardinalities 2 or 6",
);
assert(
  receiveReadyOperation.includes("full participant") && sendReadyAckOperation.includes("human client"),
  "Ready/ACK operations must distinguish full participants from the human-only quorum",
);
assert(
  !asyncAPIText.includes("두 matched client") && !asyncAPIText.includes("두 client가 모두 연결") && !asyncAPIText.includes("6개의 서로 다른 WebSocket connection"),
  "api/asyncapi.yaml must not describe participant capacity as an all-human connection count",
);
assert(asyncAPIText.includes("invalid_input"), "api/asyncapi.yaml must document invalid_input");
for (const schemaName of ["ReadyEventMessage", "SnapshotMessage", "Snapshot", "GameEndMessage", "ReadyPlayer", "PlayerData"]) {
  assertNoSecretFields(extractYAMLSchema(asyncAPIText, schemaName), `AsyncAPI ${schemaName}`);
}
assertNoSequentialIDs(asyncAPIText, "api/asyncapi.yaml");
assertOpaqueIDExamples(asyncAPIText, "api/asyncapi.yaml");
assertNoBacktickStartedPlainScalars(asyncAPIText, "api/asyncapi.yaml");
assertNoColonSpacePlainScalars(asyncAPIText, "api/asyncapi.yaml");

validateBotIdentitySchemas();
validateClientTickACKContract();
validateCharacterTypeContract();
validateCharacterNormalAttackContract();

assert(docsBuildText.includes("?token=<player-session-token>"), "docs UI must show a redacted tokenized WebSocket path");
assert(docsBuildText.includes("sessionToken"), "docs UI must explain the sessionToken response");
assert(
  docsBuildText.includes("{ gameMode, room, player, sessionToken, webSocketPath }"),
  "docs UI must show the selected gameMode in the join response",
);
assert(
  docsBuildText.includes("선택 mode의 participant capacity") && docsBuildText.includes("human WebSocket session"),
  "docs UI must distinguish participant capacity from the human WebSocket quorum",
);
assert(
  !docsBuildText.includes("6 human connection") && !docsBuildText.includes("bot fill 없음"),
  "docs UI must not describe the participant capacity as all-human or claim bot absence",
);
for (const marker of [
  "optional `gameMode`",
  "participant capacity",
  "human session",
  "raw body가 1024 bytes",
  "Shelly/Colt/Lily 순서로 `3/3/2` charge",
]) {
  assert(apiDocsText.includes(marker), `ai-docs/api-docs.md must document ${marker}`);
}
const apiDocsSessionLifecycle = extractDelimitedText(
  apiDocsText,
  "Handshake 순서는",
  "\n\nAsyncAPI document dialect",
  "ai-docs/api-docs.md session lifecycle",
);
const docsSessionTokenCard = extractDocsHTMLArticle("Session token");
for (const [text, name, forbiddenMarkers] of [
  [
    apiDocsSessionLifecycle,
    "ai-docs/api-docs.md session lifecycle",
    ["pre-start 실제 disconnect는 room을 취소", "start 전 cancel"],
  ],
  [
    docsSessionTokenCard,
    "docs UI Session token card",
    ["matchmaking pre-start 연결이 실제로 끊기면 room이 취소", "Pre-start match의 실제 disconnect는 room을 취소"],
  ],
]) {
  for (const marker of [
    "Unmatched disconnect는 room-owned 10초 fill deadline과 credential을 유지",
    "matched/loading/starting disconnect는 pre-start cancel",
  ]) {
    assert(text.includes(marker), `${name} must document ${marker}`);
  }
  for (const marker of forbiddenMarkers) {
    assert(!text.includes(marker), `${name} must not contain blanket lifecycle marker ${marker}`);
  }
}
assert(docsBuildText.includes("persistAuthorization: false"), "Swagger UI must not persist debug authorization");
for (const marker of ["pre-start", "failed upgrade", "in-flight reservation", "malformed", "secret-bearing surface", "30초 heartbeat", "90초 deadline", "latest-only", "Reliable control", "Terminal order"]) {
  assert(docsBuildText.includes(marker), `docs UI must document ${marker}`);
}
for (const [text, name, allowedTokens] of [
  [openAPIText, "api/openapi.yaml", [redactedSessionTokenSentinel]],
  [asyncAPIText, "api/asyncapi.yaml", []],
  [docsBuildText, "docs UI", []],
]) {
  assertNoRawSessionTokenExamples(text, name, allowedTokens);
}

const expectedCharacters = new Map([[0, "shelly"], [1, "colt"], [2, "lily"]]);
const approvedClientGameConfigSHA256 = "b351ce594e6fbed9df59ea778d63897c6696510611485691cefcc5eade7fd70d";
assert(
  createHash("sha256").update(clientGameConfigBytes).digest("hex") === approvedClientGameConfigSHA256,
  "client-config/game-config.json must be byte-identical to the approved v2 artifact",
);
assert(clientGameConfig.version === 2, "client config version must be 2");
assert(serverGameConfig.version === 3, "server config version must be 3");
for (const legacyField of [
  '  "version": 2,',
  '  "tileSize": 1.2,',
  '  "playerRadius": 0.5,',
  '  "playerTypes": ["default"],',
  '  "projectileRadius": 0.3,',
  '  "projectileTypes": ["default"]',
]) {
  assert(clientGameConfigText.includes(legacyField), `client legacy field must remain byte-for-byte: ${legacyField}`);
}
assertOnlyKeys(clientGameConfig, ["version", "tileSize", "playerRadius", "playerTypes", "characters", "projectileRadius", "projectileTypes"], "client-config/game-config.json");
assert(clientGameConfig.tileSize === 1.2, "client-config/game-config.json must expose tileSize 1.2");
assert(JSON.stringify(clientGameConfig.playerTypes) === JSON.stringify(["default"]), "legacy playerTypes must stay [default]");
assert(clientGameConfig.playerRadius === 0.5, "legacy playerRadius must stay 0.5");
assert(Array.isArray(clientGameConfig.characters) && clientGameConfig.characters.length === 3, "client catalog must contain exactly 3 entries");
assert(Array.isArray(serverGameConfig.player?.types) && serverGameConfig.player.types.length === 3, "server catalog must contain exactly 3 entries");
const clientCharacters = new Map(clientGameConfig.characters.map(({ characterType, id }) => [characterType, id]));
const serverCharacters = new Map(serverGameConfig.player.types.map(({ characterType, id }) => [characterType, id]));
assert(clientCharacters.size === clientGameConfig.characters.length, "client characterType IDs must be unique");
assert(serverCharacters.size === serverGameConfig.player.types.length, "server characterType IDs must be unique");
assert(new Set(clientGameConfig.characters.map(({ id }) => id)).size === 3, "client string IDs must be unique");
assert(new Set(serverGameConfig.player.types.map(({ id }) => id)).size === 3, "server string IDs must be unique");
assert(JSON.stringify([...clientCharacters].sort()) === JSON.stringify([...expectedCharacters].sort()), "client character mapping drift");
assert(JSON.stringify([...serverCharacters].sort()) === JSON.stringify([...expectedCharacters].sort()), "server character mapping drift");
const expectedClientMetadata = new Map([
  [0, { id: "shelly", name: "Shelly", role: "damage_dealer" }],
  [1, { id: "colt", name: "Colt", role: "damage_dealer" }],
  [2, { id: "lily", name: "Lily", role: "assassin" }],
]);
for (const character of clientGameConfig.characters) {
  assert(JSON.stringify({ id: character.id, name: character.name, role: character.role }) === JSON.stringify(expectedClientMetadata.get(character.characterType)), `client metadata drift for characterType ${character.characterType}`);
}
assert(clientGameConfig.projectileRadius === 0.3, "client-config/game-config.json must expose projectileRadius 0.3");
assert(hasValue(clientGameConfig.projectileTypes, "default"), "client-config/game-config.json must expose default projectile type");

assert(serverGameConfig.tickRate === 30, "server-config/game-config.json must expose tickRate 30");
assert(serverGameConfig.tile?.size === 1.2, "server-config/game-config.json must expose tile.size 1.2");
const expectedServerPlayerTypes = new Map([[0, 4000], [1, 3100], [2, 4100]]);
const expectedNormalAttacks = new Map([
  [0, { kind: "spread_projectile", damagePerHit: 280, rangeTiles: 7.2, maxCharges: 3, rechargeTicks: 30, projectile: { type: "default", count: 5, directionOffsetsDegrees: [-12, -6, 0, 6, 12], intervalTicks: 0 } }],
  [1, { kind: "burst_projectile", damagePerHit: 340, rangeTiles: 9, maxCharges: 3, rechargeTicks: 30, projectile: { type: "default", count: 6, directionOffsetsDegrees: [0], intervalTicks: 6 } }],
  [2, { kind: "melee", damagePerHit: 1100, rangeTiles: 2.2, maxCharges: 2, rechargeTicks: 30 }],
]);
for (const playerType of serverGameConfig.player.types) {
  assert(playerType.radius === 0.5, `server player radius drift for ${playerType.id}`);
  assert(playerType.hp === expectedServerPlayerTypes.get(playerType.characterType), `server player HP drift for ${playerType.id}`);
  assert(playerType.speed === 2, `server player speed drift for ${playerType.id}`);
  assert(!Object.hasOwn(playerType, "maxAttackCharges"), `server player must not expose legacy maxAttackCharges for ${playerType.id}`);
  assert(!Object.hasOwn(playerType, "attackRechargeTicks"), `server player must not expose legacy attackRechargeTicks for ${playerType.id}`);
  assert(JSON.stringify(playerType.normalAttack) === JSON.stringify(expectedNormalAttacks.get(playerType.characterType)), `server normalAttack drift for ${playerType.id}`);
}
assert(hasTypeRadius(serverGameConfig.projectile?.types, "default", 0.3), "server-config/game-config.json must expose default projectile radius 0.3");
assert(hasTypeValue(serverGameConfig.projectile?.types, "default", "speed", 13), "server-config/game-config.json must expose default projectile speed 13");
for (const projectileType of serverGameConfig.projectile?.types ?? []) {
  assert(!Object.hasOwn(projectileType, "damage"), `server projectile type must not expose damage for ${projectileType.id}`);
}
assert(serverGameConfig.map?.width === 20, "server-config/game-config.json must expose the runtime map width");
assert(serverGameConfig.map?.height === 20, "server-config/game-config.json must expose the runtime map height");
assert(serverGameConfig.map?.maxPlayers === 6, "server-config/game-config.json must expose map maxPlayers 6");
const serverMapTiles = serverGameConfig.map?.map?.flat() ?? [];
assert(serverMapTiles.includes(3), "server-config/game-config.json must include TileBush value 3");
assert(serverMapTiles.includes(4), "server-config/game-config.json must include TileWater value 4");

function hasLine(text, want) {
	return text.split(/\r?\n/).some((line) => line === want);
}

function hasTrimmedLine(text, want) {
  return text.split(/\r?\n/).some((line) => line.trim() === want);
}

function countOccurrences(text, needle) {
  return text.split(needle).length - 1;
}

function extractOpenAPIOperation(text, operationID) {
  const lines = text.split(/\r?\n/);
  const operationLine = lines.findIndex((line) => line === `      operationId: ${operationID}`);
  assert(operationLine >= 0, `api/openapi.yaml is missing operationId ${operationID}`);

  let start = operationLine;
  while (start >= 0 && !/^    (get|post|put|patch|delete):$/.test(lines[start])) {
    start -= 1;
  }
  assert(start >= 0, `api/openapi.yaml cannot locate operation ${operationID}`);

  let end = start + 1;
  while (end < lines.length && !/^( {0,4})\S/.test(lines[end])) {
    end += 1;
  }
  return lines.slice(start, end).join("\n");
}

function assertCanonicalMatchmakingMapDimensions() {
  const joinOperation = extractOpenAPIOperation(openAPIText, "joinMatchmaking");
  const canonicalExample = extractYAMLNamedBlock(joinOperation, "              example:");
  const mapBlock = extractYAMLNamedBlock(canonicalExample, "                  map:");
  const width = extractCanonicalMapDimension(mapBlock, "width");
  const height = extractCanonicalMapDimension(mapBlock, "height");
  const mapLine = mapBlock.split(/\r?\n/).find((line) => line.startsWith("                    map: "));
  assert(mapLine, "canonical matchmaking map example must include an inline map grid");

  const map = JSON.parse(mapLine.slice("                    map: ".length));
  assert(Array.isArray(map), "canonical matchmaking map example must be a JSON array");
  assert(map.length === height, "canonical matchmaking map example row count must equal height");
  for (const [rowIndex, row] of map.entries()) {
    assert(Array.isArray(row), `canonical matchmaking map row ${rowIndex} must be an array`);
    assert(row.length === width, `canonical matchmaking map row ${rowIndex} length must equal width`);
  }
}

function extractCanonicalMapDimension(mapBlock, dimension) {
  const line = mapBlock.split(/\r?\n/).find((candidate) => candidate.startsWith(`                    ${dimension}: `));
  assert(line, `canonical matchmaking map example must include ${dimension}`);
  const value = Number(line.slice(`                    ${dimension}: `.length));
  assert(Number.isSafeInteger(value) && value > 0, `canonical matchmaking map example ${dimension} must be a positive integer`);
  return value;
}

function extractYAMLSchema(text, schemaName) {
  const schemasMarker = "\n  schemas:\n";
  const schemasStart = text.indexOf(schemasMarker);
  assert(schemasStart >= 0, "YAML is missing components schemas");
  return extractYAMLNamedBlock(text.slice(schemasStart + 1), `    ${schemaName}:`);
}

function extractYAMLNamedBlock(text, marker) {
  const lines = text.split(/\r?\n/);
  const start = lines.findIndex((line) => line === marker);
  assert(start >= 0, `YAML is missing ${marker.trim()}`);
  const indent = marker.length - marker.trimStart().length;

  let end = start + 1;
  while (end < lines.length) {
    const line = lines[end];
    if (line.trim() !== "" && line.length - line.trimStart().length <= indent) {
      break;
    }
    end += 1;
  }
  return lines.slice(start, end).join("\n");
}

function extractYAMLSequenceObjects(text, propertyName) {
  const lines = text.split(/\r?\n/);
  const objects = [];
  for (let index = 0; index < lines.length; index += 1) {
    const match = new RegExp(`^(\\s*)${propertyName}:$`).exec(lines[index]);
    if (!match) continue;
    const itemIndent = match[1].length + 2;
    const itemPrefix = `${" ".repeat(itemIndent)}- `;
    let cursor = index + 1;
    let current = [];
    while (cursor < lines.length) {
      const line = lines[cursor];
      const indent = line.length - line.trimStart().length;
      if (line.trim() && indent <= match[1].length) break;
      if (line.startsWith(itemPrefix)) {
        if (current.length > 0) objects.push(current.join("\n"));
        current = [line];
      } else if (current.length > 0) {
        current.push(line);
      }
      cursor += 1;
    }
    if (current.length > 0) objects.push(current.join("\n"));
  }
  return objects;
}

function assertEveryExamplePlayerHasIsBot(objects, name) {
  assert(objects.length > 0, `${name} must include player objects`);
  for (const [index, object] of objects.entries()) {
    const flags = object.match(/^\s+IsBot:\s+(?:true|false)$/gm) ?? [];
    assert(flags.length === 1, `${name} player ${index} must contain exactly one boolean IsBot`);
  }
}

function extractDocsJSONExample(heading) {
  const examplesStart = docsBuildText.indexOf("<h2>예시</h2>");
  assert(examplesStart >= 0, "docs UI must include examples section");
  const headingStart = docsBuildText.indexOf(`<h3>${heading}</h3>`, examplesStart);
  assert(headingStart >= 0, `docs UI is missing ${heading} example`);
  const opening = "<pre><code>";
  const codeStart = docsBuildText.indexOf(opening, headingStart);
  const codeEnd = docsBuildText.indexOf("</code></pre>", codeStart);
  assert(codeStart >= 0 && codeEnd > codeStart, `docs UI ${heading} JSON is missing`);
  return JSON.parse(docsBuildText.slice(codeStart + opening.length, codeEnd));
}

function extractDocsHTMLArticle(heading) {
  const headingStart = docsBuildText.indexOf(`<h3>${heading}</h3>`);
  assert(headingStart >= 0, `docs UI is missing ${heading} article`);
  const articleEnd = docsBuildText.indexOf("</article>", headingStart);
  assert(articleEnd > headingStart, `docs UI ${heading} article is not closed`);
  return docsBuildText.slice(headingStart, articleEnd);
}

function extractDelimitedText(text, startMarker, endMarker, name) {
  const start = text.indexOf(startMarker);
  assert(start >= 0, `${name} is missing start marker ${startMarker}`);
  const end = text.indexOf(endMarker, start);
  assert(end > start, `${name} is missing end marker ${endMarker}`);
  return text.slice(start, end);
}

function assertEveryJSONPlayerHasIsBot(players, name) {
  assert(Array.isArray(players) && players.length > 0, `${name} must include players`);
  for (const [index, player] of players.entries()) {
    assert(Object.hasOwn(player, "IsBot"), `${name} player ${index} is missing IsBot`);
    assert(typeof player.IsBot === "boolean", `${name} player ${index} IsBot must be boolean`);
  }
}

function assertEveryYAMLPlayerHasCharacterType(objects, name) {
  assert(objects.length > 0, `${name} must include player objects`);
  for (const [index, object] of objects.entries()) {
    const fields = [...object.matchAll(/^\s+CharacterType:\s+(-?\d+)$/gm)];
    assert(fields.length === 1, `${name} player ${index} must contain exactly one CharacterType`);
    const characterType = Number(fields[0][1]);
    assert(Number.isSafeInteger(characterType) && characterType >= 0 && characterType <= 2, `${name} player ${index} has invalid CharacterType`);
    if (/^\s+IsBot:\s+true$/m.test(object)) {
      assert(characterType === 0, `${name} bot player ${index} must use Shelly`);
    }
  }
}

function assertEveryJSONPlayerHasCharacterType(players, name) {
  assert(Array.isArray(players) && players.length > 0, `${name} must include players`);
  for (const [index, player] of players.entries()) {
    assert(Object.hasOwn(player, "CharacterType"), `${name} player ${index} is missing CharacterType`);
    assert(Number.isSafeInteger(player.CharacterType) && player.CharacterType >= 0 && player.CharacterType <= 2, `${name} player ${index} has invalid CharacterType`);
    if (player.IsBot === true) {
      assert(player.CharacterType === 0, `${name} bot player ${index} must use Shelly`);
    }
  }
}

function validateBotIdentitySchemas() {
  assertSchemaContains(openAPIText, "Player", [
    "required: [id, team, slot, isBot, characterType]",
    "isBot:",
    "type: boolean",
  ]);
  assertSchemaContains(openAPIText, "HumanPlayer", [
    '$ref: "#/components/schemas/Player"',
    "const: false",
  ]);
  assertSchemaContains(openAPIText, "MatchmakingJoin", [
    '$ref: "#/components/schemas/HumanPlayer"',
  ]);
  assertSchemaContains(openAPIText, "PlayerSessionResponse", [
    '$ref: "#/components/schemas/HumanPlayer"',
  ]);
  assert(!/^  \/.*bot/im.test(openAPIText), "OpenAPI must not add a bot endpoint");

  assert(hasLine(asyncAPIText, "  version: 0.6.0"), "AsyncAPI version must be 0.6.0");
  assertSchemaContains(asyncAPIText, "ReadyPlayer", [
    "required: [Id, Team, Slot, IsBot, CharacterType, SpawnPosition]",
  ]);
  assertSchemaContains(asyncAPIText, "PlayerData", [
    "required: [Id, Team, Slot, IsBot, CharacterType, Pos, MoveDir, AttackDir, Speed, Radius, HP, PressedAttack, IsDead, LastProcessedClientTick]",
  ]);
  const messagesBlock = extractYAMLNamedBlock(asyncAPIText, "  messages:");
  const readyMessage = extractYAMLNamedBlock(messagesBlock, "    ReadyEventMessage:");
  const snapshotMessage = extractYAMLNamedBlock(messagesBlock, "    SnapshotMessage:");
  const readyPlayers = extractYAMLSequenceObjects(readyMessage, "Players");
  const gameplayPlayers = extractYAMLSequenceObjects(snapshotMessage, "Players");
  assertEveryExamplePlayerHasIsBot(readyPlayers, "AsyncAPI Ready examples");
  assertEveryExamplePlayerHasIsBot(gameplayPlayers, "AsyncAPI gameplay examples");
  assert(readyPlayers.some((object) => object.includes("IsBot: false")), "Ready must show a human");
  assert(readyPlayers.some((object) => object.includes("IsBot: true")), "Ready must show a bot");
  assert(gameplayPlayers.some((object) => object.includes("IsBot: false")), "Gameplay must show a human");
  assert(gameplayPlayers.some((object) => object.includes("IsBot: true")), "Gameplay must show a bot");

  const docsReady = extractDocsJSONExample("Ready Event");
  const docsGameplay = extractDocsJSONExample("Gameplay");
  assertEveryJSONPlayerHasIsBot(docsReady.Players, "docs UI Ready example");
  assertEveryJSONPlayerHasIsBot(docsGameplay.Snapshot.Players, "docs UI Gameplay example");
  for (const [players, name] of [
    [docsReady.Players, "docs UI Ready example"],
    [docsGameplay.Snapshot.Players, "docs UI Gameplay example"],
  ]) {
    assert(players.some((player) => player.IsBot === false), `${name} must show a human`);
    assert(players.some((player) => player.IsBot === true), `${name} must show a bot`);
  }
}

function validateCharacterTypeContract() {
  assertSchemaContains(openAPIText, "CharacterType", [
    "type: integer",
    "enum: [0, 1, 2]",
  ]);

  const joinRequest = extractYAMLSchema(openAPIText, "MatchmakingJoinRequest");
  const characterTypeProperty = extractSchemaProperty(joinRequest, "characterType");
  assert(characterTypeProperty.includes('$ref: "#/components/schemas/CharacterType"'), "join characterType must use the shared schema");
  assert(!topLevelRequiredFields(joinRequest).includes("characterType"), "join characterType must remain optional until SL-98");
  for (const forbidden of ["deprecated: true", "default:", "nullable:"]) {
    assert(!characterTypeProperty.includes(forbidden), `join characterType must not contain ${forbidden}`);
  }

  const playerSchema = extractYAMLSchema(openAPIText, "Player");
  assert(topLevelRequiredFields(playerSchema).filter((field) => field === "characterType").length === 1, "REST Player must require characterType exactly once");

  assert(hasLine(asyncAPIText, "  version: 0.6.0"), "AsyncAPI version must be 0.6.0");
  for (const schemaName of ["ReadyPlayer", "PlayerData"]) {
    const schema = extractYAMLSchema(asyncAPIText, schemaName);
    assert(topLevelRequiredFields(schema).filter((field) => field === "CharacterType").length === 1, `${schemaName} must require CharacterType exactly once`);
    assert(extractSchemaProperty(schema, "CharacterType").includes("enum: [0, 1, 2]"), `${schemaName}.CharacterType must use stable IDs`);
  }

  const messages = extractYAMLNamedBlock(asyncAPIText, "  messages:");
  const readyPlayers = extractYAMLSequenceObjects(extractYAMLNamedBlock(messages, "    ReadyEventMessage:"), "Players");
  const gameplayPlayers = extractYAMLSequenceObjects(extractYAMLNamedBlock(messages, "    SnapshotMessage:"), "Players");
  assertEveryYAMLPlayerHasCharacterType(readyPlayers, "AsyncAPI Ready examples");
  assertEveryYAMLPlayerHasCharacterType(gameplayPlayers, "AsyncAPI gameplay examples");
  assert(asyncAPIText.includes("CharacterType: 2"), "AsyncAPI must show Lily stable ID 2");

  const docsReady = extractDocsJSONExample("Ready Event");
  const docsGameplay = extractDocsJSONExample("Gameplay");
  assertEveryJSONPlayerHasCharacterType(docsReady.Players, "docs UI Ready example");
  assertEveryJSONPlayerHasCharacterType(docsGameplay.Snapshot.Players, "docs UI Gameplay example");
}

function validateCharacterNormalAttackContract() {
  const inputSchema = extractYAMLSchema(asyncAPIText, "InputMessage");
  const inputPressedAttack = extractSchemaProperty(inputSchema, "PressedAttack");
  for (const marker of ["server config v3", "캐릭터별 `normalAttack`", "activation 요청"]) {
    assert(inputPressedAttack.includes(marker), `InputMessage.PressedAttack must document ${marker}`);
  }

  const playerSchema = extractYAMLSchema(asyncAPIText, "PlayerData");
  const snapshotPressedAttack = extractSchemaProperty(playerSchema, "PressedAttack");
  for (const marker of ["activation tick", "공격이 승인됐을 때만 true"]) {
    assert(snapshotPressedAttack.includes(marker), `PlayerData.PressedAttack must document ${marker}`);
  }

  const snapshotSchema = extractYAMLSchema(asyncAPIText, "Snapshot");
  for (const marker of ["State.Step` 한 번", "같은 tick의 melee 피해", "GameEnd 계산"]) {
    assert(snapshotSchema.includes(marker), `Snapshot must document ${marker}`);
  }
  const projectilesProperty = extractSchemaProperty(snapshotSchema, "Projectiles");
  assert(
    projectilesProperty.includes("attack activation에서 생성되거나 이동/충돌로 갱신된 projectile history"),
    "Snapshot.Projectiles must document character attack projectile history",
  );

  const projectileSchema = extractYAMLSchema(asyncAPIText, "ProjectileData");
  assert(
    extractSchemaProperty(projectileSchema, "Damage").includes("normalAttack.damagePerHit"),
    "ProjectileData.Damage must document normalAttack.damagePerHit ownership",
  );
  assert(
    extractSchemaProperty(projectileSchema, "Type").includes("normalAttack.projectile.type"),
    "ProjectileData.Type must document normalAttack.projectile.type ownership",
  );

  const messagesBlock = extractYAMLNamedBlock(asyncAPIText, "  messages:");
  const snapshotMessage = extractYAMLNamedBlock(messagesBlock, "    SnapshotMessage:");
  const gameplay = extractYAMLNamedBlock(snapshotMessage, "        - name: gameplay");
  assertYAMLShellySpreadExample(gameplay, "AsyncAPI gameplay example");
  assertJSONShellySpreadExample(extractDocsJSONExample("Gameplay").Snapshot, "docs UI Gameplay example");
  assertJSONShellySpreadExample(
    extractMarkdownJSONExample(apiReferenceText, "Server snapshot:", "api reference Server snapshot"),
    "api reference Server snapshot",
  );
  assertJSONShellySpreadExample(
    extractMarkdownJSONExample(apiDocsText, "Server message wrapper:", "api docs Server message wrapper"),
    "api docs Server message wrapper",
  );

  for (const [text, name, markers] of [
    [protocolText, "protocol", ["Shelly는 activation tick에 5발을 동시에", "A+[0,6,12,18,24,30]", "Lily는 2.2 tile centerline", "모든 input과 movement 적용 뒤 clone한 post-movement player snapshot", "wall/boundary까지의 range를 먼저", "Client parser 구현과 final balancing은 범위 밖"]],
    [architectureText, "architecture", ["server config v3가 일반 공격", "player type의 `normalAttack`", "production `State.Step`", "room-local config", "Shelly/Colt/Lily는 각각 `3/3/2` attack charge", "projectile emission 또는 Lily melee intent를 승인"]],
    [projectMapText, "project map", ["SL-83 일반 공격", "3/3/2 charge", "A+31", "모든 input과 movement 적용 뒤 clone한 post-movement player snapshot", "same-tick batched damage", "client parser는 아직 범위 밖"]],
    [apiReferenceText, "api reference", ["server config v3의 캐릭터별 일반 공격 activation 요청", "A+[0,6,12,18,24,30]", "2.2 tile centerline", "기존 `Damage`와 `Type`"]],
    [decisionsText, "decisions", ["ADR-0036", "server config v3", "A+[0,6,12,18,24,30]", "A+31", "모든 input과 movement 적용 뒤 clone한 post-movement player snapshot", "same-tick batched damage", "range 판정 순서", "Client parser 구현과 final balancing"]],
  ]) {
    for (const marker of markers) {
      assert(text.includes(marker), `${name} must document normal attack marker ${marker}`);
    }
  }
}

function validateClientTickACKContract() {
  const inputSchema = extractYAMLSchema(asyncAPIText, "InputMessage");
  const clientTickProperty = extractSchemaProperty(inputSchema, "ClientTick");
  for (const marker of ["type: integer", "format: int64", "minimum: 0"]) {
    assert(clientTickProperty.includes(marker), `InputMessage.ClientTick must include ${marker}`);
  }
  assert(!topLevelRequiredFields(inputSchema).includes("ClientTick"), "InputMessage.ClientTick must remain optional");

  const playerSchema = extractYAMLSchema(asyncAPIText, "PlayerData");
  const playerRequired = topLevelRequiredFields(playerSchema);
  assert(
    playerRequired.filter((field) => field === "LastProcessedClientTick").length === 1,
    "PlayerData must require LastProcessedClientTick exactly once",
  );
  const processedTickProperty = extractSchemaProperty(playerSchema, "LastProcessedClientTick");
  for (const marker of ["type: integer", "format: int64", "minimum: 0"]) {
    assert(processedTickProperty.includes(marker), `PlayerData.LastProcessedClientTick must include ${marker}`);
  }

  const messagesBlock = extractYAMLNamedBlock(asyncAPIText, "  messages:");
  const inputMessage = extractYAMLNamedBlock(messagesBlock, "    InputMessage:");
  const inputExamples = extractYAMLNamedBlock(inputMessage, "      examples:");
  assert(inputExamples.includes("ClientTick: 12"), "Input examples must show a positive ClientTick");
  assert(inputExamples.includes("ClientTick: 0"), "Input examples must show legacy ClientTick 0");

  const snapshotMessage = extractYAMLNamedBlock(messagesBlock, "    SnapshotMessage:");
  const startingSignal = extractYAMLNamedBlock(snapshotMessage, "        - name: startingSignal");
  const startedControl = extractYAMLNamedBlock(snapshotMessage, "        - name: startedControl");
  const gameplay = extractYAMLNamedBlock(snapshotMessage, "        - name: gameplay");
  assertLifecyclePlayersNull(startingSignal, "starting", "startingSignal");
  assertLifecyclePlayersNull(startedControl, "started", "startedControl");

  const gameplayPlayers = extractYAMLSequenceObjects(gameplay, "Players");
  assertEveryGameplayPlayerHasClientTickACK(gameplayPlayers, "AsyncAPI gameplay example");
  assert(gameplayPlayers.some((object) => object.includes("IsBot: false")), "Gameplay ACK example must show a human");
  assert(gameplayPlayers.some((object) => object.includes("IsBot: true")), "Gameplay ACK example must show a bot");
  assert(
    gameplayPlayers.some((object) => object.includes("IsBot: false") && extractExampleACK(object) > 0n),
    "Gameplay ACK example must show at least one human with a positive processed tick",
  );

  const openAPISchemas = extractYAMLNamedBlock(openAPIText, "  schemas:");
  assert(countExactLines(openAPISchemas, "    PlayerData:") === 0, "OpenAPI must not define gameplay PlayerData");
  assert(countExactLines(openAPISchemas, "    InputMessage:") === 0, "OpenAPI must not define gameplay InputMessage");
  assert(!openAPIText.includes("ClientTick"), "OpenAPI must not define gameplay ClientTick");
  assert(!openAPIText.includes("LastProcessedClientTick"), "OpenAPI must not define processed input ACK");

  const docsGameplay = extractDocsJSONExample("Gameplay");
  assertEveryJSONPlayerHasClientTickACK(docsGameplay.Snapshot.Players, "docs UI Gameplay example");
  assert(
    docsGameplay.Snapshot.Players.some(
      (player) =>
        player.IsBot === false &&
        Number.isSafeInteger(player.LastProcessedClientTick) &&
        player.LastProcessedClientTick > 0,
    ),
    "docs UI Gameplay example must show at least one human with a positive processed tick",
  );
  assert(
    docsGameplay.Snapshot.Players.some((player) => player.IsBot === true),
    "docs UI Gameplay example must show a bot with ACK 0",
  );

  assertTopLevelRequiredFieldsParserContract();
}

function assertLifecyclePlayersNull(example, status, name) {
  assert(example.includes(`status: ${status}`), `${name} must use status ${status}`);
  assert(hasTrimmedLine(example, "Tick: 0"), `${name} must keep Tick 0`);
  assert(hasTrimmedLine(example, "Players: null"), `${name} must keep Players null`);
}

function assertEveryGameplayPlayerHasClientTickACK(objects, name) {
  assert(objects.length > 0, `${name} must include player objects`);
  for (const [index, object] of objects.entries()) {
    const botFlags = object.match(/^\s+IsBot:\s+(true|false)$/gm) ?? [];
    const ackFields = [...object.matchAll(/^\s+LastProcessedClientTick:\s+(\d+)$/gm)];
    assert(botFlags.length === 1, `${name} player ${index} must contain exactly one IsBot`);
    assert(ackFields.length === 1, `${name} player ${index} must contain exactly one LastProcessedClientTick`);
    const isBot = botFlags[0].trim().endsWith("true");
    const ack = BigInt(ackFields[0][1]);
    assert(ack >= 0n, `${name} player ${index} ACK must be a non-negative integer`);
    if (isBot) {
      assert(ack === 0n, `${name} bot player ${index} ACK must be 0`);
    }
  }
}

function extractExampleACK(object) {
  const matches = [...object.matchAll(/^\s+LastProcessedClientTick:\s+(\d+)$/gm)];
  assert(matches.length === 1, "gameplay player must expose exactly one ACK before value inspection");
  return BigInt(matches[0][1]);
}

function extractMarkdownJSONExample(text, marker, name) {
  const markerStart = text.indexOf(marker);
  assert(markerStart >= 0, `${name} is missing marker ${marker}`);
  const fence = "```json\n";
  const payloadStart = text.indexOf(fence, markerStart);
  assert(payloadStart >= 0, `${name} is missing JSON fence`);
  const payloadEnd = text.indexOf("\n```", payloadStart + fence.length);
  assert(payloadEnd > payloadStart, `${name} JSON fence is not closed`);
  return JSON.parse(text.slice(payloadStart + fence.length, payloadEnd));
}

function extractYAMLScalar(object, field, name) {
  const match = new RegExp(`^\\s+(?:-\\s+)?${field}:\\s+([^\\s#]+)\\s*$`, "m").exec(object);
  assert(match, `${name} is missing ${field}`);
  return match[1];
}

function extractYAMLVector(object, field, name) {
  const number = "([+-]?(?:\\d+(?:\\.\\d*)?|\\.\\d+)(?:e[+-]?\\d+)?)";
  const match = new RegExp(`^\\s+${field}:\\s*$[\\s\\S]*?^\\s+x:\\s+${number}\\s*$[\\s\\S]*?^\\s+y:\\s+${number}\\s*$`, "mi").exec(object);
  assert(match, `${name} is missing ${field}.x/y`);
  return { x: Number(match[1]), y: Number(match[2]) };
}

function shellySpreadDirections() {
  return [
    { x: -0.9781476007338057, y: 0.20791169081775931 },
    { x: -0.9945218953682733, y: 0.10452846326765346 },
    { x: -1, y: 0 },
    { x: -0.9945218953682733, y: -0.10452846326765346 },
    { x: -0.9781476007338057, y: -0.20791169081775931 },
  ];
}

function assertDirection(actual, expected, name) {
  assert(Number.isFinite(actual.x) && Number.isFinite(actual.y), `${name} direction must be finite`);
  assert(
    Math.abs(actual.x - expected.x) <= 1e-12 && Math.abs(actual.y - expected.y) <= 1e-12,
    `${name} direction must match the configured Shelly spread`,
  );
}

function assertYAMLShellySpreadExample(gameplay, name) {
  const players = extractYAMLSequenceObjects(gameplay, "Players");
  const shellyActivations = players.filter(
    (player) =>
      extractYAMLScalar(player, "CharacterType", `${name} player`) === "0" &&
      extractYAMLScalar(player, "PressedAttack", `${name} player`) === "true",
  );
  assert(shellyActivations.length === 1, `${name} must contain exactly one approved Shelly activation`);
  const ownerID = extractYAMLScalar(shellyActivations[0], "Id", `${name} Shelly`);
  const projectiles = extractYAMLSequenceObjects(gameplay, "Projectiles");
  const expectedDirections = shellySpreadDirections();
  assert(projectiles.length === expectedDirections.length, `${name} Shelly activation must contain five projectiles`);
  for (const [index, projectile] of projectiles.entries()) {
    const projectileName = `${name} projectile ${index}`;
    assert(extractYAMLScalar(projectile, "OwnerId", projectileName) === ownerID, `${projectileName} must use the Shelly owner`);
    assert(extractYAMLScalar(projectile, "Damage", projectileName) === "280", `${projectileName} must use Shelly damage 280`);
    assert(extractYAMLScalar(projectile, "Type", projectileName) === "default", `${projectileName} must use the configured projectile type`);
    assertDirection(extractYAMLVector(projectile, "Dir", projectileName), expectedDirections[index], projectileName);
  }
}

function assertJSONShellySpreadExample(message, name) {
  const snapshot = message.Snapshot ?? message;
  assert(snapshot && Array.isArray(snapshot.Players), `${name} must contain Snapshot.Players`);
  const shellyActivations = snapshot.Players.filter(
    (player) => player.CharacterType === 0 && player.PressedAttack === true,
  );
  assert(shellyActivations.length === 1, `${name} must contain exactly one approved Shelly activation`);
  const projectiles = snapshot.Projectiles;
  const expectedDirections = shellySpreadDirections();
  assert(Array.isArray(projectiles) && projectiles.length === expectedDirections.length, `${name} Shelly activation must contain five projectiles`);
  for (const [index, projectile] of projectiles.entries()) {
    const projectileName = `${name} projectile ${index}`;
    assert(projectile.OwnerId === shellyActivations[0].Id, `${projectileName} must use the Shelly owner`);
    assert(projectile.Damage === 280, `${projectileName} must use Shelly damage 280`);
    assert(projectile.Type === "default", `${projectileName} must use the configured projectile type`);
    assertDirection(projectile.Dir, expectedDirections[index], projectileName);
  }
}

function extractSchemaProperty(schema, propertyName) {
  const properties = extractYAMLNamedBlock(schema, "      properties:");
  const marker = `        ${propertyName}:`;
  assert(countExactLines(properties, marker) === 1, `${propertyName} property must appear exactly once`);
  return extractYAMLNamedBlock(properties, marker);
}

function topLevelRequiredFields(schema) {
  const lines = schema.split(/\r?\n/);
  const matches = lines
    .map((line, index) => ({ line, index }))
    .filter(({ line }) => line.startsWith("      required:"));
  assert(matches.length <= 1, "schema must have at most one top-level required list");
  if (matches.length === 0) return [];

  const [{ line, index }] = matches;
  const value = line.slice("      required:".length).trim();
  if (value !== "") {
    const inline = value.match(/^\[(.*)\]$/);
    assert(inline, "schema top-level required must be an inline or block list");
    if (inline[1].trim() === "") return [];
    return inline[1].split(",").map((field) => requiredFieldName(field));
  }

  const fields = [];
  for (let cursor = index + 1; cursor < lines.length; cursor += 1) {
    const candidate = lines[cursor];
    if (candidate.trim() === "" || candidate.trimStart().startsWith("#")) continue;
    const indentation = candidate.length - candidate.trimStart().length;
    if (indentation <= 6) break;
    const item = candidate.match(/^        -\s+(.+)$/);
    assert(item, "schema top-level required block must contain only list items");
    fields.push(requiredFieldName(item[1]));
  }
  assert(fields.length > 0, "schema top-level required block must not be empty");
  return fields;
}

function requiredFieldName(value) {
  const field = value.trim();
  assert(/^[A-Za-z_][A-Za-z0-9_]*$/.test(field), `invalid required field name ${field}`);
  return field;
}

function assertTopLevelRequiredFieldsParserContract() {
  const inline = `    Example:\n      required: [First, Second]\n      properties:\n        First:\n          type: string`;
  assert(
    JSON.stringify(topLevelRequiredFields(inline)) === JSON.stringify(["First", "Second"]),
    "required parser must support inline lists",
  );

  const block = `    Example:\n      required:\n        - First\n        - Second\n      properties:\n        First:\n          type: string`;
  assert(
    JSON.stringify(topLevelRequiredFields(block)) === JSON.stringify(["First", "Second"]),
    "required parser must support block lists",
  );

  const nestedOnly = `    Example:\n      properties:\n        Child:\n          required: [Nested]\n          properties:\n            Nested:\n              type: string`;
  assert(topLevelRequiredFields(nestedOnly).length === 0, "required parser must ignore nested lists");

  let malformedRejected = false;
  try {
    topLevelRequiredFields("    Example:\n      required: First");
  } catch {
    malformedRejected = true;
  }
  assert(malformedRejected, "required parser must reject malformed scalar values");
}

function countExactLines(text, want) {
  return text.split(/\r?\n/).filter((line) => line === want).length;
}

function assertEveryJSONPlayerHasClientTickACK(players, name) {
  assert(Array.isArray(players) && players.length > 0, `${name} must include players`);
  for (const [index, player] of players.entries()) {
    assert(Object.hasOwn(player, "LastProcessedClientTick"), `${name} player ${index} is missing LastProcessedClientTick`);
    assert(
      Number.isSafeInteger(player.LastProcessedClientTick) && player.LastProcessedClientTick >= 0,
      `${name} player ${index} ACK must be a non-negative integer`,
    );
    if (player.IsBot === true) {
      assert(player.LastProcessedClientTick === 0, `${name} bot player ${index} ACK must be 0`);
    }
  }
}

function assertSchemaContains(text, schemaName, markers) {
  const schema = extractYAMLSchema(text, schemaName);
  for (const marker of markers) {
    assert(schema.includes(marker), `${schemaName} must include ${marker}`);
  }
}

function assertNamedBlockContains(text, blockMarker, markers) {
  const block = extractYAMLNamedBlock(text, blockMarker);
  for (const marker of markers) {
    assert(block.includes(marker), `${blockMarker.trim()} must include ${marker}`);
  }
}

function assertNoSecretFields(block, name) {
  assert(
    !/(?:sessionToken|digest|PlayerSessionToken|PlayerSessionResponse)/.test(block) && !/^\s+token:/m.test(block),
    `${name} must not expose token or digest fields`,
  );
}

function assertNoSequentialIDs(text, name) {
  assert(!/\b(?:room|player)-\d+\b/.test(text), `${name} must use opaque room/player examples`);
}

function assertOpaqueIDExamples(text, name) {
  const exampleLines = text.split(/\r?\n/).filter((line) => /\bexample:|^\s+(?:-\s+)?(?:Id|PlayerId):/.test(line));
  const opaqueIDs = exampleLines.flatMap((line) => [...line.matchAll(/\b(room|player)_([A-Za-z0-9_-]+)/g)]);
  assert(opaqueIDs.length > 0, `${name} must include opaque ID examples`);
  for (const [, prefix, payload] of opaqueIDs) {
    assert(payload.length === 22, `${name} ${prefix}_ example must have a 22-character payload`);
  }
}

function assertNoRawSessionTokenExamples(text, name, allowedTokens = []) {
  const tokens = [...text.matchAll(/(?<![A-Za-z0-9_-])[A-Za-z0-9_-]{43}(?![A-Za-z0-9_-])/g)].map(([token]) => token);
  assert(tokens.every((token) => allowedTokens.includes(token)), `${name} must not contain a raw 43-character session token example`);
}

function assertNoBacktickStartedPlainScalars(text, name) {
	const lines = text.split(/\r?\n/);
	for (const [index, line] of lines.entries()) {
		if (/^\s+[A-Za-z0-9_-]+:\s+`/.test(line)) {
			throw new Error(`${name}:${index + 1} must quote YAML values that start with a backtick`);
		}
	}
}

function assertNoColonSpacePlainScalars(text, name) {
	const lines = text.split(/\r?\n/);
	for (const [index, line] of lines.entries()) {
		const match = /^\s+[A-Za-z0-9_-]+:\s+(.+)$/.exec(line);
		if (!match) {
			continue;
		}
		const value = match[1].trimStart();
		if (/^["'|>\[{]/.test(value)) {
			continue;
		}
		if (value.includes(": ")) {
			throw new Error(`${name}:${index + 1} must quote or block YAML values that contain ": "`);
		}
	}
}

function hasTypeRadius(types, id, radius) {
	return Array.isArray(types) && types.some((type) => type.id === id && type.radius === radius);
}

function hasTypeValue(types, id, key, value) {
	return Array.isArray(types) && types.some((type) => type.id === id && type[key] === value);
}

function hasValue(values, value) {
	return Array.isArray(values) && values.includes(value);
}

function assertOnlyKeys(object, keys, name) {
	const allowed = new Set(keys);
	for (const key of Object.keys(object)) {
		assert(allowed.has(key), `${name} must not include server-only key ${key}`);
	}
	for (const key of keys) {
		assert(Object.hasOwn(object, key), `${name} is missing ${key}`);
	}
}

function assert(condition, message) {
	if (!condition) {
		throw new Error(message);
	}
}
