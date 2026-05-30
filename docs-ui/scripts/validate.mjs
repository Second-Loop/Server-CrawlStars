import { readFile } from "node:fs/promises";
import YAML from "yaml";

const openAPIText = await readFile(new URL("../../api/openapi.yaml", import.meta.url), "utf8");
const asyncAPIText = await readFile(new URL("../../api/asyncapi.yaml", import.meta.url), "utf8");

const openAPI = YAML.parse(openAPIText);
const asyncAPI = YAML.parse(asyncAPIText);

const requiredRESTPaths = [
  "/health",
  "/rooms",
  "/rooms/{roomID}",
  "/rooms/{roomID}/players",
  "/rooms/{roomID}/start",
];
const requiredWebSocketFields = [
  "MoveDir",
  "AttackDir",
  "PressedAttack",
  "Type",
  "Snapshot",
  "Error",
  "Id",
  "OwnerId",
];

assert(openAPI?.openapi === "3.1.0", "api/openapi.yaml must use OpenAPI 3.1.0");
assert(openAPI?.["x-stability"] === "e1-debug", "api/openapi.yaml must mark x-stability: e1-debug");
for (const path of requiredRESTPaths) {
  assert(openAPI.paths?.[path], `api/openapi.yaml is missing ${path}`);
}
assert(openAPI.components?.schemas?.ErrorResponse, "api/openapi.yaml is missing ErrorResponse schema");
assert(openAPIText.includes("room_full"), "api/openapi.yaml must document room_full");

assert(asyncAPI?.asyncapi === "3.0.0", "api/asyncapi.yaml must use AsyncAPI 3.0.0");
assert(asyncAPI?.["x-stability"] === "e1-debug", "api/asyncapi.yaml must mark x-stability: e1-debug");
assert(asyncAPI.channels?.roomPlayer?.address === "/rooms/{roomID}/players/{playerID}", "api/asyncapi.yaml must document room player channel");
for (const field of requiredWebSocketFields) {
  assert(asyncAPIText.includes(field), `api/asyncapi.yaml is missing ${field}`);
}
assert(asyncAPIText.includes("invalid_input"), "api/asyncapi.yaml must document invalid_input");

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}
