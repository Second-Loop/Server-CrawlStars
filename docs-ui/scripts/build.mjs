import { mkdir, readFile, rm, writeFile } from "node:fs/promises";

const root = new URL("../../", import.meta.url);
const openAPIText = await readFile(new URL("api/openapi.yaml", root), "utf8");
const asyncAPIText = await readFile(new URL("api/asyncapi.yaml", root), "utf8");

const embedAPI = new URL("internal/docs/api/", root);
const embedStatic = new URL("internal/docs/static/", root);

await rm(embedAPI, { recursive: true, force: true });
await rm(embedStatic, { recursive: true, force: true });
await mkdir(embedAPI, { recursive: true });
await mkdir(embedStatic, { recursive: true });

await writeFile(new URL("openapi.yaml", embedAPI), openAPIText);
await writeFile(new URL("asyncapi.yaml", embedAPI), asyncAPIText);
await writeFile(new URL("openapi.html", embedStatic), renderOpenAPI(openAPIText));
await writeFile(new URL("asyncapi.html", embedStatic), renderAsyncAPI(asyncAPIText));

function renderOpenAPI(specText) {
  assertSpecPresent(specText, "openapi: 3.1.0", "OpenAPI");

  return `<!doctype html>
<html lang="ko">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <link rel="icon" href="data:," />
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" />
    <title>OpenAPI - Server Crawl Stars</title>
    <style>
      body { margin: 0; background: #f7f9fc; }
      .topbar { display: none; }
      .swagger-ui .info { margin: 32px 0 18px; }
      .swagger-ui .scheme-container { box-shadow: none; border: 1px solid #d8dde8; border-radius: 8px; }
    </style>
  </head>
  <body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
    <script>
      window.ui = SwaggerUIBundle({
        url: "/openapi.yaml",
        dom_id: "#swagger-ui",
        deepLinking: true,
        displayRequestDuration: true,
        tryItOutEnabled: true,
        persistAuthorization: false,
      });
    </script>
  </body>
</html>
`;
}

function renderAsyncAPI(specText) {
  const channelAddress = extractLineValue(specText, "    address:");
  const redactedWebSocketPath = `${channelAddress}?token=<player-session-token>`;
  const schemas = parseAsyncAPISchemas(specText);

  return page({
    title: "AsyncAPI",
    eyebrow: "WebSocket API",
    description: "E2 개발용 WebSocket 계약입니다. Player session 인증, Ready 이벤트, heartbeat, gameplay snapshot 전달 흐름을 확인합니다.",
    rawPath: "/asyncapi.yaml",
    content: `
      <section class="panel">
        <h2>연결 채널</h2>
        <article class="operation">
          <div class="method">WS</div>
          <div>
            <h3>${escapeHTML(redactedWebSocketPath)}</h3>
            <p>REST에서 받은 room ID, player ID, <code>sessionToken</code>으로 연결합니다. <code>token</code> query는 정확히 한 번 전달하며 다른 일반 query key는 허용합니다.</p>
          </div>
        </article>
      </section>
      <section class="panel">
        <h2>상태 흐름</h2>
        <div class="grid">
          <article>
            <h3>1. join</h3>
            <p><code>POST /matchmaking/join</code>은 optional <code>gameMode</code>로 <code>duel_1v1</code>, <code>solo</code>, <code>team</code>을 선택하며 생략하거나 빈 문자열이면 duel을 사용합니다. 응답은 <code>{ gameMode, room, player, sessionToken, webSocketPath }</code>이고 top-level <code>gameMode</code>와 <code>room.gameMode</code>는 같습니다. 같은 raw secret이 <code>sessionToken</code>과 tokenized <code>webSocketPath</code>에 나타나며, path를 client 내부에서만 사용해 연결합니다.</p>
          </article>
          <article>
            <h3>2. Ready</h3>
            <p>선택 mode의 participant capacity가 채워지고 연결된 human WebSocket session만 attach quorum을 만족하면 human client가 <code>Type: Ready</code>를 받습니다. Payload에는 bot을 포함한 full participant의 <code>IsBot</code>과 <code>SpawnPosition</code>이 들어갑니다. <code>duel_1v1</code> capacity는 2명, <code>solo</code>와 <code>team</code>은 6명입니다.</p>
          </article>
          <article>
            <h3>3. ready</h3>
            <p>각 human client는 준비가 끝나면 <code>{"Type":"ready"}</code>를 보냅니다. Bot은 WebSocket session과 ready ACK가 없습니다.</p>
          </article>
          <article>
            <h3>4. starting</h3>
            <p>연결된 human session이 모두 ready면 countdown 시작 신호로 <code>Snapshot.status: starting</code>과 <code>Snapshot.countdown: 5</code>를 1번 받습니다.</p>
          </article>
          <article>
            <h3>5. started</h3>
            <p>Client는 fake timer를 표시하고, server는 5초를 내부에서 센 뒤 <code>Snapshot.status: started</code>와 gameplay snapshot을 보냅니다.</p>
          </article>
        </div>
      </section>
      <section class="panel">
        <h2>전달과 heartbeat</h2>
        <div class="grid">
          <article>
            <h3>30초 / 90초</h3>
            <p>Server는 연결마다 30초 heartbeat를 실행하고 각 Ping에 90초 deadline을 둡니다. Ping error/timeout은 read/write failure와 같은 close-once 경로로 정리합니다.</p>
          </article>
          <article>
            <h3>Snapshot coalescing</h3>
            <p>일반 gameplay snapshot만 client별 latest-only slot에서 합칩니다. 느린 client가 room tick이나 다른 client를 막지 않습니다.</p>
          </article>
          <article>
            <h3>Reliable control</h3>
            <p>Ready, starting, started, error는 순서를 보존하며 버리지 않습니다. Queue overflow와 write failure는 해당 session을 닫습니다.</p>
          </article>
          <article>
            <h3>Terminal order</h3>
            <p>종료 시에는 <code>terminal snapshot -&gt; GameEnd -&gt; close</code> 순서를 socket 종료 전에 보장합니다.</p>
          </article>
        </div>
      </section>
      <section class="panel">
        <h2>연결 보안</h2>
        <div class="grid">
          <article>
            <h3>Session token</h3>
            <p>Token은 일회용이 아니며 room/player session이 남아 있을 때 재사용합니다. Unmatched disconnect는 room-owned 10초 fill deadline과 credential을 유지합니다. matched/loading/starting disconnect는 pre-start cancel로 room을 삭제하고, failed upgrade는 같은 경로로 재시도할 수 있습니다.</p>
          </article>
          <article>
            <h3>실패 순서</h3>
            <p>Handshake는 room 404, player 404, token 401, live connection 또는 in-flight reservation 409 순서로 거부합니다. 다른 query pair가 malformed여도 401입니다.</p>
          </article>
          <article>
            <h3>Logging</h3>
            <p><code>sessionToken</code>, tokenized <code>webSocketPath</code>, inbound query는 모두 secret-bearing surface입니다. Raw token과 전체 query 문자열은 log에 남기지 않고 예시는 항상 가립니다.</p>
          </article>
          <article>
            <h3>Public payload</h3>
            <p>공개 Room/Player, Ready, Snapshot, GameEnd payload에는 raw token이나 digest가 없습니다.</p>
          </article>
        </div>
      </section>
      <section class="panel">
        <h2>메시지</h2>
        <div class="grid">
          <article>
            <h3>Input</h3>
            <p><code>MoveDir</code>, <code>AttackDir</code>, <code>PressedAttack</code>를 보냅니다. Gameplay input에는 <code>Type</code>을 넣지 않습니다.</p>
          </article>
          <article>
            <h3>Ready Event</h3>
            <p>Server가 human session에 <code>Type: Ready</code>, <code>Map</code>, bot을 포함한 <code>Players[].IsBot</code>과 <code>Players[].SpawnPosition</code>을 보냅니다.</p>
          </article>
          <article>
            <h3>Ready ACK</h3>
            <p>Client는 map load/render 준비가 끝나면 <code>Type: ready</code>를 보냅니다.</p>
          </article>
          <article>
            <h3>Snapshot</h3>
            <p><code>Snapshot.status</code>는 lowercase이고, gameplay field인 <code>Tick</code>, <code>Players</code>, <code>Projectiles</code>는 기존 PascalCase를 유지합니다.</p>
          </article>
          <article>
            <h3>Error</h3>
            <p><code>Type: error</code>, <code>Error.code: invalid_input</code></p>
          </article>
        </div>
      </section>
      <section class="panel">
        <h2>예시</h2>
        <div class="grid">
          <article>
            <h3>Ready Event</h3>
            <pre><code>{
  "Type": "Ready",
  "Map": {
    "width": 5,
    "height": 5,
    "index": 0,
    "maxPlayers": 6,
    "tileSize": 1.2,
    "map": [[1, 1, 1, 1, 1]]
  },
  "Players": [
    {
      "Id": "player_VuTsRqPoNmLkJiHgFeDcBa",
      "Team": "red",
      "Slot": 0,
      "IsBot": false,
      "SpawnPosition": { "x": -1.2, "y": 1.2 }
    },
    {
      "Id": "player_AbCdEfGhIjKlMnOpQrStUv",
      "Team": "blue",
      "Slot": 0,
      "IsBot": true,
      "SpawnPosition": { "x": 1.2, "y": -1.2 }
    }
  ]
}</code></pre>
          </article>
          <article>
            <h3>Ready ACK</h3>
            <pre><code>{
  "Type": "ready"
}</code></pre>
          </article>
          <article>
            <h3>Starting Signal</h3>
            <pre><code>{
  "Type": "snapshot",
  "Snapshot": {
    "status": "starting",
    "countdown": 5,
    "Tick": 0,
    "Players": null,
    "Projectiles": null
  }
}</code></pre>
          </article>
          <article>
            <h3>Gameplay</h3>
            <pre><code>{
  "Type": "snapshot",
  "Snapshot": {
    "status": "started",
    "Tick": 1,
    "Players": [
      {
        "Id": "player_VuTsRqPoNmLkJiHgFeDcBa",
        "Team": "red",
        "Slot": 0,
        "IsBot": false,
        "Pos": { "x": -1.2, "y": 1.2 },
        "MoveDir": { "x": 0, "y": 0 },
        "AttackDir": { "x": 0, "y": 0 },
        "Speed": 2,
        "Radius": 0.5,
        "HP": 100,
        "PressedAttack": false,
        "IsDead": false
      },
      {
        "Id": "player_AbCdEfGhIjKlMnOpQrStUv",
        "Team": "blue",
        "Slot": 0,
        "IsBot": true,
        "Pos": { "x": 1.2, "y": -1.2 },
        "MoveDir": { "x": -1, "y": 0 },
        "AttackDir": { "x": -1, "y": 0 },
        "Speed": 2,
        "Radius": 0.5,
        "HP": 100,
        "PressedAttack": true,
        "IsDead": false
      }
    ],
    "Projectiles": null
  }
}</code></pre>
          </article>
        </div>
      </section>
      <section class="panel">
        <h2>스키마</h2>
        <p>${schemas.map((schema) => `<code>${escapeHTML(schema)}</code>`).join(" ")}</p>
      </section>
    `,
  });
}

function parseAsyncAPISchemas(specText) {
  const schemas = [];
  let inSchemas = false;

  for (const line of specText.split(/\r?\n/)) {
    if (line === "  schemas:") {
      inSchemas = true;
      continue;
    }
    if (!inSchemas) {
      continue;
    }

    const schemaMatch = /^    ([A-Za-z][A-Za-z0-9]*):$/.exec(line);
    if (schemaMatch) {
      schemas.push(schemaMatch[1]);
    }
  }

  return schemas;
}

function assertSpecPresent(specText, marker, name) {
  if (!specText.includes(marker)) {
    throw new Error(`${name} spec is missing ${marker}`);
  }
}

function extractLineValue(text, prefix) {
  const line = text.split(/\r?\n/).find((candidate) => candidate.startsWith(prefix));
  return line ? line.slice(prefix.length).trim() : "";
}

function page({ title, eyebrow, description, rawPath, content }) {
  return `<!doctype html>
<html lang="ko">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <link rel="icon" href="data:," />
    <title>${escapeHTML(title)} - Server Crawl Stars</title>
    <style>
      :root { color-scheme: light; --ink: #1d2433; --muted: #5d6678; --line: #d8dde8; --paper: #f7f9fc; --accent: #1b6f8f; --accent-bg: #e7f5fa; }
      * { box-sizing: border-box; }
      body { margin: 0; font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; color: var(--ink); background: var(--paper); }
      header, main { width: min(1040px, calc(100% - 32px)); margin: 0 auto; }
      header { padding: 40px 0 22px; }
      main { padding-bottom: 48px; }
      .eyebrow { color: var(--accent); font-weight: 700; text-transform: uppercase; font-size: 12px; letter-spacing: .08em; }
      h1 { margin: 8px 0 10px; font-size: 34px; line-height: 1.15; letter-spacing: 0; }
      h2 { margin: 0 0 18px; font-size: 20px; letter-spacing: 0; }
      h3 { margin: 0 0 6px; font-size: 16px; letter-spacing: 0; }
      p { color: var(--muted); line-height: 1.6; margin: 0; }
      a { color: var(--accent); font-weight: 700; text-decoration-thickness: 1px; text-underline-offset: 3px; }
      code { background: var(--accent-bg); border: 1px solid #c6e6f0; border-radius: 4px; padding: 2px 6px; color: #17475a; }
      pre { margin: 0; overflow: auto; border: 1px solid var(--line); border-radius: 8px; background: #f8fbfd; padding: 12px; }
      pre code { display: block; border: 0; background: transparent; padding: 0; white-space: pre; color: #263242; }
      .actions { display: flex; gap: 12px; flex-wrap: wrap; margin-top: 20px; }
      .button { display: inline-flex; align-items: center; min-height: 38px; border: 1px solid var(--line); border-radius: 6px; padding: 0 12px; background: white; }
      .panel { background: white; border: 1px solid var(--line); border-radius: 8px; padding: 22px; margin-top: 16px; }
      .operation-list { display: grid; gap: 12px; }
      .operation { display: grid; grid-template-columns: 84px 1fr; gap: 14px; align-items: start; border: 1px solid var(--line); border-radius: 8px; padding: 14px; }
      .method { display: inline-flex; justify-content: center; align-items: center; min-height: 30px; border-radius: 5px; background: var(--accent-bg); color: var(--accent); font-weight: 800; }
      .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 12px; }
      .grid article { border: 1px solid var(--line); border-radius: 8px; padding: 14px; }
      small { color: var(--muted); }
      @media (max-width: 620px) { h1 { font-size: 28px; } .operation { grid-template-columns: 1fr; } .method { width: 84px; } }
    </style>
  </head>
  <body>
    <header>
      <div class="eyebrow">${escapeHTML(eyebrow)}</div>
      <h1>${escapeHTML(title)}</h1>
      <p>${escapeHTML(description)}</p>
      <div class="actions">
        <a class="button" href="${escapeHTML(rawPath)}">Raw spec</a>
        <a class="button" href="/openapi">OpenAPI</a>
        <a class="button" href="/asyncapi">AsyncAPI</a>
      </div>
    </header>
    <main>${content}</main>
  </body>
</html>
`;
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}
