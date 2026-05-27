# API Documentation Policy

## Scope

이 문서는 REST API와 WebSocket message contract를 문서화하는 E1 결정을 기록합니다.

E1은 client가 server-authoritative input과 snapshot 흐름을 확인할 수 있는 작은 development contract surface를 노출합니다. 이는 최종 production API contract가 아닙니다.

## Decision

- REST API는 OpenAPI 3.x를 사용합니다.
- Interactive page를 추가할 때 REST API는 Swagger UI로 render합니다.
- WebSocket message contract는 AsyncAPI를 사용합니다.
- OpenAPI는 `ws://` 또는 `wss://` server URL을 언급할 수 있지만, bidirectional WebSocket message stream의 primary source of truth로 취급하지 않습니다.
- Development/debug API는 formal client contract로 승격되기 전까지 unstable 및 E1-only로 명시합니다.

## REST Documentation

다음처럼 request/response가 명확한 API에는 OpenAPI를 사용합니다.

- `GET /health`
- `GET /rooms`
- `POST /rooms`
- `GET /rooms/{roomID}`
- `POST /rooms/{roomID}/players`
- `POST /rooms/{roomID}/start`

REST는 각 operation이 bounded request와 response를 가지므로 OpenAPI가 적합합니다. Swagger UI는 endpoint를 읽기 쉽고 수동 테스트 가능하게 만듭니다.

## WebSocket Documentation

다음처럼 지속적인 message flow에는 AsyncAPI를 사용합니다.

- client가 room/player WebSocket endpoint에 connect
- client가 input message 전송
- server가 snapshot broadcast
- server가 structured error message 전송

AsyncAPI는 channel, message, payload schema, bidirectional event stream을 설명할 수 있으므로 WebSocket에 적합합니다.

## OpenAPI WebSocket Boundary

OpenAPI는 `ws://` 또는 `wss://` server URL을 설명할 수 있고, WebSocket connection 전에 room을 만들거나 player ID를 발급하는 HTTP endpoint를 문서화할 수 있습니다.

하지만 Swagger UI가 지속적인 bidirectional gameplay stream을 안정적으로 실행하는 데 적합하지 않으므로, OpenAPI를 E1의 전체 WebSocket contract로 취급하지 않습니다.

## Document Locations

Implementation이 시작되면 source spec은 repository에 둡니다.

```text
api/openapi.yaml
api/asyncapi.yaml
```

Docs hosting을 구현할 때 running server에서 다음 path로 노출합니다.

```text
GET /docs/rest
GET /docs/ws
GET /docs/openapi.yaml
GET /docs/asyncapi.yaml
```

GitHub Pages는 나중에 static mirror가 필요할 때 검토합니다. E1의 primary hosting path는 아닙니다.

## Development And Debug API Marking

E1-only API는 docs에 다음을 표시해야 합니다.

- `x-stability: e1-debug`
- summary 또는 description에 `E1 debug API` 포함
- Unity client code가 해당 endpoint에 의존해도 되는지 설명하는 note

Debug API는 암묵적으로 stable client contract로 승격하지 않습니다. 승격에는 follow-up Linear issue와 docs update가 필요합니다.

## Validation

처음으로 spec file을 추가하는 implementation issue는 해당 spec validation도 함께 추가해야 합니다. 최소 기준:

- OpenAPI spec이 정상 parse됩니다.
- AsyncAPI spec이 정상 parse됩니다.
- Documentation path가 `ai-docs/protocol.md`에 언급되어 있습니다.
