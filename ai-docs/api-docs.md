# API Documentation Policy

## Scope

This document records the E1 decision for documenting REST APIs and WebSocket message contracts.

E1 exposes a small development contract surface so the client can inspect the server-authoritative input and snapshot flow. It is not the final production API contract.

## Decision

- REST APIs use OpenAPI 3.x.
- REST APIs should be rendered through Swagger UI when an interactive page is added.
- WebSocket message contracts use AsyncAPI.
- OpenAPI may mention `ws://` or `wss://` server URLs, but it is not the primary source of truth for bidirectional WebSocket message streams.
- Development and debug APIs must be explicitly marked as unstable and E1-only until promoted to a formal client contract.

## REST Documentation

Use OpenAPI for request/response APIs such as:

- `GET /health`
- `GET /rooms`
- `POST /rooms`
- `GET /rooms/{roomID}`
- `POST /rooms/{roomID}/players`
- `POST /rooms/{roomID}/start`

OpenAPI is the right fit for REST because each operation has a bounded request and response. Swagger UI can make these endpoints readable and manually testable.

## WebSocket Documentation

Use AsyncAPI for persistent message flows such as:

- client connects to a room/player WebSocket endpoint
- client sends input messages
- server broadcasts snapshots
- server sends structured error messages

AsyncAPI is the right fit for WebSocket because it can describe channels, messages, payload schemas, and bidirectional event streams.

## OpenAPI WebSocket Boundary

OpenAPI can describe server URLs with `ws://` or `wss://`, and it can document HTTP endpoints that create rooms or issue player IDs before a WebSocket connection.

OpenAPI should not be treated as the full WebSocket contract for E1 because Swagger UI does not provide a reliable way to exercise ongoing bidirectional gameplay streams.

## Document Locations

When implementation begins, keep source specs in the repository:

```text
api/openapi.yaml
api/asyncapi.yaml
```

When docs hosting is implemented, expose them from the running server:

```text
GET /docs/rest
GET /docs/ws
GET /docs/openapi.yaml
GET /docs/asyncapi.yaml
```

GitHub Pages can be considered later as a static mirror. It is not the primary E1 hosting path.

## Development And Debug API Marking

E1-only APIs must be marked in the docs with:

- `x-stability: e1-debug`
- a summary or description containing `E1 debug API`
- notes explaining whether Unity client code may depend on the endpoint

Debug APIs should not be silently promoted to stable client contracts. Promotion requires a follow-up Linear issue and a docs update.

## Validation

The first implementation issue that adds spec files should also add validation for them. At minimum:

- OpenAPI spec parses successfully.
- AsyncAPI spec parses successfully.
- Documentation paths are mentioned in `ai-docs/protocol.md`.
