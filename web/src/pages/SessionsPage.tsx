import { Link, useSearchParams } from "react-router";
import { useThreads } from "../api/queries.ts";
import { TASK_STATES } from "../api/types.ts";
import { pick } from "../lib/format.ts";
import { ProjectSelect } from "../components/ProjectSelect.tsx";
import { SessionPager, SessionRows } from "../components/SessionList.tsx";
import { taskStateLabel } from "../components/StateBadge.tsx";
import { Field, Input, PageHeader, Panel, QueryState, Segmented, Select, buttonClass } from "../components/ui.tsx";

const PAGE_SIZE = 30;
const VIEWS: { value: "active" | "all"; label: string }[] = [{ value: "active", label: "Active" }, { value: "all", label: "All" }];

export function SessionsPage() {
  const [params, setParams] = useSearchParams();
  const view = params.get("view") === "all" ? "all" : "active";
  const project = params.get("project") ?? "";
  const q = params.get("q") ?? "";
  const state = pick(TASK_STATES, params.get("state") ?? "");
  const rawPage = Number(params.get("page"));
  const page = Number.isSafeInteger(rawPage) && rawPage > 0 ? rawPage : 0;
  function update(patch: Record<string, string>) {
    const next = new URLSearchParams(params);
    next.delete("page");
    for (const [key, value] of Object.entries(patch)) {
      if (value === "") next.delete(key);
      else next.set(key, value);
    }
    setParams(next, { replace: true });
  }
  const sessions = useThreads({
    subagents: "exclude", sort: "activity", limit: PAGE_SIZE, offset: page * PAGE_SIZE,
    ...(view === "active" ? { active: true } : {}),
    ...(project === "" ? {} : { project: Number(project) }),
    ...(q === "" ? {} : { q }), ...(state === undefined ? {} : { state: [state] }),
  });
  return (
    <>
      <PageHeader title="Sessions" meta="Across projects · recent activity first" actions={<Link to="/history" className={buttonClass()}>History</Link>} />
      <div className="flex flex-col gap-3.5">
        <div className="flex flex-wrap items-end gap-3">
          <Segmented label="Session view" options={VIEWS} value={view} onChange={(value) => update({ view: value === "active" ? "" : value })} />
          <Field label="Project">{(id) => <ProjectSelect id={id} value={project} onChange={(value) => update({ project: value })} />}</Field>
          <Field label="Status">{(id) => <Select id={id} value={state ?? ""} onChange={(event) => update({ state: event.target.value })}>
            <option value="">Any status</option>
            {TASK_STATES.filter((value) => value !== "backlog").map((value) => <option key={value} value={value}>{taskStateLabel(value)}</option>)}
          </Select>}</Field>
          <Field label="Search sessions" className="min-w-40 flex-1">{(id) => <Input id={id} type="search" placeholder="Search titles across projects" value={q} onChange={(event) => update({ q: event.target.value })} />}</Field>
        </div>
        <p className="text-small text-muted">{view === "active" ? "Running sessions and sessions needing input, regardless of when they were created." : "All top-level sessions, including finished and settled work. Inspect children from their parent session."}</p>
        <QueryState isPending={sessions.isPending} error={sessions.error}>
          {sessions.data === undefined ? null : <>
            <Panel title={view === "active" ? "Active sessions" : "All sessions"} flush meta={sessions.isFetching ? "updating" : `${sessions.data.total}`}>
              <SessionRows threads={sessions.data.threads} emptyText={view === "active" && q === "" && project === "" && state === undefined ? "Nothing running or needing input. Switch to All to find a finished session." : "No sessions match. Clear filters or try another search."} />
            </Panel>
            <SessionPager page={page} total={sessions.data.total} size={PAGE_SIZE} disabled={sessions.isPlaceholderData} onPage={(value) => update({ page: value === 0 ? "" : String(value) })} />
          </>}
        </QueryState>
      </div>
    </>
  );
}
