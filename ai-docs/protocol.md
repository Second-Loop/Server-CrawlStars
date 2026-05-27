# Protocol Planning

아직 gameplay protocol은 구현되어 있지 않습니다.

## Documentation Policy

E1 REST API는 OpenAPI 3.x로 문서화하고, interactive page를 추가할 때 Swagger UI로 render합니다. E1 WebSocket message contract는 AsyncAPI로 문서화합니다.

OpenAPI는 `ws://` 또는 `wss://` server URL을 언급할 수 있지만, client input과 server snapshot broadcast 같은 bidirectional WebSocket message stream의 source of truth는 AsyncAPI입니다.

전체 documentation policy는 `ai-docs/api-docs.md`를 참고합니다.

## 현재 Endpoint

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

## 향후 계획 주제

- HTTP와 WebSocket 책임 분리
- OpenAPI REST contract shape
- AsyncAPI WebSocket message contract shape
- authentication boundary
- room 생성 및 join flow
- match state snapshot
- client input message
- server tick model
- reconciliation 및 prediction 가정
- versioning strategy

첫 vertical slice가 Linear에서 승인되기 전에는 protocol message를 구현하지 않습니다.
