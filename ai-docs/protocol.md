# Protocol Planning

No gameplay protocol is implemented yet.

## Documentation Policy

E1 REST APIs will be documented with OpenAPI 3.x and rendered through Swagger UI when an interactive page is added. E1 WebSocket message contracts will be documented with AsyncAPI.

OpenAPI may mention `ws://` or `wss://` server URLs, but AsyncAPI is the source of truth for bidirectional WebSocket message streams such as client input and server snapshot broadcasts.

See `ai-docs/api-docs.md` for the full documentation policy.

## Current Endpoint

```text
GET /health
```

Response:

```json
{
  "status": "ok",
  "service": "server-crawlstars"
}
```

## Future Planning Topics

- HTTP versus WebSocket responsibilities
- OpenAPI REST contract shape
- AsyncAPI WebSocket message contract shape
- authentication boundary
- room creation and join flow
- match state snapshots
- client input messages
- server tick model
- reconciliation and prediction assumptions
- versioning strategy

Do not implement protocol messages until the first vertical slice is accepted in Linear.
