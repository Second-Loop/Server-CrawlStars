import { readFile } from "node:fs/promises";

const openAPIText = await readFile(new URL("../../api/openapi.yaml", import.meta.url), "utf8");
const asyncAPIText = await readFile(new URL("../../api/asyncapi.yaml", import.meta.url), "utf8");
const clientGameConfigText = await readFile(new URL("../../client-config/game-config.json", import.meta.url), "utf8");
const clientGameConfig = JSON.parse(clientGameConfigText);

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
assert(openAPIText.includes("room_full"), "api/openapi.yaml must document room_full");
assertNoBacktickStartedPlainScalars(openAPIText, "api/openapi.yaml");
assertNoColonSpacePlainScalars(openAPIText, "api/openapi.yaml");

assert(hasLine(asyncAPIText, "asyncapi: 3.0.0"), "api/asyncapi.yaml must use AsyncAPI 3.0.0");
assert(hasLine(asyncAPIText, "x-stability: e1-debug"), "api/asyncapi.yaml must mark x-stability: e1-debug");
assert(hasLine(asyncAPIText, "    address: /rooms/{roomID}/players/{playerID}"), "api/asyncapi.yaml must document room player channel");
for (const field of requiredWebSocketFields) {
	assert(asyncAPIText.includes(field), `api/asyncapi.yaml is missing ${field}`);
}
assert(asyncAPIText.includes("invalid_input"), "api/asyncapi.yaml must document invalid_input");
assertNoBacktickStartedPlainScalars(asyncAPIText, "api/asyncapi.yaml");
assertNoColonSpacePlainScalars(asyncAPIText, "api/asyncapi.yaml");

assert(clientGameConfig.version === 1, "client-config/game-config.json must use version 1");
assert(clientGameConfig.tickRate === 30, "client-config/game-config.json must expose tickRate 30");
assert(clientGameConfig.tile?.size === 1.2, "client-config/game-config.json must expose tile.size 1.2");
assert(hasTypeRadius(clientGameConfig.player?.types, "default", 0.5), "client-config/game-config.json must expose default player radius 0.5");
assert(hasTypeValue(clientGameConfig.player?.types, "default", "hp", 100), "client-config/game-config.json must expose default player hp 100");
assert(hasTypeValue(clientGameConfig.player?.types, "default", "speed", 2), "client-config/game-config.json must expose default player speed 2");
assert(hasTypeRadius(clientGameConfig.projectile?.types, "default", 0.3), "client-config/game-config.json must expose default projectile radius 0.3");
assert(hasTypeValue(clientGameConfig.projectile?.types, "default", "damage", 10), "client-config/game-config.json must expose default projectile damage 10");
assert(hasTypeValue(clientGameConfig.projectile?.types, "default", "speed", 13), "client-config/game-config.json must expose default projectile speed 13");
assert(clientGameConfig.map?.width === 20, "client-config/game-config.json must expose the runtime map width");
assert(clientGameConfig.map?.height === 20, "client-config/game-config.json must expose the runtime map height");
assert(clientGameConfig.map?.maxPlayers === 6, "client-config/game-config.json must expose map maxPlayers 6");

function hasLine(text, want) {
	return text.split(/\r?\n/).some((line) => line === want);
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

function assert(condition, message) {
	if (!condition) {
		throw new Error(message);
	}
}
