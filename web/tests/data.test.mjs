import test from "node:test";
import assert from "node:assert/strict";
import {
  createDataClient,
  exportCSVText,
  filterRows,
  readPreferences,
} from "../src/data.ts";

function storage() {
  const map = new Map();
  return {
    getItem: (k) => map.get(k) ?? null,
    setItem: (k, v) => map.set(k, v),
    removeItem: (k) => map.delete(k),
    clear: () => map.clear(),
    get length() {
      return map.size;
    },
    key: (i) => [...map.keys()][i],
  };
}
function fixture() {
  const suffix = ".0123456789abcdef.json";
  const program = {
    id: "hackerone:example",
    entity: "program",
    program_id: "hackerone:example",
    label: "Example",
    platform: "hackerone",
    url: "https://hackerone.com/example",
    bounty: 1,
    detail_file: "detail-example" + suffix,
  };
  const asset = {
    id: "asset-1",
    entity: "asset",
    program_id: program.id,
    label: "*.example.com",
    platform: "hackerone",
    type: "WILDCARD",
    scope: "in_scope",
    conflict: 0,
  };
  const change = {
    id: 12,
    entity: "asset",
    kind: "modified",
    program_id: program.id,
    label: asset.label,
    platform: "hackerone",
    created_at: "2026-11-10T00:00:00Z",
    before_data: { scope: "in_scope" },
    after_data: { scope: "out_of_scope" },
  };
  const manifest = {
    schema: 1,
    dataset_id: "stable-namespace",
    version: "first",
    generated_at: "2026-11-10T00:00:00Z",
    files: {
      programs: "programs" + suffix,
      "assets-hackerone": "hackerone" + suffix,
      "assets-bugcrowd": "bugcrowd" + suffix,
      changes: "changes" + suffix,
      overview: "overview" + suffix,
      runs: "runs" + suffix,
    },
    config: {
      repository: "VpSanta33/SRC-Monitoring",
      workflow: "pages.yml",
      schedule: "17,47 * * * *",
      timezone: "UTC",
      enabled: { hackerone: true, bugcrowd: true },
    },
    history_limit: 5000,
    history_total: 1,
  };
  const documents = {
    "manifest.json": manifest,
    [manifest.files.programs]: [program],
    [manifest.files["assets-hackerone"]]: [asset],
    [manifest.files["assets-bugcrowd"]]: [],
    [manifest.files.changes]: [change],
    [manifest.files.overview]: {
      counts: { programs: 1, assets: 1, changes_24h: 1 },
      sources: [],
      types: [],
      trend: [],
    },
    [manifest.files.runs]: [],
    [program.detail_file]: [
      {
        ...asset,
        data: { metadata: [{ instruction: "Read the program rules" }] },
      },
    ],
  };
  const calls = [];
  const fetcher = async (raw) => {
    const u = new URL(raw);
    calls.push(u.pathname);
    assert.ok(u.pathname.startsWith("/SRC-Monitoring/data/"));
    const key = u.pathname.split("/").at(-1);
    return documents[key]
      ? new Response(JSON.stringify(documents[key]), { status: 200 })
      : new Response("missing", { status: 404 });
  };
  return { program, asset, change, manifest, documents, fetcher, calls };
}
test("Pages subpath, static search, lazy detail, and no API server", async () => {
  const f = fixture(),
    client = createDataClient(
      "https://example.github.io/SRC-Monitoring/",
      f.fetcher,
      storage,
    );
  const list = await client.request("assets?q=EXAMPLE&scope=in_scope");
  assert.equal(list.total, 1);
  assert.equal(list.items[0].program_name, "Example");
  assert.ok(!f.calls.some((p) => p.includes("detail-example")));
  const detail = await client.request("asset?id=asset-1");
  assert.equal(detail.data.metadata[0].instruction, "Read the program rules");
  assert.ok(f.calls.every((p) => !p.startsWith("/api/")));
});
test("watch and read states survive refresh and a new browser client", async () => {
  const f = fixture(),
    local = storage();
  let client = createDataClient(
    "https://example.github.io/SRC-Monitoring/",
    f.fetcher,
    () => local,
  );
  await client.request("watch", { id: f.program.id, watched: true });
  await client.request("changes/read", { ids: [12] });
  f.manifest.version = "second";
  await client.refresh();
  let view = await client.request("overview");
  assert.equal(view.counts.watched, 1);
  assert.equal(view.counts.unread, 0);
  client = createDataClient(
    "https://example.github.io/SRC-Monitoring/",
    f.fetcher,
    () => local,
  );
  assert.equal((await client.request("programs?watched=1")).total, 1);
  assert.equal((await client.request("changes?unread=1")).total, 0);
  await client.request("watch", { id: f.program.id, watched: false });
  assert.equal((await client.request("programs?watched=1")).total, 0);
  // A genuinely replaced monitoring dataset must not inherit numeric read IDs.
  f.manifest.dataset_id = "new-baseline";
  await client.refresh();
  assert.equal((await client.request("overview")).counts.unread, 1);
});
test("unavailable or full local storage reports failure instead of false success", async () => {
  const f = fixture(),
    client = createDataClient(
      "https://example.github.io/SRC-Monitoring/",
      f.fetcher,
      () => undefined,
    );
  await assert.rejects(
    client.request("watch", { id: f.program.id, watched: true }),
    /本地存储/,
  );
  assert.deepEqual(
    readPreferences(
      {
        getItem() {
          throw Error("blocked");
        },
      },
      "k",
    ),
    { watched: [], read: [] },
  );
  const full = createDataClient(
    "https://example.github.io/SRC-Monitoring/",
    f.fetcher,
    () => ({
      getItem: () => null,
      setItem() {
        throw Error("quota");
      },
    }),
  );
  await assert.rejects(full.request("changes/read", { ids: [12] }), /未保存/);
});
test("literal filters and pagination/CSV export preserve requested scope", async () => {
  const f = fixture();
  assert.equal(
    filterRows([f.asset], "assets", new URLSearchParams("q=%25"), {
      watched: [],
      read: [],
    }).length,
    0,
  );
  assert.equal(
    filterRows([f.asset], "assets", new URLSearchParams("scope=out_of_scope"), {
      watched: [],
      read: [],
    }).length,
    0,
  );
  const client = createDataClient(
    "https://example.github.io/SRC-Monitoring/",
    f.fetcher,
    storage,
  );
  assert.equal((await client.request("assets?page=2&limit=1")).items.length, 0);
  assert.equal((await client.request("assets?all=1")).items.length, 1);
  const csv = exportCSVText("assets", [
    { ...f.asset, label: ' =SUM(1,2)\n"quoted"' },
  ]);
  assert.ok(csv.startsWith("\ufeff"));
  assert.ok(csv.includes("' =SUM"));
  assert.ok(csv.includes('""quoted""'));
});
test("missing snapshots and path traversal in a manifest fail visibly", async () => {
  const f = fixture();
  f.manifest.files.programs = "../../secret";
  const client = createDataClient(
    "https://example.github.io/SRC-Monitoring/",
    f.fetcher,
    storage,
  );
  await assert.rejects(client.request("programs"), /路径无效/);
});
