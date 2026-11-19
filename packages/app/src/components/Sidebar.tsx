import { useState, type ReactNode } from "react";
import { Link, NavLink, useLocation, useNavigate } from "react-router";
import { useProjects, useTasks } from "../api/queries.ts";
import { EMPTY_LOAD, LIVE_STATES, projectLoads, sidebarProjects, totalLoad, type ProjectLoad } from "../lib/scope.ts";
import { IconChevronDown, IconMore, IconPin } from "./icons.tsx";
import { useScope } from "./Scope.tsx";
import { activeRowClass, cx } from "./ui.tsx";

/** Views that follow the scope; picking a project anywhere else opens the Dashboard. */
const SCOPED_PATHS = ["/", "/tasks", "/schedules"];

/** Open work per project, from the same query the unscoped Dashboard reads. */
export function useProjectLoads(): Map<number, ProjectLoad> {
  const tasks = useTasks({ state: [...LIVE_STATES] });
  return projectLoads(tasks.data?.tasks ?? []);
}

/** What the scope asks of the user: needs input, and everything the Dashboard lists. */
export function useScopeAttention(): { need: number; total: number } {
  const loads = useProjectLoads();
  const { projectId } = useScope();
  const load = projectId === null ? totalLoad(loads.values()) : (loads.get(projectId) ?? EMPTY_LOAD);
  return { need: load.need, total: load.need + load.waiting };
}

/** Sidebar nav row style: the active row box when current (`fill` false: the row's parent paints it). */
export function navRowClass(active: boolean, touch = false, fill = true): string {
  return cx(
    "flex w-full min-w-0 items-center gap-2.5 px-2.5 text-left text-ui transition-colors duration-150",
    touch ? "min-h-11" : "min-h-8",
    active ? "font-medium text-fg" : "text-muted hover:text-fg",
    fill && (active ? activeRowClass : "hover:bg-panel"),
  );
}

/** A row's open work: a run square while anything runs, then needs input (yellow) and the rest the user owes. */
function LoadMarks({ load }: { load: ProjectLoad }) {
  return (
    <span className="ml-auto flex shrink-0 items-center gap-2 font-mono text-meta-sm tabular">
      {load.run > 0 ? <span title={`${load.run} running`} aria-label={`${load.run} running`} className="size-1.5 bg-run" /> : null}
      {load.need > 0 ? (
        <span title={`${load.need} need input`} className="inline-flex items-center gap-1 text-need-fg">
          <span aria-hidden className="size-1.5 bg-need" />
          {load.need}
        </span>
      ) : null}
      {load.waiting > 0 ? (
        <span title={`${load.waiting} to review, start or retry`} className="text-muted">
          {load.waiting}
        </span>
      ) : null}
    </span>
  );
}

function ProjectItem({
  label,
  mono = true,
  selected,
  load,
  onSelect,
  pin,
  touch,
}: {
  label: string;
  mono?: boolean;
  selected: boolean;
  load: ProjectLoad;
  onSelect: () => void;
  pin?: { pinned: boolean; toggle: () => void } | undefined;
  touch: boolean;
}) {
  return (
    <li className={cx("group/project flex items-stretch", selected ? activeRowClass : "hover:bg-panel")}>
      <button
        type="button"
        aria-pressed={selected}
        onClick={onSelect}
        className={cx(navRowClass(selected, touch, false), "flex-1")}
      >
        <span className={cx("min-w-0 flex-1 truncate", mono && "font-mono text-detail")}>{label}</span>
        <LoadMarks load={load} />
      </button>
      {pin === undefined ? (
        <span aria-hidden className="w-7 shrink-0" />
      ) : (
        <button
          type="button"
          aria-pressed={pin.pinned}
          aria-label={`${pin.pinned ? "Unpin" : "Pin"} ${label}`}
          title={pin.pinned ? "Unpin" : "Pin to top"}
          onClick={pin.toggle}
          className={cx(
            "flex w-7 shrink-0 items-center justify-center transition-[color,opacity] duration-150 hover:text-fg focus-visible:opacity-100",
            pin.pinned ? "text-fg" : "text-muted",
            !touch && !pin.pinned && "opacity-0 group-hover/project:opacity-100",
          )}
        >
          <IconPin />
        </button>
      )}
    </li>
  );
}

function SectionLabel({ children, action }: { children: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex min-h-7 items-center justify-between px-2.5 pt-3 pb-1">
      <span className="label-caps text-muted">{children}</span>
      {action}
    </div>
  );
}

/**
 * The scope picker: All projects, pinned projects, projects with open work (most recent activity first), and the
 * quiet rest folded. Rows carry their open work. `touch` sizes rows for the phone sheet and always shows pins.
 */
export function ProjectNav({ onPick, touch = false }: { onPick?: () => void; touch?: boolean }) {
  const scope = useScope();
  const projects = useProjects();
  const loads = useProjectLoads();
  const navigate = useNavigate();
  const location = useLocation();
  const [showQuiet, setShowQuiet] = useState(false);
  const lists = sidebarProjects(projects.data?.projects ?? [], loads, scope.pinned, scope.projectId);

  const pick = (id: number | null) => {
    scope.setProjectId(id);
    if (!SCOPED_PATHS.includes(location.pathname)) void navigate("/");
    onPick?.();
  };
  const item = (p: { id: number; title: string }) => (
    <ProjectItem
      key={p.id}
      label={p.title}
      selected={scope.projectId === p.id}
      load={loads.get(p.id) ?? EMPTY_LOAD}
      onSelect={() => pick(p.id)}
      pin={{ pinned: scope.pinned.includes(p.id), toggle: () => scope.togglePinned(p.id) }}
      touch={touch}
    />
  );

  return (
    <nav aria-label="Projects" className="flex flex-col">
      <ul>
        <ProjectItem
          label="All projects"
          mono={false}
          selected={scope.projectId === null}
          load={totalLoad(loads.values())}
          onSelect={() => pick(null)}
          touch={touch}
        />
      </ul>
      {lists.pinned.length === 0 ? null : (
        <>
          <SectionLabel>Pinned</SectionLabel>
          <ul>{lists.pinned.map(item)}</ul>
        </>
      )}
      <SectionLabel
        action={
          <Link
            to="/projects"
            onClick={onPick}
            title="Manage projects"
            aria-label="Manage projects"
            className="flex size-6 items-center justify-center text-muted hover:bg-panel hover:text-fg"
          >
            <IconMore size={14} />
          </Link>
        }
      >
        Projects
      </SectionLabel>
      <ul>{lists.active.map(item)}</ul>
      {lists.active.length === 0 && lists.quiet.length === 0 && projects.data !== undefined ? (
        <p className="px-2.5 py-1 text-small text-muted">No projects yet.</p>
      ) : null}
      {lists.quiet.length === 0 ? null : (
        <>
          <button
            type="button"
            aria-expanded={showQuiet}
            onClick={() => setShowQuiet((v) => !v)}
            className={cx(
              "flex items-center gap-1.5 px-2.5 font-mono text-meta text-muted hover:text-fg",
              touch ? "min-h-11" : "min-h-8",
            )}
          >
            <span aria-hidden className={cx("inline-flex transition-transform duration-150", !showQuiet && "-rotate-90")}>
              <IconChevronDown size={12} />
            </span>
            {lists.quiet.length} quiet project{lists.quiet.length === 1 ? "" : "s"}
          </button>
          {showQuiet ? <ul>{lists.quiet.map(item)}</ul> : null}
        </>
      )}
    </nav>
  );
}

/** A main view in the sidebar: icon, label and an optional count. */
export function SideLink({ to, icon, label, count }: { to: string; icon: ReactNode; label: string; count?: ReactNode }) {
  return (
    <NavLink to={to} end={to === "/"} className={({ isActive }) => navRowClass(isActive)}>
      <span className="flex shrink-0">{icon}</span>
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {count}
    </NavLink>
  );
}

/** Dashboard's count: what the scope asks of the user, need-toned while anything needs input. */
export function AttentionCount({ need, total }: { need: number; total: number }) {
  if (total === 0) return null;
  return (
    <span
      title={`${total} need${total === 1 ? "s" : ""} you${need > 0 ? `, ${need} waiting for input` : ""}`}
      className={cx("inline-flex items-center gap-1.5 font-mono text-meta-sm tabular", need > 0 ? "text-need-fg" : "text-muted")}
    >
      {need > 0 ? <span aria-hidden className="size-1.5 bg-need" /> : null}
      {total}
    </span>
  );
}
