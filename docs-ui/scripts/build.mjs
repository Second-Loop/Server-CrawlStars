import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import YAML from "yaml";

const root = new URL("../../", import.meta.url);
const openAPIText = await readFile(new URL("api/openapi.yaml", root), "utf8");
const asyncAPIText = await readFile(new URL("api/asyncapi.yaml", root), "utf8");
const openAPI = YAML.parse(openAPIText);
const asyncAPI = YAML.parse(asyncAPIText);

const embedAPI = new URL("internal/docs/api/", root);
const embedStatic = new URL("internal/docs/static/", root);

await rm(embedAPI, { recursive: true, force: true });
await rm(embedStatic, { recursive: true, force: true });
await mkdir(embedAPI, { recursive: true });
await mkdir(embedStatic, { recursive: true });

await writeFile(new URL("openapi.yaml", embedAPI), openAPIText);
await writeFile(new URL("asyncapi.yaml", embedAPI), asyncAPIText);
await writeFile(new URL("openapi.html", embedStatic), renderOpenAPI(openAPI));
await writeFile(new URL("asyncapi.html", embedStatic), renderAsyncAPI(asyncAPI));

function renderOpenAPI(spec) {
  const operations = [];
  for (const [path, methods] of Object.entries(spec.paths ?? {})) {
    for (const [method, operation] of Object.entries(methods)) {
      operations.push({
        method: method.toUpperCase(),
        path,
        summary: operation.summary ?? "",
        responses: Object.keys(operation.responses ?? {}),
      });
    }
  }

  return page({
    title: "OpenAPI",
    eyebrow: "REST API",
    description: spec.info?.description ?? "Development REST API.",
    rawPath: "/openapi.yaml",
    content: `
      <section class="panel">
        <h2>REST endpoints</h2>
        <div class="operation-list">
          ${operations.map((op) => `
            <article class="operation">
              <div class="method">${escapeHTML(op.method)}</div>
              <div>
                <h3>${escapeHTML(op.path)}</h3>
                <p>${escapeHTML(op.summary)}</p>
                <small>Responses: ${escapeHTML(op.responses.join(", "))}</small>
              </div>
            </article>
          `).join("")}
        </div>
      </section>
      <section class="panel">
        <h2>Error codes</h2>
        <p><code>room_not_found</code>, <code>room_cap_reached</code>, <code>room_full</code>, <code>room_has_no_players</code>, <code>method_not_allowed</code>, <code>not_found</code></p>
      </section>
    `,
  });
}

function renderAsyncAPI(spec) {
  const channel = spec.channels?.roomPlayer;
  const schemas = Object.keys(spec.components?.schemas ?? {});

  return page({
    title: "AsyncAPI",
    eyebrow: "WebSocket API",
    description: "E1 debug WebSocket contract for room input and snapshot streaming.",
    rawPath: "/asyncapi.yaml",
    content: `
      <section class="panel">
        <h2>Channel</h2>
        <article class="operation">
          <div class="method">WS</div>
          <div>
            <h3>${escapeHTML(channel?.address ?? "")}</h3>
            <p>Connect with a REST-issued room ID and player ID.</p>
          </div>
        </article>
      </section>
      <section class="panel">
        <h2>Messages</h2>
        <div class="grid">
          <article>
            <h3>Input</h3>
            <p><code>MoveDir</code>, <code>AttackDir</code>, <code>PressedAttack</code></p>
          </article>
          <article>
            <h3>Snapshot</h3>
            <p><code>Type: snapshot</code>, <code>Snapshot</code>, <code>Players</code>, <code>Projectiles</code></p>
          </article>
          <article>
            <h3>Error</h3>
            <p><code>Type: error</code>, <code>Error.code: invalid_input</code></p>
          </article>
        </div>
      </section>
      <section class="panel">
        <h2>Schemas</h2>
        <p>${schemas.map((schema) => `<code>${escapeHTML(schema)}</code>`).join(" ")}</p>
      </section>
    `,
  });
}

function page({ title, eyebrow, description, rawPath, content }) {
  return `<!doctype html>
<html lang="en">
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
