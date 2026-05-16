# Protocol Planning

No gameplay protocol is implemented yet.

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
- authentication boundary
- room creation and join flow
- match state snapshots
- client input messages
- server tick model
- reconciliation and prediction assumptions
- versioning strategy

Do not implement protocol messages until the first vertical slice is accepted in Linear.

