import { sessionPath } from "../lib/sessions.ts";
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Link, Navigate, useSearchParams } from "react-router";
import { useCatalog, useStats, useThreads } from "../api/queries.ts";
import { usePlatform } from "../platform/context.tsx";
import type { PreferenceStorage } from "../platform/index.ts";
import { THREAD_STATUSES, type Stats, type ThreadStatus, type SubagentsFilter, type Thread, type ThreadListQuery, type ThreadSource } from "../api/types.ts";
import { IconExternal } from "../components/icons.tsx";
import { ProjectSelect } from "../components/ProjectSelect.tsx";
import { PrChips } from "../components/PullRequests.tsx";
import { ThreadStatusBadge, threadStatusLabel } from "../components/StateBadge.tsx";
import { Button, Drawer, ErrorText, Field, Input, PageHeader, Panel, QueryState, Segmented, Select, cx } from "../components/ui.tsx";
import { dateTime, localDateToIso, localMidnight, pick } from "../lib/format.ts";
import { RowCopyLink } from "../components/StageLinks.tsx";

const PAGE_SIZE = 50;
const PRESETS = ["today", "7d", "30d", "all", "custom"] as const;
type Preset = (typeof PRESETS)[number];
const PRESET_OPTIONS: { value: Preset; label: string }[] = PRESETS.map((p) => ({ value: p, label: p }));
const SOURCES: readonly ThreadSource[] = ["app", "t3"];
const SUBAGENT_VALUES = ["hidden", "shown"] as const;
const ALL_TIME_FROM = "2000-01-01";
const TZ = Intl.DateTimeFormat().resolvedOptions().timeZone;
const TOP_PROJECTS = 6;
/** Longest gap-filled day axis; beyond this the axis starts at the first day with data. */
const MAX_FILLED_DAYS = 120;

/** Preset → { from, to } (local-midnight aligned, so the query key is stable within a day). */
function range(preset: Preset, fromDate: string, toDate: string): { from?: string; to?: string } {
  switch (preset) {
    case "today":
      return { from: localMidnight(0) };
    case "7d":
      return { from: localMidnight(6) };
    case "30d":
      return { from: localMidnight(29) };
    case "all":
      return {};
    case "custom": {
      const from = localDateToIso(fromDate);
      const to = localDateToIso(toDate, 1);
      return { ...(from === undefined ? {} : { from }), ...(to === undefined ? {} : { to }) };
    }
  }
}

const pad = (n: number) => String(n).padStart(2, "0");
const dayKey = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
const sameDay = (a: Date, b: Date) => dayKey(a) === dayKey(b);
const isToday = (d: Date) => sameDay(d, new Date());
const axisLabel = (d: Date) => (isToday(d) ? "today" : shortDay(d));
const shortDay = (d: Date) => d.toLocaleDateString(undefined, { month: "short", day: "2-digit" });

/** "10:42" for today, "Nov 07" otherwise. */
function compactTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "-";
  if (isToday(d)) return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false });
  return shortDay(d);
}

type Filters = ReturnType<typeof useFilters>;

// "settled" is a filter of its own: badges show settled over the run status, so a status filter matches unsettled threads only.
const STATUS_FILTERS = [...THREAD_STATUSES, "settled"] as const;
type StatusFilter = (typeof STATUS_FILTERS)[number];

function statusQuery(s: StatusFilter | undefined): { status?: ThreadStatus[]; settled?: boolean } {
  if (s === undefined) return {};
  if (s === "settled") return { settled: true };
  return { status: [s], settled: false };
}

/** URL-param backed filter state. */
function useFilters() {
  const [params, setParams] = useSearchParams();
  const get = (k: string) => params.get(k) ?? "";
  const update = (patch: Record<string, string>) => {
    const next = new URLSearchParams(params);
    for (const [k, v] of Object.entries(patch)) {
      if (v === "") next.delete(k);
      else next.set(k, v);
    }
    if (!("page" in patch)) next.delete("page");
    setParams(next, { replace: true });
  };
  const q = get("q");
  // A title search (e.g. from the command palette) defaults to all time so older threads are found.
  const defaultPreset: Preset = q === "" ? "7d" : "all";
  const preset = pick(PRESETS, get("preset")) ?? defaultPreset;
  const status = pick(STATUS_FILTERS, get("status"));
  const subagentsShown = get("subagents") === "shown";
  const subagents: SubagentsFilter = subagentsShown ? "include" : "exclude";
  return {
    get,
    update,
    q,
    preset,
    defaultPreset,
    status,
    subagentsShown,
    subagents,
    provider: get("provider"),
    project: get("project"),
    source: pick(SOURCES, get("source")),
    page: Math.max(0, Number(get("page")) || 0),
  };
}

// Last-used filters (search string without `page`), restored when /history is opened without params.
const FILTERS_KEY = "stage-history-filters";

function readStoredFilters(storage: PreferenceStorage): string {
  return storage.get(FILTERS_KEY) ?? "";
}

/** Storage unavailable (private mode): filters just won't be remembered. */
function storeFilters(storage: PreferenceStorage, params: URLSearchParams) {
  const next = new URLSearchParams(params);
  next.delete("page");
  storage.set(FILTERS_KEY, next.toString());
}

/**
 * Restores remembered filters once, on arrival without params, before the view mounts (so no query fires
 * with the defaults first). Clearing every filter later leaves the URL empty without restoring again.
 */
export function HistoryPage() {
  const { storage } = usePlatform();
  const [params] = useSearchParams();
  const [restore, setRestore] = useState(() => (params.size === 0 ? readStoredFilters(storage) : ""));
  if (restore !== "") {
    if (params.size === 0) return <Navigate replace to={{ search: `?${restore}` }} />;
    setRestore("");
    return null;
  }
  return <HistoryView />;
}

function HistoryView() {
  const { storage } = usePlatform();
  const f = useFilters();
  const [params] = useSearchParams();
  const search = params.toString();
  useEffect(() => {
    storeFilters(storage, new URLSearchParams(search));
  }, [storage, search]);
  const [drawer, setDrawer] = useState(false);
  const { from, to } = range(f.preset, f.get("from"), f.get("to"));

  const query: ThreadListQuery = {
    limit: PAGE_SIZE,
    offset: f.page * PAGE_SIZE,
    subagents: f.subagents,
    ...(from === undefined ? {} : { from }),
    ...(to === undefined ? {} : { to }),
    ...(f.project === "" ? {} : { project: Number(f.project) }),
    ...statusQuery(f.status),
    ...(f.provider === "" ? {} : { provider: f.provider }),
    ...(f.source === undefined ? {} : { source: f.source }),
    ...(f.q === "" ? {} : { q: f.q }),
  };
  const threads = useThreads(query);
  // Stats default to the last 30 days when `from` is omitted; "all" needs an explicit early bound.
  const stats = useStats({
    tz: TZ,
    subagents: f.subagents,
    from: from ?? ALL_TIME_FROM,
    ...(to === undefined ? {} : { to }),
  });

  const activeCount = [f.project, f.status ?? "", f.provider, f.source ?? "", f.q].filter((v) => v !== "").length +
    (f.subagentsShown ? 1 : 0);
  const total = threads.data?.total;
  const meta = `${total === undefined ? "…" : total} threads · subagents ${f.subagentsShown ? "shown" : "hidden"}`;

  return (
    <>
      <PageHeader title="History" meta={meta} />
      <div className="flex flex-col gap-3.5">
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <Segmented
              label="Time window"
              options={PRESET_OPTIONS}
              value={f.preset}
              onChange={(p) => f.update({ preset: p === f.defaultPreset ? "" : p })}
            />
            <div className="hidden min-w-0 flex-1 flex-wrap items-center gap-2 md:flex">
              <FilterControls f={f} />
              <SearchBox key={f.q} initial={f.q} onSearch={(q) => f.update({ q })} className="ml-auto max-w-56 min-w-40 flex-1" />
            </div>
            <Button small className="ml-auto md:hidden" onClick={() => setDrawer(true)}>
              Filters{activeCount === 0 ? "" : ` · ${activeCount}`}
            </Button>
          </div>
          {f.preset === "custom" ? (
            <div className="flex flex-wrap items-end gap-2">
              <Field label="From" className="w-40">
                {(id) => <Input id={id} type="date" value={f.get("from")} onChange={(e) => f.update({ from: e.target.value })} />}
              </Field>
              <Field label="To (inclusive)" className="w-40">
                {(id) => <Input id={id} type="date" value={f.get("to")} onChange={(e) => f.update({ to: e.target.value })} />}
              </Field>
            </div>
          ) : null}
        </div>

        <ErrorText error={stats.error} />
        {stats.data === undefined ? null : <Summary stats={stats.data} preset={f.preset} />}

        <QueryState isPending={threads.isPending} error={threads.error}>
          {threads.data === undefined ? null : (
            <>
              <ThreadList data={threads.data.threads} />
              <Pager page={f.page} total={threads.data.total} onPage={(p) => f.update({ page: p === 0 ? "" : String(p) })} />
            </>
          )}
        </QueryState>
      </div>

      {drawer ? (
        <Drawer
          title="Filters"
          onClose={() => setDrawer(false)}
          footer={
            <>
              <Button
                variant="ghost"
                disabled={activeCount === 0}
                onClick={() => f.update({ project: "", status: "", provider: "", source: "", subagents: "", q: "" })}
              >
                Clear
              </Button>
              <Button variant="primary" className="ml-auto" onClick={() => setDrawer(false)}>
                Done
              </Button>
            </>
          }
        >
          <div className="flex flex-col gap-3">
            <Field label="Search title">
              {(id) => <SearchBox key={f.q} id={id} initial={f.q} onSearch={(q) => f.update({ q })} />}
            </Field>
            <FilterControls f={f} labelled />
          </div>
        </Drawer>
      ) : null}
    </>
  );
}

/** The select filters; `labelled` stacks each under a Field label (drawer), otherwise inline with aria-labels. */
function FilterControls({ f, labelled = false }: { f: Filters; labelled?: boolean }) {
  const wrap = (label: string, control: (id: string | undefined) => ReactNode) =>
    labelled ? (
      <Field label={label}>{(id) => control(id)}</Field>
    ) : (
      control(undefined)
    );
  const cls = labelled ? "w-full" : "w-auto";
  return (
    <>
      {labelled ? (
        <Field label="Project">
          {(id) => <ProjectSelect id={id} value={f.project} onChange={(v) => f.update({ project: v })} />}
        </Field>
      ) : (
        <ProjectSelect value={f.project} onChange={(v) => f.update({ project: v })} />
      )}
      {wrap("Status", (id) => (
        <Select id={id} className={cls} value={f.status ?? ""} onChange={(e) => f.update({ status: e.target.value })} aria-label="Status">
          <option value="">Any status</option>
          {STATUS_FILTERS.map((s) => (
            <option key={s} value={s}>
              {s === "settled" ? "settled" : threadStatusLabel(s)}
            </option>
          ))}
        </Select>
      ))}
      {wrap("Provider", (id) => (
        <ProviderSelect id={id} className={cls} value={f.provider} onChange={(v) => f.update({ provider: v })} />
      ))}
      {wrap("Source", (id) => (
        <Select id={id} className={cls} value={f.source ?? ""} onChange={(e) => f.update({ source: e.target.value })} aria-label="Source">
          <option value="">Source: app + t3</option>
          <option value="app">Source: app</option>
          <option value="t3">Source: t3</option>
        </Select>
      ))}
      {wrap("Subagents", (id) => (
        <Select
          id={id}
          className={cls}
          value={f.subagentsShown ? "shown" : "hidden"}
          onChange={(e) => {
            const v = pick(SUBAGENT_VALUES, e.target.value);
            f.update({ subagents: v === "shown" ? "shown" : "" });
          }}
          aria-label="Subagents"
        >
          <option value="hidden">Subagents: hidden</option>
          <option value="shown">Subagents: shown</option>
        </Select>
      ))}
    </>
  );
}

function SearchBox({
  id,
  initial,
  onSearch,
  className,
}: {
  id?: string;
  initial: string;
  onSearch: (q: string) => void;
  className?: string;
}) {
  const [q, setQ] = useState(initial);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    onSearch(q.trim());
  };
  return (
    <form onSubmit={submit} role="search" className={className}>
      <Input
        id={id}
        type="search"
        value={q}
        onChange={(e) => setQ(e.target.value)}
        onBlur={() => {
          if (q.trim() !== initial) onSearch(q.trim());
        }}
        placeholder="search titles"
        aria-label="Search titles"
      />
    </form>
  );
}

function ProviderSelect({
  id,
  className,
  value,
  onChange,
}: {
  id: string | undefined;
  className: string;
  value: string;
  onChange: (v: string) => void;
}) {
  const providers = useCatalog().data?.providers ?? [];
  const known = providers.some((p) => p.providerInstanceId === value);
  return (
    <Select id={id} className={className} value={value} onChange={(e) => onChange(e.target.value)} aria-label="Provider">
      <option value="">Any provider</option>
      {providers.map((p) => (
        <option key={p.providerInstanceId} value={p.providerInstanceId}>
          {p.displayName}
        </option>
      ))}
      {value !== "" && !known ? <option value={value}>{value}</option> : null}
    </Select>
  );
}

// ---- Summary ----

type Day = { key: string; date: Date; count: number };

/** Gap-filled day series between the stats range bounds (capped at today). */
function daySeries(stats: Stats): Day[] {
  const counts = new Map(stats.threadsByDay.map((d) => [d.day, d.count]));
  const now = new Date();
  const toBound = new Date(new Date(stats.to).getTime() - 1);
  const end = Number.isNaN(toBound.getTime()) || toBound > now ? now : toBound;
  const start = new Date(stats.from);
  if (Number.isNaN(start.getTime())) return [];
  start.setHours(0, 0, 0, 0);
  const spanDays = Math.round((end.getTime() - start.getTime()) / 86_400_000);
  if (spanDays > MAX_FILLED_DAYS) {
    const first = stats.threadsByDay[0];
    const m = first === undefined ? null : /^(\d{4})-(\d{2})-(\d{2})$/.exec(first.day);
    if (m === null) return [];
    start.setFullYear(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  }
  const endMidnight = new Date(end.getFullYear(), end.getMonth(), end.getDate());
  const n = Math.round((endMidnight.getTime() - start.getTime()) / 86_400_000);
  const days: Day[] = [];
  for (let i = 0; i <= n; i++) {
    const date = new Date(start.getFullYear(), start.getMonth(), start.getDate() + i);
    const key = dayKey(date);
    days.push({ key, date, count: counts.get(key) ?? 0 });
  }
  return days;
}

function Summary({ stats, preset }: { stats: Stats; preset: Preset }) {
  const days = daySeries(stats);
  const total = stats.threadsByDay.reduce((n, d) => n + d.count, 0);
  const projects = stats.threadsByProject.slice(0, TOP_PROJECTS);
  return (
    <div className="grid grid-cols-1 gap-3.5 md:grid-cols-[1fr_300px]">
      <Panel fill title="Threads per day" meta={`${total} in ${days.length}d`}>
        <DayBars days={days} />
      </Panel>
      <Panel title="Per project" meta={preset}>
        {projects.length === 0 ? (
          <p className="text-small text-muted">No threads in this window.</p>
        ) : (
          <dl className="grid grid-cols-[1fr_auto] gap-x-2.5 gap-y-1.5 text-detail">
            {projects.map((p) => (
              <div key={p.t3ProjectId} className="contents">
                <dt className="truncate" title={p.title}>
                  {p.title}
                </dt>
                <dd className="tabular text-right font-mono text-muted">{p.count}</dd>
              </div>
            ))}
          </dl>
        )}
      </Panel>
    </div>
  );
}

function DayBars({ days }: { days: Day[] }) {
  const first = days[0];
  const last = days[days.length - 1];
  if (first === undefined || last === undefined) return <p className="text-small text-muted">No threads in this window.</p>;
  const max = Math.max(...days.map((d) => d.count), 1);
  return (
    <div className="flex flex-1 flex-col">
      <ul aria-label="Threads per day" className={cx("flex min-h-16 flex-1 items-end", days.length > 60 ? "gap-0" : "gap-[3px]")}>
        {days.map((d) => {
          const text = `${shortDay(d.date)}: ${d.count} thread${d.count === 1 ? "" : "s"}`;
          return (
            <li key={d.key} title={text} className="flex h-full min-w-0 flex-1 items-end">
              <span className="sr-only">{text}</span>
              <span
                aria-hidden="true"
                className={cx("block w-full", d === last ? "bg-accent" : "bg-accent/70", d.count === 0 && "bg-line")}
                style={{ height: d.count === 0 ? "1px" : `${Math.max((d.count / max) * 100, 3)}%` }}
              />
            </li>
          );
        })}
      </ul>
      <div className="mt-1.5 flex justify-between font-mono text-label text-muted">
        <span>{axisLabel(first.date)}</span>
        {first === last ? null : <span>{axisLabel(last.date)}</span>}
      </div>
    </div>
  );
}

// ---- Thread list ----

const COLS = "md:grid-cols-[64px_minmax(0,1fr)_110px_120px_120px_56px_52px]";

function ThreadList({ data }: { data: Thread[] }) {
  if (data.length === 0) {
    return (
      <Panel>
        <p className="text-small text-muted">No threads match. Widen the time window or clear filters.</p>
      </Panel>
    );
  }
  return (
    <section aria-label="Threads" className="border border-line bg-surface">
      <div className={cx("hidden items-center gap-3 border-b border-line bg-panel px-3 py-1.5 md:grid", COLS)}>
        {["created", "title", "project", "status", "model", "source"].map((h) => (
          <span key={h} className="label-caps">
            {h}
          </span>
        ))}
        <span className="sr-only">copy link, open in T3</span>
      </div>
      <ul>
        {data.map((t) => (
          <ThreadRow key={t.t3ThreadId} t={t} />
        ))}
      </ul>
    </section>
  );
}

function ThreadRow({ t }: { t: Thread }) {
  const title = <Link to={sessionPath(t.t3ThreadId)} className="font-medium hover:underline">{t.title}</Link>;
  const project =
    t.projectId === null ? (
      <span className="font-mono text-meta text-muted">{t.t3ProjectId}</span>
    ) : (
      <Link to={`/projects/${t.projectId}`} className="hover:underline">
        {t.projectTitle ?? t.t3ProjectId}
      </Link>
    );
  const model = t.model ?? t.providerInstanceId ?? "-";
  const subagent = t.parentThreadId === null ? null : <span className="ml-1.5 text-meta-sm font-normal text-muted">subagent</span>;
  const pulls = t.pullRequests.length === 0 ? null : (
    <span className="shrink-0">
      <PrChips prs={t.pullRequests} detailTo={sessionPath(t.t3ThreadId)} />
    </span>
  );
  const when = (
    <time dateTime={t.createdAt} title={dateTime(t.createdAt)}>
      {compactTime(t.createdAt)}
    </time>
  );
  return (
    <li
      className={cx(
        "group/row border-b border-line px-3 py-2 text-small last:border-b-0 hover:bg-panel md:grid md:items-center md:gap-3",
        COLS,
      )}
    >
      {/* laptop cells */}
      <span className="hidden font-mono text-meta text-muted md:block">{when}</span>
      <span className="hidden min-w-0 items-center gap-2 md:flex">
        <span className="min-w-0 truncate text-ui" title={t.title}>
          {title}
          {subagent}
        </span>
        {pulls}
      </span>
      <span className="hidden min-w-0 truncate md:block">{project}</span>
      <span className="hidden md:block">
        <ThreadStatusBadge status={t.status} settled={t.settled} />
      </span>
      <span className="hidden min-w-0 truncate font-mono text-meta text-muted md:block" title={model}>
        {model}
      </span>
      <span className="hidden font-mono text-meta text-muted md:block">{t.source}</span>
      <span className="hidden items-center justify-end gap-1 md:flex">
        <RowCopyLink subject={{ kind: "session", id: t.t3ThreadId, title: t.title }} />
        <T3Link t={t} />
      </span>

      {/* phone stack */}
      <div className="flex flex-col gap-1 md:hidden">
        <div className="flex items-start gap-2">
          <span className="min-w-0 flex-1 text-ui break-words">
            {title}
            {subagent}
          </span>
          <ThreadStatusBadge status={t.status} settled={t.settled} />
        </div>
        <div className="flex flex-wrap items-center gap-x-2.5 gap-y-0.5 font-mono text-meta-sm text-muted">
          {when}
          <span className="min-w-0 truncate">{project}</span>
          <span className="min-w-0 truncate">{model}</span>
          <span>{t.source}</span>
          {pulls}
          <span className="ml-auto">
            <T3Link t={t} />
          </span>
        </div>
      </div>
    </li>
  );
}

function T3Link({ t }: { t: Thread }) {
  if (t.t3DeepLink === null) return null;
  return (
    <a
      href={t.t3DeepLink}
      title="Open in app"
      aria-label={`Open "${t.title}" in app`}
      className="inline-flex text-muted hover:text-fg"
    >
      <IconExternal size={14} />
    </a>
  );
}

function Pager({ page, total, onPage }: { page: number; total: number; onPage: (p: number) => void }) {
  const first = total === 0 ? 0 : page * PAGE_SIZE + 1;
  const last = Math.min(total, (page + 1) * PAGE_SIZE);
  return (
    <div className="flex items-center justify-between gap-2">
      <span className="tabular font-mono text-meta text-muted">
        {first}-{last} of {total}
      </span>
      <div className="flex gap-2">
        <Button small disabled={page === 0} onClick={() => onPage(page - 1)}>
          Prev
        </Button>
        <Button small disabled={last >= total} onClick={() => onPage(page + 1)}>
          Next
        </Button>
      </div>
    </div>
  );
}
