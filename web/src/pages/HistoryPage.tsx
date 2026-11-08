import { useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router";
import { useCatalog, useStats, useThreads } from "../api/queries.ts";
import { THREAD_STATUSES, type Stats, type Thread, type ThreadListQuery } from "../api/types.ts";
import { ProjectSelect } from "../components/ProjectSelect.tsx";
import { ThreadStatusBadge } from "../components/StateBadge.tsx";
import { Badge, Button, Card, ErrorText, Field, Input, Muted, PageHeader, Select, cx } from "../components/ui.tsx";
import { dateTime, localDateToIso, localMidnight, pick, relativeTime } from "../lib/format.ts";

const PAGE_SIZE = 50;
const PRESETS = ["today", "7d", "30d", "all", "custom"] as const;
type Preset = (typeof PRESETS)[number];
const PRESET_LABELS: Record<Preset, string> = { today: "Today", "7d": "7 days", "30d": "30 days", all: "All", custom: "Custom" };
const ALL_TIME_FROM = "2000-01-01";
const TZ = Intl.DateTimeFormat().resolvedOptions().timeZone;

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

export function HistoryPage() {
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

  const preset = pick(PRESETS, get("preset")) ?? "7d";
  const { from, to } = range(preset, get("from"), get("to"));
  const status = pick(THREAD_STATUSES, get("status"));
  const settled = get("settled");
  const source = get("source");
  const page = Math.max(0, Number(get("page")) || 0);

  const query: ThreadListQuery = {
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
    ...(from === undefined ? {} : { from }),
    ...(to === undefined ? {} : { to }),
    ...(get("project") === "" ? {} : { project: Number(get("project")) }),
    ...(status === undefined ? {} : { status: [status] }),
    ...(settled === "" ? {} : { settled: settled === "true" }),
    ...(get("provider") === "" ? {} : { provider: get("provider") }),
    ...(source === "app" || source === "t3" ? { source } : {}),
    ...(get("q") === "" ? {} : { q: get("q") }),
  };
  const threads = useThreads(query);
  // Stats default to the last 30 days when `from` is omitted; "all" needs an explicit early bound.
  const stats = useStats({ tz: TZ, from: from ?? ALL_TIME_FROM, ...(to === undefined ? {} : { to }) });

  return (
    <>
      <PageHeader title="History" subtitle="All T3 threads mirrored from every project." />
      <div className="flex flex-col gap-4">
        <Card>
          <div className="flex flex-col gap-3">
            <div className="flex flex-wrap items-end gap-2">
              <div className="flex overflow-hidden rounded-md border border-neutral-300 dark:border-neutral-700">
                {PRESETS.map((p) => (
                  <button
                    key={p}
                    type="button"
                    onClick={() => update({ preset: p === "7d" ? "" : p })}
                    className={cx(
                      "px-3 py-1.5 text-sm",
                      p === preset
                        ? "bg-neutral-900 text-white dark:bg-neutral-100 dark:text-neutral-900"
                        : "hover:bg-neutral-100 dark:hover:bg-neutral-800",
                    )}
                  >
                    {PRESET_LABELS[p]}
                  </button>
                ))}
              </div>
              {preset === "custom" ? (
                <>
                  <Field label="From">
                    {(id) => <Input id={id} type="date" value={get("from")} onChange={(e) => update({ from: e.target.value })} />}
                  </Field>
                  <Field label="To (inclusive)">
                    {(id) => <Input id={id} type="date" value={get("to")} onChange={(e) => update({ to: e.target.value })} />}
                  </Field>
                </>
              ) : null}
            </div>
            <div className="flex flex-wrap items-end gap-2">
              <ProjectSelect value={get("project")} onChange={(v) => update({ project: v })} />
              <Select value={status ?? ""} onChange={(e) => update({ status: e.target.value })} aria-label="Status">
                <option value="">Any status</option>
                {THREAD_STATUSES.map((s) => (
                  <option key={s} value={s}>
                    {s}
                  </option>
                ))}
              </Select>
              <Select value={settled} onChange={(e) => update({ settled: e.target.value })} aria-label="Settled">
                <option value="">Settled: any</option>
                <option value="true">Settled</option>
                <option value="false">Not settled</option>
              </Select>
              <ProviderSelect value={get("provider")} onChange={(v) => update({ provider: v })} />
              <Select value={source} onChange={(e) => update({ source: e.target.value })} aria-label="Source">
                <option value="">Any source</option>
                <option value="app">t3-stage tasks</option>
                <option value="t3">Other T3 threads</option>
              </Select>
              <SearchBox key={get("q")} initial={get("q")} onSearch={(q) => update({ q })} />
            </div>
          </div>
        </Card>
        <StatsPanel stats={stats.data} error={stats.error} />
        <ErrorText error={threads.error} />
        {threads.data === undefined ? (
          threads.isPending ? <Muted>Loading…</Muted> : null
        ) : (
          <>
            <ThreadTable data={threads.data.threads} />
            <Pager page={page} total={threads.data.total} onPage={(p) => update({ page: p === 0 ? "" : String(p) })} />
          </>
        )}
      </div>
    </>
  );
}

function SearchBox({ initial, onSearch }: { initial: string; onSearch: (q: string) => void }) {
  const [q, setQ] = useState(initial);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    onSearch(q.trim());
  };
  return (
    <form onSubmit={submit} className="flex min-w-52 flex-1 gap-2">
      <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search title…" aria-label="Search title" />
      <Button type="submit">Search</Button>
    </form>
  );
}

function ProviderSelect({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const providers = useCatalog().data?.providers ?? [];
  const known = providers.some((p) => p.providerInstanceId === value);
  return (
    <Select value={value} onChange={(e) => onChange(e.target.value)} aria-label="Provider">
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

function ThreadTable({ data }: { data: Thread[] }) {
  if (data.length === 0) return <Muted>No threads match.</Muted>;
  return (
    <div className="overflow-x-auto rounded-lg border border-neutral-200 bg-white dark:border-neutral-800 dark:bg-neutral-900">
      <table className="w-full text-left text-sm">
        <thead className="border-b border-neutral-200 text-xs text-neutral-500 dark:border-neutral-800">
          <tr>
            {["Created", "Title", "Project", "Status", "Settled", "Model", "Source"].map((h) => (
              <th key={h} className="px-3 py-2 font-medium whitespace-nowrap">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-neutral-200 dark:divide-neutral-800">
          {data.map((t) => (
            <tr key={t.t3ThreadId} className="align-top">
              <td className="px-3 py-2 whitespace-nowrap text-neutral-500" title={dateTime(t.createdAt)}>
                {relativeTime(t.createdAt)}
              </td>
              <td className="max-w-96 px-3 py-2">
                <div className="flex flex-wrap items-center gap-x-2">
                  {t.taskId === null ? (
                    <span className="font-medium">{t.title}</span>
                  ) : (
                    <Link to={`/tasks/${t.taskId}`} className="font-medium hover:underline">
                      {t.title}
                    </Link>
                  )}
                  {t.t3WebUrl === null ? null : (
                    <a href={t.t3WebUrl} target="_blank" rel="noreferrer" className="text-xs text-blue-600 hover:underline dark:text-blue-400">
                      T3
                    </a>
                  )}
                </div>
                {t.branch === null ? null : <div className="font-mono text-xs text-neutral-500">{t.branch}</div>}
              </td>
              <td className="px-3 py-2 whitespace-nowrap">
                {t.projectId === null ? (
                  <span className="font-mono text-xs text-neutral-500">{t.t3ProjectId}</span>
                ) : (
                  <Link to={`/projects/${t.projectId}`} className="hover:underline">
                    {t.projectTitle ?? t.t3ProjectId}
                  </Link>
                )}
              </td>
              <td className="px-3 py-2">
                <ThreadStatusBadge status={t.status} />
              </td>
              <td className="px-3 py-2">{t.settled ? <Badge tone="green">settled</Badge> : <span className="text-neutral-400">no</span>}</td>
              <td className="px-3 py-2 text-xs whitespace-nowrap text-neutral-500">
                {[t.providerInstanceId, t.model].filter((x) => x !== null).join(" / ") || "-"}
              </td>
              <td className="px-3 py-2">
                <Badge tone={t.source === "app" ? "blue" : "gray"}>{t.source}</Badge>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Pager({ page, total, onPage }: { page: number; total: number; onPage: (p: number) => void }) {
  const first = total === 0 ? 0 : page * PAGE_SIZE + 1;
  const last = Math.min(total, (page + 1) * PAGE_SIZE);
  return (
    <div className="flex items-center justify-between gap-2 text-sm text-neutral-500">
      <span>
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

function Bars({ rows, emptyText }: { rows: { key: string; label: string; count: number }[]; emptyText: string }) {
  if (rows.length === 0) return <Muted>{emptyText}</Muted>;
  const max = Math.max(...rows.map((r) => r.count), 1);
  return (
    <ul className="flex flex-col gap-1">
      {rows.map((r) => (
        <li key={r.key} className="grid grid-cols-[minmax(5rem,9rem)_1fr_3rem] items-center gap-2 text-xs">
          <span className="truncate text-neutral-600 dark:text-neutral-400" title={r.label}>
            {r.label}
          </span>
          <span className="h-3 rounded-sm bg-neutral-100 dark:bg-neutral-800">
            <span className="block h-full rounded-sm bg-blue-500" style={{ width: `${(r.count / max) * 100}%` }} />
          </span>
          <span className="text-right tabular-nums">{r.count}</span>
        </li>
      ))}
    </ul>
  );
}

function StatsPanel({ stats, error }: { stats: Stats | undefined; error: unknown }) {
  if (error !== null) return <ErrorText error={error} />;
  if (stats === undefined) return null;
  const total = stats.threadsByStatus.reduce((n, s) => n + s.count, 0);
  return (
    <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
      <Card title={`Threads per day (${total})`}>
        <div className="max-h-72 overflow-y-auto">
          <Bars rows={stats.threadsByDay.map((d) => ({ key: d.day, label: d.day, count: d.count }))} emptyText="No threads in range." />
        </div>
      </Card>
      <Card title="Threads per project">
        <div className="max-h-72 overflow-y-auto">
          <Bars
            rows={stats.threadsByProject.map((p) => ({ key: p.t3ProjectId, label: p.title, count: p.count }))}
            emptyText="No threads in range."
          />
        </div>
        {stats.threadsByStatus.length === 0 ? null : (
          <div className="mt-3 flex flex-wrap gap-2 border-t border-neutral-200 pt-3 dark:border-neutral-800">
            {stats.threadsByStatus.map((s) => (
              <span key={s.status} className="flex items-center gap-1 text-xs">
                <ThreadStatusBadge status={s.status} /> {s.count}
              </span>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}
