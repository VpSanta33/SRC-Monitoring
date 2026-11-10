import type { Entity, Manifest, Overview, Row, List } from "./types.ts";

type Preferences = { watched: string[]; read: number[] };
const EMPTY: Preferences = { watched: [], read: [] };
export function readPreferences(
  storage: Storage | undefined,
  key: string,
): Preferences {
  try {
    const value = JSON.parse(storage?.getItem(key) || "{}");
    return {
      watched: Array.isArray(value.watched)
        ? value.watched.filter((v: unknown) => typeof v === "string")
        : [],
      read: Array.isArray(value.read)
        ? value.read.filter(
            (v: unknown) => Number.isSafeInteger(v) && Number(v) > 0,
          )
        : [],
    };
  } catch {
    return { ...EMPTY };
  }
}
export function filterRows(
  rows: Row[],
  entity: Entity,
  params: URLSearchParams,
  prefs: Preferences,
): Row[] {
  const watched = new Set(prefs.watched),
    read = new Set(prefs.read),
    q = (params.get("q") || "").toLocaleLowerCase();
  return rows
    .map((row) => ({
      ...row,
      watched: watched.has(String(row.id)) ? 1 : 0,
      program_watched: watched.has(row.program_id) ? 1 : 0,
      seen: read.has(Number(row.id)) ? 1 : 0,
    }))
    .filter(
      (row) =>
        (!params.get("platform") || row.platform === params.get("platform")) &&
        (!params.get("program_id") ||
          row.program_id === params.get("program_id")) &&
        (!q ||
          row.label.toLocaleLowerCase().includes(q) ||
          (row.program_name || "").toLocaleLowerCase().includes(q)) &&
        (params.get("watched") !== "1" || watched.has(row.program_id)) &&
        (entity !== "programs" ||
          params.get("bounty") !== "1" ||
          !!row.bounty) &&
        (entity !== "assets" ||
          !params.get("scope") ||
          row.scope === params.get("scope")) &&
        (entity !== "assets" ||
          !params.get("type") ||
          row.type === params.get("type")) &&
        (entity !== "changes" ||
          !params.get("kind") ||
          row.kind === params.get("kind")) &&
        (entity !== "changes" ||
          params.get("unread") !== "1" ||
          !read.has(Number(row.id))),
    )
    .sort((a, b) =>
      entity === "changes"
        ? Number(b.id) - Number(a.id)
        : entity === "programs"
          ? (b.watched || 0) - (a.watched || 0) ||
            a.label.localeCompare(b.label)
          : a.label.localeCompare(b.label),
    );
}
export function exportCSVText(entity: Entity, rows: Row[]): string {
  const columns: Record<Entity, (keyof Row)[]> = {
    programs: [
      "platform",
      "label",
      "url",
      "bounty",
      "asset_count",
      "in_scope_count",
    ],
    assets: [
      "platform",
      "program_name",
      "label",
      "type",
      "scope",
      "conflict",
      "program_url",
    ],
    changes: [
      "created_at",
      "platform",
      "program_name",
      "entity",
      "kind",
      "label",
    ],
  };
  const escape = (value: unknown) => {
    let s = String(value ?? "");
    if (/^[\t\r\n]/.test(s) || /^[\s]*[=+@-]/.test(s)) s = "'" + s;
    return '"' + s.replaceAll('"', '""') + '"';
  };
  return (
    "\ufeff" +
    [
      columns[entity].join(","),
      ...rows.map((r) => columns[entity].map((k) => escape(r[k])).join(",")),
    ].join("\r\n")
  );
}
export function createDataClient(
  baseURL: string,
  fetcher: typeof fetch = fetch,
  storageProvider: () => Storage | undefined = () => undefined,
) {
  let manifestPromise: Promise<Manifest> | undefined;
  let currentVersion = "";
  const cache = new Map<string, Promise<unknown>>();
  const base = new URL("data/", baseURL);
  const storage = () => {
    try {
      return storageProvider();
    } catch {
      return undefined;
    }
  };
  async function getJSON<T>(name: string, noCache = false): Promise<T> {
    const response = await fetcher(new URL(name, base), {
      cache: noCache ? "no-store" : "default",
    });
    if (!response.ok)
      throw new Error(
        `公开数据加载失败（HTTP ${response.status}）。请刷新重试，或在 Actions 查看发布状态。`,
      );
    return response.json();
  }
  const getManifest = () => {
    if (!manifestPromise)
      manifestPromise = getJSON<Manifest>("manifest.json", true)
        .then((m) => {
          if (
            m.schema !== 1 ||
            !m.dataset_id ||
            !m.version ||
            !m.files ||
            !m.config
          )
            throw new Error("不支持的数据版本，请重新构建站点。");
          if (currentVersion !== m.version) {
            cache.clear();
            currentVersion = m.version;
          }
          return m;
        })
        .catch((e) => {
          manifestPromise = undefined;
          throw e;
        });
    return manifestPromise;
  };
  function file<T>(name: string | undefined): Promise<T> {
    if (!name || !/^[\w-]+\.[a-f0-9]{16}\.json$/.test(name))
      return Promise.reject(new Error("数据清单中的文件路径无效"));
    if (!cache.has(name))
      cache.set(
        name,
        getJSON(name).catch((e) => {
          cache.delete(name);
          throw e;
        }),
      );
    return cache.get(name) as Promise<T>;
  }
  function key(m: Manifest) {
    return `src-observatory:${new URL(baseURL).pathname}:${m.dataset_id}`;
  }
  async function rows(
    entity: Entity,
    m: Manifest,
    platform = "",
  ): Promise<Row[]> {
    if (entity === "changes") return file<Row[]>(m.files.changes);
    const programs = await file<Row[]>(m.files.programs);
    if (entity === "programs") return programs;
    const byID = new Map(programs.map((p) => [String(p.id), p]));
    const platforms = platform
      ? ["hackerone", "bugcrowd"].filter((p) => p === platform)
      : ["hackerone", "bugcrowd"];
    const arrays = await Promise.all(
      platforms.map((p) => file<Row[]>(m.files["assets-" + p])),
    );
    return arrays
      .flat()
      .map((row) => ({
        ...row,
        program_name: byID.get(row.program_id)?.label,
        program_url: byID.get(row.program_id)?.url,
      }));
  }
  async function request<T>(path: string, body?: unknown): Promise<T> {
    const [endpoint, search = ""] = path.split("?"),
      params = new URLSearchParams(search),
      m = await getManifest();
    const prefs = readPreferences(storage(), key(m));
    if (body !== undefined) {
      const payload = body as { id: string; watched: boolean; ids: number[] };
      if (endpoint === "watch") {
        const watched = new Set(prefs.watched);
        if (payload.watched) watched.add(payload.id);
        else watched.delete(payload.id);
        prefs.watched = [...watched];
      } else if (endpoint === "changes/read")
        prefs.read = [...new Set([...prefs.read, ...payload.ids])];
      else throw new Error("静态站点不能修改采集任务，请前往 GitHub Actions。");
      const target = storage();
      if (!target)
        throw new Error("浏览器未允许本地存储，无法保存关注或已读状态。");
      try {
        target.setItem(key(m), JSON.stringify(prefs));
      } catch {
        throw new Error("本地存储不可用或已满，未保存本次修改。");
      }
      return { ok: true } as T;
    }
    if (endpoint === "overview") {
      const [overview, programs, changes] = await Promise.all([
        file<Overview>(m.files.overview),
        file<Row[]>(m.files.programs),
        file<Row[]>(m.files.changes),
      ]);
      const read = new Set(prefs.read),
        watched = new Set(prefs.watched);
      return {
        ...overview,
        counts: {
          ...overview.counts,
          watched: programs.filter((p) => watched.has(String(p.id))).length,
          unread: changes.filter((c) => !read.has(Number(c.id))).length,
        },
        syncing: false,
        published_at: m.generated_at,
        config: m.config,
        history_limit: m.history_limit,
        history_total: m.history_total,
      } as T;
    }
    if (endpoint === "runs") return { items: await file(m.files.runs) } as T;
    if (endpoint === "asset") {
      const indexes = await rows("assets", m),
        asset = indexes.find((r) => r.id === params.get("id"));
      if (!asset) throw new Error("该资产已不在当前数据集中，请刷新清单。");
      const program = (await file<Row[]>(m.files.programs)).find(
        (p) => p.id === asset.program_id,
      );
      const detail = (await file<Row[]>(program?.detail_file)).find(
        (r) => r.id === asset.id,
      );
      if (!detail) throw new Error("资产详情缺失，请等待下一次发布。");
      return { ...asset, ...detail } as T;
    }
    if (
      endpoint === "programs" ||
      endpoint === "assets" ||
      endpoint === "changes"
    ) {
      const all = filterRows(
        await rows(endpoint, m, params.get("platform") || ""),
        endpoint,
        params,
        prefs,
      );
      const page = Math.max(1, Number(params.get("page")) || 1),
        limit = Math.min(100, Math.max(1, Number(params.get("limit")) || 25));
      const result: List = {
        items:
          params.get("all") === "1"
            ? all
            : all.slice((page - 1) * limit, page * limit),
        total: all.length,
        page,
        limit,
      };
      return result as T;
    }
    throw new Error("未知数据请求");
  }
  return {
    request,
    async refresh() {
      manifestPromise = undefined;
      return getManifest();
    },
  };
}
