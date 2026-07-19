import { readFile } from "node:fs/promises";

const openAPIText = await readFile(new URL("../../api/openapi.yaml", import.meta.url), "utf8");
const asyncAPIText = await readFile(new URL("../../api/asyncapi.yaml", import.meta.url), "utf8");
const apiDocsText = await readFile(new URL("../../ai-docs/api-docs.md", import.meta.url), "utf8");
const docsBuildText = await readFile(new URL("./build.mjs", import.meta.url), "utf8");
const clientGameConfigText = await readFile(new URL("../../client-config/game-config.json", import.meta.url), "utf8");
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

assertSchemaContains(openAPIText, "OpaqueRoomID", ['pattern: "^room_[A-Za-z0-9_-]{22}$"']);
assertSchemaContains(openAPIText, "OpaquePlayerID", ['pattern: "^player_[A-Za-z0-9_-]{22}$"']);
assertSchemaContains(openAPIText, "PlayerSessionToken", ['pattern: "^[A-Za-z0-9_-]{43}$"']);
assertSchemaContains(openAPIText, "PlayerSessionToken", ["sessionToken", "tokenized `webSocketPath`", "Failed upgrade"]);
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
assertNamedBlockContains(asyncAPIText, "    playerSessionToken:", ["type: httpApiKey", "name: token", "in: query"]);
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
assert(hasLine(asyncAPIInfo, "  version: 0.4.0"), "api/asyncapi.yaml must publish version 0.4.0");
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
const asyncAPIOperations = extractYAMLNamedBlock(asyncAPIText, "operations:");
const receiveReadyOperation = extractYAMLNamedBlock(asyncAPIOperations, "  receiveReady:");
for (const marker of ["full participant list", "human session만 Ready ACK"]) {
  assert(receiveReadyOperation.includes(marker), `receiveReady must document ${marker}`);
}
const sendReadyAckOperation = extractYAMLNamedBlock(asyncAPIOperations, "  sendReadyAck:");
assert(sendReadyAckOperation.includes("Bot은 ACK를 보내지 않습니다"), "sendReadyAck must be human-only");
const asyncAPIComponents = extractYAMLNamedBlock(asyncAPIText, "components:");
const asyncAPIMessages = extractYAMLNamedBlock(asyncAPIComponents, "  messages:");
const readyEventMessage = extractYAMLNamedBlock(asyncAPIMessages, "    ReadyEventMessage:");
assert(readyEventMessage.includes("full participant assignment"), "ReadyEventMessage must describe full participants");
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
for (const marker of ["optional `gameMode`", "participant capacity", "human session", "raw body가 1024 bytes"]) {
  assert(apiDocsText.includes(marker), `ai-docs/api-docs.md must document ${marker}`);
}
assert(docsBuildText.includes("persistAuthorization: false"), "Swagger UI must not persist debug authorization");
for (const marker of ["pre-start", "failed upgrade", "in-flight reservation", "malformed", "secret-bearing surface", "30초 heartbeat", "90초 deadline", "latest-only", "Reliable control", "Terminal order"]) {
  assert(docsBuildText.includes(marker), `docs UI must document ${marker}`);
}
for (const [text, name] of [[openAPIText, "api/openapi.yaml"], [asyncAPIText, "api/asyncapi.yaml"], [docsBuildText, "docs UI"]]) {
  assertNoRawSessionTokenExamples(text, name);
}

assert(clientGameConfig.version === 1, "client-config/game-config.json must use version 1");
assertOnlyKeys(clientGameConfig, ["version", "tileSize", "playerRadius", "playerTypes", "projectileRadius", "projectileTypes"], "client-config/game-config.json");
assert(clientGameConfig.tileSize === 1.2, "client-config/game-config.json must expose tileSize 1.2");
assert(clientGameConfig.playerRadius === 0.5, "client-config/game-config.json must expose playerRadius 0.5");
assert(hasValue(clientGameConfig.playerTypes, "default"), "client-config/game-config.json must expose default player type");
assert(clientGameConfig.projectileRadius === 0.3, "client-config/game-config.json must expose projectileRadius 0.3");
assert(hasValue(clientGameConfig.projectileTypes, "default"), "client-config/game-config.json must expose default projectile type");

assert(serverGameConfig.version === 1, "server-config/game-config.json must use version 1");
assert(serverGameConfig.tickRate === 30, "server-config/game-config.json must expose tickRate 30");
assert(serverGameConfig.tile?.size === 1.2, "server-config/game-config.json must expose tile.size 1.2");
assert(hasTypeRadius(serverGameConfig.player?.types, "default", 0.5), "server-config/game-config.json must expose default player radius 0.5");
assert(hasTypeValue(serverGameConfig.player?.types, "default", "hp", 100), "server-config/game-config.json must expose default player hp 100");
assert(hasTypeValue(serverGameConfig.player?.types, "default", "speed", 2), "server-config/game-config.json must expose default player speed 2");
assert(hasTypeRadius(serverGameConfig.projectile?.types, "default", 0.3), "server-config/game-config.json must expose default projectile radius 0.3");
assert(hasTypeValue(serverGameConfig.projectile?.types, "default", "damage", 10), "server-config/game-config.json must expose default projectile damage 10");
assert(hasTypeValue(serverGameConfig.projectile?.types, "default", "speed", 13), "server-config/game-config.json must expose default projectile speed 13");
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

function assertEveryJSONPlayerHasIsBot(players, name) {
  assert(Array.isArray(players) && players.length > 0, `${name} must include players`);
  for (const [index, player] of players.entries()) {
    assert(Object.hasOwn(player, "IsBot"), `${name} player ${index} is missing IsBot`);
    assert(typeof player.IsBot === "boolean", `${name} player ${index} IsBot must be boolean`);
  }
}

function validateBotIdentitySchemas() {
  assertSchemaContains(openAPIText, "Player", [
    "required: [id, team, slot, isBot]",
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

  assert(hasLine(asyncAPIText, "  version: 0.4.0"), "AsyncAPI version must be 0.4.0");
  assertSchemaContains(asyncAPIText, "ReadyPlayer", [
    "required: [Id, Team, Slot, IsBot, SpawnPosition]",
  ]);
  assertSchemaContains(asyncAPIText, "PlayerData", [
    "required: [Id, Team, Slot, IsBot, Pos, MoveDir, AttackDir, Speed, Radius, HP, PressedAttack, IsDead]",
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

function assertNoRawSessionTokenExamples(text, name) {
  assert(!/(?<![A-Za-z0-9_-])[A-Za-z0-9_-]{43}(?![A-Za-z0-9_-])/.test(text), `${name} must not contain a raw 43-character session token example`);
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
