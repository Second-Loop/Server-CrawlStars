# Decisions

## ADR-0001: 최소 Go HTTP Server로 시작

Status: Accepted

Context: Gameplay system이 존재하기 전에 CI로 검증 가능한 code가 필요합니다.

Decision: 작은 Go module, `cmd/server` entrypoint, `/health`를 노출하는 `internal/health` package로 시작합니다.

Consequences:

- CI가 format, vet, tests, build를 즉시 검증할 수 있습니다.
- Gameplay architecture를 확정하지 않아도 server executable이 생깁니다.
- 향후 networking decision은 열린 상태로 남깁니다.

## ADR-0002: Symphony 차용 범위는 Workflow Rule로 제한

Status: Accepted

Context: Project는 orchestration infrastructure를 만들지 않고 issue-driven, review-gated collaboration을 원합니다.

Decision: Issue-as-source-of-truth, acceptance criteria, validation, PR review, repository workflow docs만 차용합니다. Scheduler, runner, polling daemon, web dashboard, automatic merge loop는 만들지 않습니다.

Consequences:

- Process가 명시적이고 versioned 상태로 repo에 남습니다.
- Automation은 나중에 Linear-scoped work로 정당화될 때만 추가할 수 있습니다.

## ADR-0003: 초기 Oracle VM Runtime은 systemd 기반 VM Pull CD 사용

Status: Accepted

Context: SL-6은 `main` update 이후 Go server를 위한 작은 deployment path가 필요합니다. VM에는 SSH access와 passwordless sudo가 있지만 Docker, Cloudflare Tunnel, Tailscale, nginx, caddy, required public app port가 없습니다. OCI Security List와 NSG 변경은 issue scope 밖입니다.

Decision: GitHub Actions가 linux/amd64 tarball을 build하고 workflow artifact와 GitHub Release asset을 모두 publish합니다. Oracle VM은 최신 release asset을 pull하고, `/opt/crawl-stars-server/releases/<sha>/` 아래에 install하고, `/opt/crawl-stars-server/current`를 전환하고, systemd service를 restart한 뒤 `http://127.0.0.1:8080/health`를 확인합니다.

Consequences:

- Deployment를 위해 inbound application port가 필요하지 않습니다.
- GitHub Release asset은 public repo 기준 VM pull path를 단순하게 유지합니다.
- Server process는 Docker, PM2, Kubernetes 대신 systemd로 관리합니다.
- Rollback은 `/opt/crawl-stars-server/previous`로 symlink를 되돌리고 systemd restart를 실행하는 방식입니다.

## ADR-0004: HTTPS는 Cloudflare Tunnel로 노출

Status: Accepted

Context: SL-35는 Go server를 VM 내부 private 상태로 유지하면서 public HTTPS hostname을 필요로 합니다. 현재는 OCI public inbound 변경을 피해야 하므로 direct Caddy `80/tcp`, `443/tcp` ingress는 선택하지 않습니다.

Decision: Go server를 `127.0.0.1:8080`에 유지하고 VM에서 Cloudflare Tunnel connector를 실행합니다. Cloudflare는 `api-crawlstars.tolerblanc.com`을 `http://127.0.0.1:8080`으로 route합니다. Apex `tolerblanc.com`은 local Caddy `http://127.0.0.1:8081`로 route하며, Caddy는 최소 hello response를 반환합니다. Public HTTPS는 Cloudflare edge가 소유하고, 이 tunnel-backed setup에서 Caddy는 local-only입니다.

Consequences:

- OCI public inbound는 이 경로에서 application `80/tcp` 또는 `443/tcp`를 필요로 하지 않습니다. Connector가 Cloudflare로 outbound connection을 만듭니다.
- Go server port는 VM firewall, OCI Security Lists, NSG에 열면 안 됩니다.
- Go server가 WebSocket endpoint를 구현하면 WebSocket traffic도 같은 Cloudflare Tunnel hostname을 사용할 수 있습니다.
- Caddy는 systemd로 실행되지만 apex hello page를 위해 `127.0.0.1:8081`에서만 listen합니다.

## ADR-0005: REST는 OpenAPI로, WebSocket Message는 AsyncAPI로 문서화

Status: Accepted

Context: E1에는 room lifecycle, client input, server snapshot flow를 위한 작은 contract surface가 필요합니다. REST endpoint는 읽고 수동 호출하기 쉬워야 하지만, WebSocket gameplay traffic은 Swagger UI가 잘 모델링하지 못하는 bidirectional stream입니다.

Decision: REST API는 OpenAPI 3.x를 사용하고, interactive REST page를 추가할 때 Swagger UI를 사용합니다. WebSocket channel과 message payload는 AsyncAPI를 사용합니다. OpenAPI는 `ws://` 또는 `wss://` server URL을 참조할 수 있지만, WebSocket input과 snapshot stream의 source of truth는 AsyncAPI입니다.

Consequences:

- REST와 WebSocket contract는 필요한 경우 schema vocabulary를 공유하면서도 독립적으로 발전할 수 있습니다.
- E1 debug API는 승격 전까지 unstable 및 E1-only로 명확히 표시해야 합니다.
- 처음 spec file을 추가하는 implementation issue는 OpenAPI와 AsyncAPI document validation을 함께 추가해야 합니다.
- 선호되는 hosted path는 `/docs/rest`, `/docs/ws`, `/docs/openapi.yaml`, `/docs/asyncapi.yaml`입니다.

## ADR-0006: Simulation Core는 Transport-Independent Step Contract로 시작

Status: Accepted

Context: E1 server work는 REST/WebSocket contract surface를 열기 전에 server-authoritative core loop skeleton을 unit test로 고정해야 합니다. SL-38은 room lifecycle, WebSocket, matching 없이 domain model과 `Step(inputs) -> Snapshot` 경계를 먼저 정의합니다.

Decision: `internal/simulation` package에 최소 domain vocabulary와 `State.Step(inputs []InputCommand) Snapshot` 계약을 둡니다. 이 package는 HTTP, WebSocket, room manager, matching queue를 import하지 않습니다. SL-38에서는 tick 증가와 snapshot 생성만 고정하고, movement/collision, attack skeleton, REST room lifecycle, WebSocket runner는 후속 E1 하위 티켓에서 같은 계약 위에 얹습니다.

Consequences:

- Core simulation은 WebSocket 없이 Go unit test로 직접 검증할 수 있습니다.
- Red 1명 + blue 1명 구성은 테스트하되, team slot model은 한 team당 여러 player를 막지 않습니다.
- Network handler는 후속 티켓에서 `Step`을 호출하는 adapter가 되어야 하며, simulation package가 transport detail을 알면 안 됩니다.
