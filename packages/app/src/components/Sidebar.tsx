import { useState, type ReactNode } from "react";
import { Link, NavLink, useLocation, useNavigate } from "react-router";
import { useProjects, useTasks } from "../api/queries.ts";
import { capCount } from "../lib/format.ts";
import { EMPTY_LOAD, LIVE_STATES, projectLoads, sidebarProjects, totalLoad, type ProjectLoad } from "../lib/scope.ts";
import { IconMore, IconPin } from "./icons.tsx";
import { useScope } from "./Scope.tsx";
import { DisclosureButton, EmptyState, IconButton, Meta, Skeleton, SkeletonBar, SubHeading, activeRowClass, cx, iconButtonClass } from "./ui.tsx";

/** Views that follow the scope; picking a project anywhere else opens the Dashboard. */
const SCOPED_PATHS = ["/", "/tasks", "/trees", "/schedules"];

/** Open work per project, from the same query the unscoped Dashboard reads. */
export function useProjectLoads(): Map<number, ProjectLoad> {
  const tasks = useTasks({ state: [...LIVE_STATES] });
  return projectLoads(tasks.data?.tasks ?? []);
}

/** The scope's open work: All projects' total, or the scoped project's load. */
export function useScopeLoad(): ProjectLoad {
  const loads = useProjectLoads();
  const { projectId } = useScope();
  return projectId === null ? totalLoad(loads.values()) : (loads.get(projectId) ?? EMPTY_LOAD);
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

/**
 * A row's open work as one neutral count in a fixed 2ch slot, so counts line up down the sidebar; nothing for a row with
 * none, so quiet names keep their room. The breakdown is the tooltip and screen-reader text.
 */
export function LoadCount({ load }: { load: ProjectLoad }) {
  if (load.open === 0) return null;
  const count = capCount(load.open);
  // A capped count's "+" hangs past the two-digit slot, so "99+" keeps the column.
  const hang = count.endsWith("+");
  const backlog = load.open - load.need - load.waiting - load.run;
  const label = [
    load.need > 0 ? `${load.need} need input` : null,
    load.waiting > 0 ? `${load.waiting} to review, start or retry` : null,
    load.run > 0 ? `${load.run} running` : null,
    backlog > 0 ? `${backlog} in backlog` : null,
  ]
    .filter((p) => p !== null)
    .join(", ");
  return (
    <Meta small title={label} className="ml-auto flex shrink-0 tabular">
      <span aria-hidden className="relative min-w-[2ch] text-right">
        {hang ? count.slice(0, -1) : count}
        {hang ? <span className="absolute left-full">+</span> : null}
      </span>
      <span className="sr-only">{label}</span>
    </Meta>
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
        <LoadCount load={load} />
      </button>
      {pin === undefined ? (
        <span aria-hidden className="w-control-sm shrink-0" />
      ) : (
        <IconButton
          small
          icon={IconPin}
          aria-pressed={pin.pinned}
          label={`${pin.pinned ? "Unpin" : "Pin"} ${label}`}
          title={pin.pinned ? "Unpin" : "Pin to top"}
          onClick={pin.toggle}
          className={cx(
            "self-center focus-visible:opacity-100",
            !touch && !pin.pinned && "opacity-0 group-hover/project:opacity-100",
          )}
        />
      )}
    </li>
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
          <SubHeading title="Pinned" className="px-2.5 pt-3 pb-1" />
          <ul>{lists.pinned.map(item)}</ul>
        </>
      )}
      <SubHeading
        title="Projects"
        className="px-2.5 pt-3 pb-1"
        actions={
          <Link
            to="/projects"
            onClick={onPick}
            title="Manage projects"
            aria-label="Manage projects"
            className={iconButtonClass("ghost", true)}
          >
            <IconMore size={14} />
          </Link>
        }
      />
      <ul>{lists.active.map(item)}</ul>
      {projects.isPending ? (
        <Skeleton label="Loading projects">
          {["w-14", "w-10", "w-20"].map((w) => (
            <div key={w} className={cx("flex items-center px-2.5", touch ? "min-h-11" : "min-h-8")}>
              <SkeletonBar className={cx("h-3", w)} />
            </div>
          ))}
        </Skeleton>
      ) : null}
      {lists.active.length === 0 && lists.quiet.length === 0 && projects.data !== undefined ? (
        <EmptyState className="px-2.5 py-1">No projects yet.</EmptyState>
      ) : null}
      {lists.quiet.length === 0 ? null : (
        <>
          <DisclosureButton
            open={showQuiet}
            onClick={() => setShowQuiet((v) => !v)}
            className={cx("px-2.5 font-mono text-meta text-muted hover:text-fg", touch ? "min-h-11" : "min-h-8")}
          >
            {lists.quiet.length} quiet project{lists.quiet.length === 1 ? "" : "s"}
          </DisclosureButton>
          {showQuiet ? <ul>{lists.quiet.map(item)}</ul> : null}
        </>
      )}
    </nav>
  );
}

/** A main view in the sidebar: icon, label and an optional count, its right edge on the project rows' counts. */
export function SideLink({ to, icon, label, count }: { to: string; icon: ReactNode; label: string; count?: ReactNode }) {
  return (
    <NavLink to={to} end={to === "/"} className={({ isActive }) => navRowClass(isActive)}>
      <span className="flex shrink-0">{icon}</span>
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {count === undefined ? null : <span className="mr-control-sm flex shrink-0">{count}</span>}
    </NavLink>
  );
}
