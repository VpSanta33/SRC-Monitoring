import { useEffect, useRef, useState } from "react";
import {
  Activity,
  ArrowDownLeft,
  ArrowRight,
  ArrowUpRight,
  Bell,
  Check,
  CheckCheck,
  ChevronLeft,
  ChevronRight,
  CircleHelp,
  Database,
  Download,
  ExternalLink,
  Filter,
  Globe2,
  Github,
  Layers3,
  LayoutDashboard,
  LoaderCircle,
  Menu,
  Plus,
  Radar,
  RefreshCw,
  Search,
  Settings2,
  ShieldCheck,
  Star,
  X,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";

import type { Page, Entity, Source, Overview, Row, List, Run } from "./types";
import { createDataClient, exportCSVText } from "./data";
const dataClient = createDataClient(
  new URL(import.meta.env.BASE_URL, location.origin).href,
  fetch,
  () => window.localStorage,
);
const labels: Record<Page, string> = {
  overview: "监控概览",
  programs: "项目目录",
  assets: "资产清单",
  changes: "变更动态",
  watchlist: "我的关注",
  sources: "数据源与同步",
};
const nav: { id: Page; icon: LucideIcon }[] = [
  { id: "overview", icon: LayoutDashboard },
  { id: "programs", icon: Layers3 },
  { id: "assets", icon: Globe2 },
  { id: "changes", icon: Activity },
  { id: "watchlist", icon: Star },
];
const platformName = (p: string) =>
  p === "hackerone" ? "HackerOne" : "Bugcrowd";
const num = (n: number | undefined) =>
  new Intl.NumberFormat("en-US").format(n || 0);
const date = (s?: string) =>
  s
    ? new Date(s).toLocaleString("zh-CN", {
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      })
    : "尚未同步";
const kindName = (k?: string) =>
  ({ added: "新增", modified: "修改", removed: "移除" })[k || ""] || k;
const scopeName = (s?: string) => (s === "in_scope" ? "范围内" : "范围外");
function getPage(): Page {
  const p = location.hash.slice(1).split("?")[0];
  return Object.hasOwn(labels, p) ? (p as Page) : "overview";
}
async function api<T>(path: string, body?: unknown): Promise<T> {
  return dataClient.request<T>(path, body);
}
function Platform({
  name,
  compact = false,
}: {
  name: string;
  compact?: boolean;
}) {
  return (
    <span className={"platform " + name}>
      <span className="platform-mark">{name === "hackerone" ? "h₁" : "b"}</span>
      {!compact && platformName(name)}
    </span>
  );
}
function Empty({
  title,
  description,
  icon: Icon = Radar,
}: {
  title: string;
  description: string;
  icon?: LucideIcon;
}) {
  return (
    <div className="empty">
      <span className="empty-icon">
        <Icon size={25} />
      </span>
      <h3>{title}</h3>
      <p>{description}</p>
    </div>
  );
}
function Badge({
  kind,
  children,
}: {
  kind?: string;
  children: React.ReactNode;
}) {
  return <span className={"badge " + (kind || "")}>{children}</span>;
}

export default function App() {
  const [page, setPage] = useState<Page>(getPage);
  const [overview, setOverview] = useState<Overview | null>(null);
  const [list, setList] = useState<List | null>(null);
  const [recent, setRecent] = useState<Row[]>([]);
  const [runs, setRuns] = useState<Run[]>([]);
  const [filters, setFilters] = useState<Record<string, string>>(() =>
    Object.fromEntries(new URLSearchParams(location.hash.split("?")[1] || "")),
  );
  const [query, setQuery] = useState("");
  const [pageNo, setPageNo] = useState(1);
  const [refresh, setRefresh] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [toast, setToast] = useState("");
  const [busy, setBusy] = useState(false);
  const [menu, setMenu] = useState(false);
  const [detail, setDetail] = useState<Row | null>(null);
  const [programAssets, setProgramAssets] = useState<List | null>(null);
  const [detailError, setDetailError] = useState("");
  const dialog = useRef<HTMLDialogElement>(null);
  const entity: Entity =
    page === "watchlist"
      ? "programs"
      : page === "assets"
        ? "assets"
        : page === "changes"
          ? "changes"
          : "programs";
  useEffect(() => {
    const route = () => {
      setPage(getPage());
      setFilters(
        Object.fromEntries(
          new URLSearchParams(location.hash.split("?")[1] || ""),
        ),
      );
      setQuery("");
      setPageNo(1);
      setMenu(false);
      setError("");
    };
    window.addEventListener("hashchange", route);
    return () => window.removeEventListener("hashchange", route);
  }, []);
  useEffect(() => {
    const timer = setTimeout(() => {
      setFilters((f) => ({ ...f, q: query }));
      setPageNo(1);
    }, 250);
    return () => clearTimeout(timer);
  }, [query]);
  useEffect(() => {
    if (!toast) return;
    const timer = setTimeout(() => setToast(""), 4000);
    return () => clearTimeout(timer);
  }, [toast]);
  useEffect(() => {
    let alive = true;
    const load = () =>
      api<Overview>("overview")
        .then((data) => {
          if (alive) setOverview(data);
        })
        .catch((e) => {
          if (alive) setError(e.message);
        });
    load();
    const timer = setInterval(() => {
      void dataClient
        .refresh()
        .then(() => {
          if (alive) setRefresh((v) => v + 1);
        })
        .catch((e) => {
          if (alive) setError(e.message);
        });
    }, 300000);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, [refresh]);
  useEffect(() => {
    const changed = (event: StorageEvent) => {
      if (!event.key || event.key.startsWith("src-observatory:"))
        setRefresh((v) => v + 1);
    };
    window.addEventListener("storage", changed);
    return () => window.removeEventListener("storage", changed);
  }, []);
  const sourcesSignature = overview?.sources
    .map((s) => s.last_success + s.status)
    .join("|");
  useEffect(() => {
    let alive = true;
    setError("");
    setLoading(true);
    const params = new URLSearchParams({
      ...filters,
      page: String(pageNo),
      ...(page === "watchlist" ? { watched: "1" } : {}),
    });
    const load = async () => {
      try {
        if (page === "overview") {
          const data = await api<List>("changes?limit=5");
          if (alive) setRecent(data.items);
        } else if (page === "sources") {
          const data = await api<{ items: Run[] }>("runs");
          if (alive) setRuns(data.items);
        } else {
          const data = await api<List>(entity + "?" + params);
          if (alive) setList(data);
        }
      } catch (e) {
        if (alive) setError((e as Error).message);
      } finally {
        if (alive) setLoading(false);
      }
    };
    load();
    return () => {
      alive = false;
    };
  }, [page, filters, pageNo, refresh, entity, sourcesSignature]);
  useEffect(() => {
    let alive = true;
    if (detail) {
      dialog.current?.showModal();
      setDetailError("");
      setProgramAssets(null);
      if (detail.entity === "program")
        api<List>(
          "assets?" +
            new URLSearchParams({ program_id: String(detail.id), limit: "25" }),
        )
          .then((v) => {
            if (alive) setProgramAssets(v);
          })
          .catch((e) => {
            if (alive) setDetailError(e.message);
          });
    } else {
      dialog.current?.close();
    }
    return () => {
      alive = false;
    };
  }, [detail]);
  const updateFilter = (key: string, value: string) => {
    setFilters((f) => ({ ...f, [key]: value }));
    setPageNo(1);
  };
  const refreshData = async () => {
    setBusy(true);
    try {
      await dataClient.refresh();
      setRefresh((v) => v + 1);
      setToast("已读取最新发布数据；采集任务由 GitHub Actions 执行");
    } catch (e) {
      setToast((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const watch = async (row: Row) => {
    try {
      await api("watch", { id: row.id, watched: !row.watched });
      setToast(row.watched ? "已取消关注" : "已加入关注列表");
      setDetail((d) =>
        d?.id === row.id ? { ...d, watched: row.watched ? 0 : 1 } : d,
      );
      setRefresh((v) => v + 1);
    } catch (e) {
      setToast((e as Error).message);
    }
  };
  const markRead = async (ids: number[]) => {
    try {
      await api("changes/read", { ids });
      setRefresh((v) => v + 1);
      setToast("已标记为已读");
    } catch (e) {
      setToast((e as Error).message);
    }
  };
  const openDetail = async (row: Row) => {
    if (row.entity === "asset" && !row.kind) {
      try {
        setDetail(
          await api<Row>("asset?id=" + encodeURIComponent(String(row.id))),
        );
      } catch (e) {
        setToast((e as Error).message);
      }
    } else setDetail(row);
    if (row.kind && !row.seen) void markRead([Number(row.id)]);
  };
  const exportCSV = async () => {
    try {
      const params = new URLSearchParams({
        ...filters,
        all: "1",
        ...(page === "watchlist" ? { watched: "1" } : {}),
      });
      const result = await api<List>(entity + "?" + params);
      const url = URL.createObjectURL(
        new Blob([exportCSVText(entity, result.items)], {
          type: "text/csv;charset=utf-8",
        }),
      );
      const link = document.createElement("a");
      link.href = url;
      link.download = `src-${entity}.csv`;
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (e) {
      setToast((e as Error).message);
    }
  };
  const syncing = busy;
  const repositoryURL =
    "https://github.com/" +
    (overview?.config.repository || "VpSanta33/SRC-Monitoring");
  const workflowURL =
    repositoryURL +
    "/actions/workflows/" +
    (overview?.config.workflow || "pages.yml");
  const counts = overview?.counts || {};
  const initialized = (overview?.sources || []).some((s) => s.last_success);
  const heading = (
    <div className="page-heading">
      <div>
        <h1>{labels[page]}</h1>
        <p>
          {
            {
              overview: "集中跟踪公开项目、资产范围与最新变更。",
              programs: "发现公开安全项目，持续跟踪你关注的范围。",
              assets: "在统一清单中检索、筛选与核对公开资产。",
              changes: "查看范围与项目的变化，保留完整的前后记录。",
              watchlist: "集中查看已关注项目及其资产范围。",
              sources: "管理公开数据来源、同步计划和运行记录。",
            }[page]
          }
        </p>
      </div>
      <div className="heading-actions">
        {page !== "overview" && page !== "sources" && (
          <button className="btn" onClick={exportCSV}>
            <Download size={16} />
            导出 CSV
          </button>
        )}
        <button
          className="btn primary"
          onClick={refreshData}
          disabled={syncing}
        >
          <RefreshCw size={16} className={syncing ? "spin" : ""} />
          {syncing ? "正在刷新" : "刷新数据"}
        </button>
      </div>
    </div>
  );
  function sourceCard(s: Source) {
    return (
      <div className="source-card" key={s.platform}>
        <div className="source-card-top">
          <Platform name={s.platform} />
          <Badge
            kind={
              !s.enabled
                ? "muted"
                : s.status === "healthy"
                  ? "green"
                  : s.status === "error"
                    ? "red"
                    : "amber"
            }
          >
            <span className="status-dot" />
            {!s.enabled
              ? "已暂停"
              : s.status === "healthy"
                ? "同步正常"
                : s.status === "running"
                  ? "同步中"
                  : s.status === "error"
                    ? "同步异常"
                    : "等待首次同步"}
          </Badge>
        </div>
        <p className="source-description">公开项目与范围 · 社区镜像</p>
        <div className="source-numbers">
          <div>
            <strong>{num(s.program_count)}</strong>
            <span>公开项目</span>
          </div>
          <div>
            <strong>{num(s.asset_count)}</strong>
            <span>去重资产</span>
          </div>
        </div>
        <div className="source-footer">
          <span>成功同步</span>
          <span>{date(s.last_success)}</span>
          <a
            className="icon-button"
            href={s.url}
            target="_blank"
            rel="noreferrer"
            title={"查看 " + platformName(s.platform) + " 数据源"}
            aria-label={"查看 " + platformName(s.platform) + " 数据源"}
          >
            <ExternalLink size={15} />
          </a>
        </div>
        {s.error && <p className="source-error">{s.error}</p>}
      </div>
    );
  }
  function changesTable(rows: Row[], compact = false) {
    return (
      <div className="table-scroll">
        <table>
          <thead>
            <tr>
              <th>变更 / 资产标识</th>
              <th>所属项目</th>
              {!compact && <th>平台</th>}
              <th>发现时间</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.id} className={r.seen ? "" : "unread-row"}>
                <td>
                  <button
                    className="table-link change-label"
                    onClick={() => openDetail(r)}
                  >
                    <Badge kind={r.kind}>{kindName(r.kind)}</Badge>
                    <span className="truncate">{r.label}</span>
                    {!r.seen && <i className="unread-dot" />}
                  </button>
                  <span className="cell-sub">
                    {r.entity === "program" ? "项目资料" : "资产范围"}
                  </span>
                </td>
                <td className="program-cell">{r.program_name}</td>
                {!compact && (
                  <td>
                    <Platform name={r.platform} />
                  </td>
                )}
                <td className="time-cell">{date(r.created_at)}</td>
                <td>
                  <button
                    className="icon-button"
                    aria-label={"查看变更 " + r.label}
                    onClick={() => openDetail(r)}
                  >
                    <ArrowUpRight size={16} />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  }
  const maxType = Math.max(...(overview?.types || []).map((t) => t.count), 1);
  return (
    <>
      {menu && (
        <button
          className="nav-backdrop"
          aria-label="关闭导航"
          onClick={() => setMenu(false)}
        />
      )}
      <aside id="main-sidebar" className={"sidebar " + (menu ? "open" : "")}>
        <button
          className="icon-button sidebar-close"
          aria-label="关闭侧边栏"
          onClick={() => setMenu(false)}
        >
          <X size={16} />
        </button>
        <a className="brand" href="#overview">
          <img src={import.meta.env.BASE_URL + "favicon.svg"} alt="" />
          <span>
            Observatory<small>SRC ASSET MONITOR</small>
          </span>
        </a>
        <div className="nav-label">工作台</div>
        <nav aria-label="主导航">
          {nav.map(({ id, icon: Icon }) => (
            <a className={page === id ? "active" : ""} href={"#" + id} key={id}>
              <Icon size={18} />
              {labels[id]}
              {id === "programs" && (
                <span className="nav-count">{num(counts.programs)}</span>
              )}
              {id === "changes" && counts.unread > 0 && (
                <span className="nav-dot" />
              )}
            </a>
          ))}
        </nav>
        <div className="nav-label second-label">管理</div>
        <nav>
          <a className={page === "sources" ? "active" : ""} href="#sources">
            <Database size={18} />
            数据源与同步
          </a>
        </nav>
        <div className="sidebar-bottom">
          <div className="live-label">
            <span className="status-dot" />
            被动监控 · 公开范围
          </div>
          <div className="version">
            OBSERVATORY <span>v1.0</span>
          </div>
        </div>
      </aside>
      <div className="app-shell">
        <header className="topbar">
          <button
            className="icon-button mobile-menu"
            aria-label="打开导航"
            aria-expanded={menu}
            aria-controls="main-sidebar"
            onClick={() => setMenu(!menu)}
          >
            <Menu size={20} />
          </button>
          <div className="breadcrumb">
            工作台 <span>/</span>
            <strong>{labels[page]}</strong>
          </div>
          <div className="topbar-right">
            <span className="clock-label">
              {new Date().toLocaleDateString("zh-CN", {
                year: "numeric",
                month: "long",
                day: "numeric",
                weekday: "short",
              })}
            </span>
            <a
              className="notification-button"
              href="#changes"
              aria-label="查看未读变更"
            >
              <Bell size={18} />
              {counts.unread > 0 && <i />}
            </a>
            <a
              className="repository-link"
              href={repositoryURL}
              target="_blank"
              rel="noreferrer"
            >
              <Github size={17} />
              源码
            </a>
          </div>
        </header>
        <main>
          {heading}
          {error && (
            <div className="error-banner">
              {error}
              <button onClick={() => setRefresh((v) => v + 1)}>重试</button>
            </div>
          )}
          {!overview ? (
            <div className="loading">
              <LoaderCircle className="spin" />
              正在连接监控服务…
            </div>
          ) : page === "overview" ? (
            <>
              <div className="overview-status">
                <span className="status-dot" />
                <span>
                  {overview.syncing
                    ? "正在采集最新公开范围"
                    : initialized
                      ? "公开数据已加载"
                      : "尚未发布有效资产基线"}
                </span>
                <span className="separator">/</span>
                <span>GitHub Actions 定时更新</span>
                <a href="#sources">
                  数据发布于 {date(overview.published_at)}{" "}
                  <ArrowUpRight size={13} />
                </a>
              </div>
              <section className="stats-grid">
                {[
                  {
                    label: "监控项目",
                    value: counts.programs,
                    icon: Layers3,
                    foot: "来自 2 个公开平台",
                    tag: "PROGRAMS",
                  },
                  {
                    label: "资产总量",
                    value: counts.assets,
                    icon: Globe2,
                    foot: num(counts.in_scope) + " 项在范围内",
                    tag: "ASSETS",
                  },
                  {
                    label: "24 小时变更",
                    value: counts.changes_24h,
                    icon: Activity,
                    foot: "新增、修改与移除",
                    tag: "CHANGES",
                  },
                  {
                    label: "关注项目",
                    value: counts.watched,
                    icon: Star,
                    foot: "集中跟进重要项目",
                    tag: "WATCHLIST",
                  },
                ].map(({ label, value, icon: Icon, foot, tag }) => (
                  <div className="stat-card" key={tag}>
                    <div className="stat-top">
                      <span>{label}</span>
                      <Icon size={18} />
                    </div>
                    <div className="stat-number">
                      {num(value)}
                      <span>{tag === "CHANGES" ? "次" : "项"}</span>
                    </div>
                    <div className="stat-foot">
                      <span>{foot}</span>
                    </div>
                  </div>
                ))}
              </section>
              <div className="overview-grid">
                <section className="panel sources-panel">
                  <div className="section-heading">
                    <h2>
                      数据源监控 <span>02</span>
                    </h2>
                    <a href="#sources">
                      管理 <ArrowUpRight size={14} />
                    </a>
                  </div>
                  <div className="source-grid">
                    {overview.sources.map(sourceCard)}
                  </div>
                  <div className="source-note">
                    <ShieldCheck size={15} />
                    <span>同步完整快照，异常时保留已有资产与历史。</span>
                  </div>
                </section>
                <section className="panel type-panel">
                  <div className="section-heading">
                    <h2>资产类型分布</h2>
                    <span className="small-muted">TOP 6</span>
                  </div>
                  {overview.types.length ? (
                    <div className="type-bars">
                      {overview.types.map((t, i) => (
                        <div className="type-row" key={t.type}>
                          <div>
                            <span>
                              <i className={"type-dot dot-" + i} />
                              {t.type}
                            </span>
                            <strong>{num(t.count)}</strong>
                          </div>
                          <div className="bar-track">
                            <div
                              className={"bar-fill bar-" + i}
                              style={{
                                width:
                                  Math.max(2, (t.count / maxType) * 100) + "%",
                              }}
                            />
                          </div>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <Empty
                      title="等待资产数据"
                      description="完成首次同步后显示类型分布。"
                    />
                  )}
                </section>
              </div>
              <section className="panel recent-panel">
                <div className="section-heading">
                  <h2>
                    最新变更 <span className="subtle">LATEST ACTIVITY</span>
                  </h2>
                  <a href="#changes">
                    查看全部 <ArrowRight size={14} />
                  </a>
                </div>
                {recent.length ? (
                  changesTable(recent, true)
                ) : (
                  <Empty
                    title={
                      initialized
                        ? "暂无新的范围变更"
                        : "正在建立第一份资产快照"
                    }
                    description="首次采集不会生成变更通知；后续快照出现差异时，将记录在这里。"
                    icon={Activity}
                  />
                )}
              </section>
              <div className="bottom-note">
                <CircleHelp size={15} />
                <span>
                  公开镜像可能存在延迟。开展测试前，请打开项目原始链接核对最新范围与规则。
                </span>
                <a
                  href="https://github.com/arkadiyt/bounty-targets-data"
                  target="_blank"
                  rel="noreferrer"
                >
                  关于数据源 <ExternalLink size={12} />
                </a>
              </div>
            </>
          ) : page === "sources" ? (
            <>
              <div className="source-grid large-source-grid">
                {overview.sources.map(sourceCard)}
              </div>
              <section className="panel settings-panel">
                <div className="section-heading">
                  <h2>
                    <Settings2 size={18} />
                    同步计划
                  </h2>
                  <Badge kind="muted">GitHub Actions</Badge>
                </div>
                <div className="settings-content">
                  <div className="schedule-description">
                    <strong>定时采集公开范围，自动发布更新</strong>
                    <p>
                      计划：<code>{overview.config.schedule}</code>（
                      {overview.config.timezone}）。实际执行时间可能因 GitHub
                      排队而延迟。
                    </p>
                    <p>
                      数据发布于 {date(overview.published_at)}
                      。刷新页面只读取已发布数据。
                    </p>
                  </div>
                  <a
                    className="btn primary"
                    href={workflowURL}
                    target="_blank"
                    rel="noreferrer"
                  >
                    <ExternalLink size={16} />
                    查看采集任务
                  </a>
                </div>
                <div className="source-note">
                  如需手动采集或修改计划，由仓库维护者在 GitHub Actions
                  中操作；浏览本站无需登录。
                </div>
              </section>
              <section className="panel">
                <div className="section-heading">
                  <h2>同步日志</h2>
                  <span className="small-muted">最近 50 次运行</span>
                </div>
                {runs.length ? (
                  <div className="table-scroll">
                    <table>
                      <thead>
                        <tr>
                          <th>平台</th>
                          <th>状态</th>
                          <th>开始时间</th>
                          <th>项目 / 资产</th>
                          <th>变更</th>
                          <th>运行结果</th>
                        </tr>
                      </thead>
                      <tbody>
                        {runs.map((r) => (
                          <tr key={r.id}>
                            <td>
                              <Platform name={r.platform} />
                            </td>
                            <td>
                              <Badge
                                kind={
                                  r.status === "success"
                                    ? "green"
                                    : r.status === "failed"
                                      ? "red"
                                      : "amber"
                                }
                              >
                                {r.status === "success"
                                  ? "成功"
                                  : r.status === "failed"
                                    ? "失败"
                                    : "运行中"}
                              </Badge>
                            </td>
                            <td className="time-cell">{date(r.started_at)}</td>
                            <td>
                              {num(r.programs)} / {num(r.assets)}
                            </td>
                            <td>{num(r.changes)}</td>
                            <td className="log-message">
                              {r.message || "正在请求公开范围数据…"}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <Empty
                    title="还没有同步记录"
                    description="首次采集完成并发布后，将显示同步记录。"
                  />
                )}
              </section>
              <div className="bottom-note">
                <Database size={15} />
                <span>
                  当前数据适配器：bounty-targets-data
                  社区公开镜像。采集基线与完整变更历史保存在仓库中，页面展示最近
                  5,000 条变更。
                </span>
              </div>
            </>
          ) : (
            <>
              {page === "watchlist" && (
                <p className="local-preferences-note">
                  关注仅保存在当前浏览器，无需账号；清除网站数据后会重置。
                </p>
              )}
              {page === "changes" && (
                <p className="local-preferences-note">
                  已读状态仅保存在当前浏览器。展示最近{" "}
                  {num(overview.history_limit)} 条变更，完整历史共{" "}
                  {num(overview.history_total)} 条。
                </p>
              )}
              <div className="list-tabs">
                <button
                  className={!filters.platform ? "selected" : ""}
                  onClick={() => updateFilter("platform", "")}
                >
                  全部平台
                </button>
                <button
                  className={filters.platform === "hackerone" ? "selected" : ""}
                  onClick={() => updateFilter("platform", "hackerone")}
                >
                  HackerOne
                </button>
                <button
                  className={filters.platform === "bugcrowd" ? "selected" : ""}
                  onClick={() => updateFilter("platform", "bugcrowd")}
                >
                  Bugcrowd
                </button>
                <span>{num(list?.total)} 条记录</span>
              </div>
              {filters.program_id && (
                <div className="active-filter">
                  <Layers3 size={13} />
                  <span>
                    当前项目：
                    {list?.items[0]?.program_name || filters.program_id}
                  </span>
                  <button
                    className="icon-button"
                    aria-label="清除项目限定"
                    onClick={() => {
                      updateFilter("program_id", "");
                      history.replaceState(null, "", "#assets");
                    }}
                  >
                    <X size={13} />
                  </button>
                </div>
              )}
              <section className="panel list-panel">
                <div className="filter-bar">
                  <label className="search-box">
                    <Search size={17} />
                    <input
                      aria-label="搜索项目或资产"
                      placeholder={
                        entity === "programs"
                          ? "搜索项目名称…"
                          : "搜索资产标识或项目名称…"
                      }
                      value={query}
                      onChange={(e) => setQuery(e.target.value)}
                    />
                    {query && (
                      <button
                        className="icon-button"
                        onClick={() => setQuery("")}
                        aria-label="清空搜索"
                      >
                        <X size={14} />
                      </button>
                    )}
                  </label>
                  <div className="filters">
                    <Filter size={15} />
                    {entity === "assets" ? (
                      <>
                        <select
                          aria-label="范围筛选"
                          value={filters.scope || ""}
                          onChange={(e) =>
                            updateFilter("scope", e.target.value)
                          }
                        >
                          <option value="">所有范围</option>
                          <option value="in_scope">范围内</option>
                          <option value="out_of_scope">范围外</option>
                        </select>
                        <input
                          className="type-filter"
                          aria-label="资产类型筛选"
                          placeholder="精确类型，如 URL"
                          value={filters.type || ""}
                          onChange={(e) => updateFilter("type", e.target.value)}
                        />
                      </>
                    ) : entity === "changes" ? (
                      <>
                        <select
                          aria-label="变更类型"
                          value={filters.kind || ""}
                          onChange={(e) => updateFilter("kind", e.target.value)}
                        >
                          <option value="">所有变更</option>
                          <option value="added">新增</option>
                          <option value="modified">修改</option>
                          <option value="removed">移除</option>
                        </select>
                        <label className="checkbox-label">
                          <input
                            type="checkbox"
                            checked={filters.unread === "1"}
                            onChange={(e) =>
                              updateFilter(
                                "unread",
                                e.target.checked ? "1" : "",
                              )
                            }
                          />
                          仅未读
                        </label>
                      </>
                    ) : (
                      <label className="checkbox-label">
                        <input
                          type="checkbox"
                          checked={filters.bounty === "1"}
                          onChange={(e) =>
                            updateFilter("bounty", e.target.checked ? "1" : "")
                          }
                        />
                        赏金项目
                      </label>
                    )}
                    {entity !== "programs" && (
                      <label className="checkbox-label">
                        <input
                          type="checkbox"
                          checked={filters.watched === "1"}
                          onChange={(e) =>
                            updateFilter("watched", e.target.checked ? "1" : "")
                          }
                        />
                        仅关注
                      </label>
                    )}
                  </div>
                </div>
                {loading ? (
                  <div className="loading">
                    <LoaderCircle size={20} className="spin" />
                    正在加载…
                  </div>
                ) : list?.items.length ? (
                  entity === "changes" ? (
                    changesTable(list.items)
                  ) : (
                    <div className="table-scroll">
                      <table>
                        <thead>
                          <tr>
                            <th>
                              {entity === "programs" ? "项目名称" : "资产标识"}
                            </th>
                            <th>平台</th>
                            <th>
                              {entity === "programs" ? "资产范围" : "所属项目"}
                            </th>
                            <th>
                              {entity === "programs" ? "项目类型" : "范围状态"}
                            </th>
                            <th>
                              {entity === "programs" ? "关注" : "资产类型"}
                            </th>
                            <th />
                          </tr>
                        </thead>
                        <tbody>
                          {list.items.map((r) => (
                            <tr key={r.id}>
                              <td>
                                <button
                                  className="table-link name-cell"
                                  onClick={() => openDetail(r)}
                                >
                                  {entity === "programs" && (
                                    <span className="project-avatar">
                                      {r.label.charAt(0).toUpperCase()}
                                    </span>
                                  )}
                                  <span className="truncate">{r.label}</span>
                                </button>
                                {entity === "assets" && !!r.conflict && (
                                  <span className="conflict-note">
                                    原始范围存在冲突，请核对规则
                                  </span>
                                )}
                              </td>
                              <td>
                                <Platform name={r.platform} />
                              </td>
                              <td>
                                {entity === "programs" ? (
                                  <span>
                                    <strong>{num(r.asset_count)}</strong>
                                    <span className="small-muted">
                                      {" "}
                                      / {num(r.in_scope_count)} 范围内
                                    </span>
                                  </span>
                                ) : (
                                  <span className="program-cell">
                                    {r.program_name}
                                  </span>
                                )}
                              </td>
                              <td>
                                {entity === "programs" ? (
                                  <Badge kind={r.bounty ? "green" : "muted"}>
                                    {r.bounty ? "Bug Bounty" : "VDP / 无赏金"}
                                  </Badge>
                                ) : (
                                  <Badge
                                    kind={
                                      r.scope === "in_scope" ? "green" : "muted"
                                    }
                                  >
                                    {scopeName(r.scope)}
                                  </Badge>
                                )}
                              </td>
                              <td>
                                {entity === "programs" ? (
                                  <button
                                    className={
                                      "icon-button star-button " +
                                      (r.watched ? "starred" : "")
                                    }
                                    onClick={() => watch(r)}
                                    aria-label={
                                      (r.watched ? "取消关注 " : "关注 ") +
                                      r.label
                                    }
                                  >
                                    <Star
                                      size={18}
                                      fill={r.watched ? "currentColor" : "none"}
                                    />
                                  </button>
                                ) : (
                                  <span className="type-label">{r.type}</span>
                                )}
                              </td>
                              <td>
                                <button
                                  className="icon-button"
                                  aria-label={"查看 " + r.label}
                                  onClick={() => openDetail(r)}
                                >
                                  <ArrowUpRight size={16} />
                                </button>
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  )
                ) : (
                  <Empty
                    icon={page === "watchlist" ? Star : Search}
                    title={
                      page === "watchlist"
                        ? "还没有匹配的关注项目"
                        : "没有匹配的记录"
                    }
                    description={
                      page === "watchlist"
                        ? "在项目目录点击星标，即可持续关注项目。"
                        : "尝试调整筛选条件，或等待数据源完成同步。"
                    }
                  />
                )}
                <div className="pagination">
                  <span>共 {num(list?.total)} 条 · 每页 25 条</span>
                  <div>
                    {entity === "changes" &&
                      !!list?.items.some((r) => !r.seen) && (
                        <button
                          className="btn small"
                          onClick={() =>
                            markRead(
                              list.items
                                .filter((r) => !r.seen)
                                .map((r) => Number(r.id)),
                            )
                          }
                        >
                          <CheckCheck size={14} />
                          本页已读
                        </button>
                      )}
                    <button
                      className="icon-button"
                      disabled={pageNo <= 1 || loading}
                      aria-label="上一页"
                      onClick={() => setPageNo((p) => p - 1)}
                    >
                      <ChevronLeft size={17} />
                    </button>
                    <span>
                      {pageNo} /{" "}
                      {Math.max(1, Math.ceil((list?.total || 0) / 25))}
                    </span>
                    <button
                      className="icon-button"
                      disabled={pageNo * 25 >= (list?.total || 0) || loading}
                      aria-label="下一页"
                      onClick={() => setPageNo((p) => p + 1)}
                    >
                      <ChevronRight size={17} />
                    </button>
                  </div>
                </div>
              </section>
            </>
          )}
        </main>
        <footer>
          数据来自公开范围镜像 · 以平台原始规则为准
          <span>
            公开资产监控 <i /> GitHub Pages
          </span>
        </footer>
      </div>
      <dialog
        ref={dialog}
        onCancel={() => setDetail(null)}
        onClick={(e) => {
          if (e.target === dialog.current) setDetail(null);
        }}
        aria-labelledby="dialog-title"
      >
        {detail && (
          <>
            <div className="dialog-header">
              <div>
                <div className="eyebrow">
                  {detail.kind
                    ? "CHANGE DETAILS"
                    : detail.entity === "program"
                      ? "PROGRAM DETAILS"
                      : "ASSET DETAILS"}
                </div>
                <h2 id="dialog-title">{detail.label}</h2>
              </div>
              <button
                className="icon-button"
                aria-label="关闭详情"
                onClick={() => setDetail(null)}
              >
                <X size={21} />
              </button>
            </div>
            <div className="dialog-body">
              <div className="detail-summary">
                <Platform name={detail.platform} />
                {detail.kind && (
                  <Badge kind={detail.kind}>{kindName(detail.kind)}</Badge>
                )}
                <a
                  href={detail.url || detail.program_url}
                  target="_blank"
                  rel="noreferrer"
                >
                  查看平台原始规则 <ExternalLink size={14} />
                </a>
              </div>
              {detail.kind ? (
                <>
                  <p className="small-muted">
                    发现时间：{date(detail.created_at)} · {detail.program_name}
                  </p>
                  <div className="diff-grid">
                    <div>
                      <h3>
                        <ArrowDownLeft size={15} />
                        变更前
                      </h3>
                      <pre>
                        {detail.before_data
                          ? JSON.stringify(detail.before_data, null, 2)
                          : "无（新增记录）"}
                      </pre>
                    </div>
                    <div>
                      <h3>
                        <Plus size={15} />
                        变更后
                      </h3>
                      <pre>
                        {detail.after_data
                          ? JSON.stringify(detail.after_data, null, 2)
                          : "无（记录已移除）"}
                      </pre>
                    </div>
                  </div>
                </>
              ) : detail.entity === "program" ? (
                <>
                  <div className="detail-metrics">
                    <span>
                      <strong>{num(detail.asset_count)}</strong> 项资产
                    </span>
                    <span>
                      <strong>{num(detail.in_scope_count)}</strong> 项范围内
                    </span>
                    <button
                      className={"btn " + (detail.watched ? "" : "primary")}
                      onClick={() => watch(detail)}
                    >
                      <Star size={15} />
                      {detail.watched ? "取消关注" : "关注项目"}
                    </button>
                  </div>
                  <h3>资产范围</h3>
                  {detailError ? (
                    <div className="error-banner">{detailError}</div>
                  ) : programAssets ? (
                    <>
                      <div className="detail-assets">
                        {programAssets.items.map((a) => (
                          <div key={a.id}>
                            <span className="mono">{a.label}</span>
                            <Badge
                              kind={a.scope === "in_scope" ? "green" : "muted"}
                            >
                              {scopeName(a.scope)}
                            </Badge>
                          </div>
                        ))}
                      </div>
                      {programAssets.total > 25 && (
                        <p className="small-muted">
                          显示前 25 项，共 {num(programAssets.total)} 项。
                        </p>
                      )}
                      <button
                        className="btn"
                        onClick={() => {
                          setDetail(null);
                          location.hash =
                            "assets?program_id=" +
                            encodeURIComponent(String(detail.id));
                        }}
                      >
                        查看该项目的全部资产 <ArrowRight size={14} />
                      </button>
                    </>
                  ) : (
                    <div className="loading">
                      <LoaderCircle className="spin" />
                      加载资产…
                    </div>
                  )}
                  <h3>项目属性</h3>
                  <pre>{JSON.stringify(detail.data?.metadata, null, 2)}</pre>
                </>
              ) : (
                <>
                  <div className="detail-metrics">
                    <Badge
                      kind={detail.scope === "in_scope" ? "green" : "muted"}
                    >
                      {scopeName(detail.scope)}
                    </Badge>
                    <span>{detail.type}</span>
                    <span>{detail.program_name}</span>
                  </div>
                  {!!detail.conflict && (
                    <div className="error-banner">
                      该资产在原始数据中同时出现在范围内和范围外，平台优先提示为范围外，请核对原始规则。
                    </div>
                  )}
                  <h3>原始范围说明</h3>
                  <pre>{JSON.stringify(detail.data?.metadata, null, 2)}</pre>
                </>
              )}
            </div>
          </>
        )}
      </dialog>
      {toast && (
        <div className="toast" role="status">
          <Check size={17} />
          {toast}
        </div>
      )}
    </>
  );
}
