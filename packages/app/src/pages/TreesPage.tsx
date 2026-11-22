import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { useTree, useTrees } from "../api/queries.ts";
import type { TreeSummary } from "../api/types.ts";
import { useScope } from "../components/Scope.tsx";
import { GroupPanel, StatStrip, useGroupFold, type GroupMeta } from "../components/TaskGroups.tsx";
import { TrackSessionButton } from "../components/trees/AdoptSessionDrawer.tsx";
import { TreeRow, TreeRowsSkeleton } from "../components/trees/TreeRow.tsx";
import { TreeView, TreeViewSkeleton } from "../components/trees/TreeView.tsx";
import { DisclosureButton, EmptyState, ErrorText, PageHeader, Panel, QueryState, Row, RowList, Segmented, buttonClass } from "../components/ui.tsx";
import { projectWorkspace } from "../lib/projects.ts";
import { settledSince } from "../lib/settled.ts";
import { TREE_GROUP_KEYS, byTreeGroup, parseTreeGroup, treeClosed, treeGroup, type TreeGroupKey } from "../lib/trees.ts";

/** Open: trees with work left (with the last 7 days completed); Closed: done, completed or archived trees. */
type View = "open" | "closed" | "all";

const VIEWS: { value: View; label: string }[] = [
  { value: "open", label: "Open" },
  { value: "closed", label: "Closed" },
  { value: "all", label: "All" },
];

const parseView = (v: string | null): View => VIEWS.find((o) => o.value === v)?.value ?? "open";

/** Stat strip cells and filtered panel titles; a tree counts under its most pressing next step. */
const TREE_GROUP_META: Record<TreeGroupKey, GroupMeta> = {
  needs: { title: "Needs input", stat: "Needs input", short: "need", tone: "need", empty: "No tree is waiting on you." },
  done: { title: "Awaiting review", stat: "Review", short: "review", tone: "done", empty: "No tree has finished runs to review." },
  ready: { title: "Ready", stat: "Ready", short: "ready", tone: "idle", empty: "No tree has tasks ready to start." },
  failed: { title: "Failed / interrupted", stat: "Failed", short: "fail", tone: "bad", empty: "No tree has failed or interrupted tasks." },
  running: { title: "Running", stat: "Running", short: "run", tone: "run", empty: "No tree is running." },
  waiting: { title: "Waiting", stat: "Waiting", short: "wait", tone: "idle", empty: "No tree is waiting on needs." },
  settled: { title: "Completed · last 7 days", stat: "Completed", short: "done", tone: "ok", empty: "No tree completed in the last 7 days." },
};

const FOLD_KEY = "stage-trees-groups";

export function TreesPage() {
  const [params, setParams] = useSearchParams();
  const view = parseView(params.get("view"));
  const scope = useScope();
  const setParam = (key: string, value: string | null) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (value === null) next.delete(key);
        else next.set(key, value);
        return next;
      },
      { replace: true },
    );

  return (
    <>
      <PageHeader
        title={scope.project === null ? "Trees" : <><span className="text-muted">{scope.project.title} / </span>Trees</>}
        meta={scope.project === null ? "Plans of dependent tasks across projects" : projectWorkspace(scope.project)}
        actions={
          <>
            <TrackSessionButton />
            {scope.project === null ? null : (
              <Link to={`/projects/${scope.project.id}`} className={buttonClass("secondary")}>
                Project settings
              </Link>
            )}
          </>
        }
      />
      <div className="mb-3.5 flex flex-wrap items-center gap-x-3 gap-y-2">
        <Segmented label="Trees to show" options={VIEWS} value={view} onChange={(v) => setParam("view", v === "open" ? null : v)} />
      </div>
      {view === "open" ? (
        <OpenTrees selected={parseTreeGroup(params.get("state"))} onSelect={(k) => setParam("state", k)} />
      ) : (
        <ClosedTrees view={view} />
      )}
    </>
  );
}

/** Open trees: Active (most pressing first) and Completed in the window; a stat cell filters to one group. */
function OpenTrees({ selected, onSelect }: { selected: TreeGroupKey | null; onSelect: (key: string | null) => void }) {
  const scope = useScope();
  const [since] = useState(() => settledSince());
  // Trees with work left, plus trees completed inside the window.
  const trees = useTrees({ settledFrom: since, ...scope.query });
  const fold = useGroupFold(FOLD_KEY);

  const open = (trees.data?.trees ?? []).filter((t) => t.status === "open");
  const counts: Record<TreeGroupKey, number> = { needs: 0, done: 0, ready: 0, failed: 0, running: 0, waiting: 0, settled: 0 };
  for (const t of open) counts[treeGroup(t)]++;
  const active = byTreeGroup(open.filter((t) => treeGroup(t) !== "settled"));
  const completed = open.filter((t) => treeGroup(t) === "settled").toSorted((a, b) => (b.settledAt ?? "").localeCompare(a.settledAt ?? ""));
  const collapsed = fold.folded("settled", []);

  return (
    <QueryState
      isPending={trees.isPending}
      error={trees.error}
      hasData={trees.data !== undefined}
      stale={trees.isPlaceholderData}
      skeleton={
        <>
          <StatStrip groups={TREE_GROUP_KEYS} meta={TREE_GROUP_META} counts={null} selected={selected} onToggle={(k) => onSelect(selected === k ? null : k)} />
          <Panel flush title="Active">
            <TreeRowsSkeleton />
          </Panel>
        </>
      }
    >
      <StatStrip
        groups={TREE_GROUP_KEYS}
        meta={TREE_GROUP_META}
        counts={counts}
        selected={selected}
        onToggle={(k) => onSelect(selected === k ? null : k)}
      />
      <div className="flex flex-col gap-3.5">
        {selected === null ? (
          <>
            <Panel flush title="Active" meta={<span className="tabular">{active.length}</span>}>
              <TreeList trees={active} emptyText="No active trees. Let an agent plan one, or pick New tree from a session's batch." />
            </Panel>
            {completed.length === 0 ? null : (
              <GroupPanel
                info={TREE_GROUP_META.settled}
                count={completed.length}
                collapsed={collapsed}
                onToggle={() => fold.toggle("settled", [])}
              >
                <TreeList trees={completed} emptyText={TREE_GROUP_META.settled.empty} />
              </GroupPanel>
            )}
          </>
        ) : (
          <GroupPanel info={TREE_GROUP_META[selected]} count={counts[selected]}>
            <TreeList
              trees={selected === "settled" ? completed : active.filter((t) => treeGroup(t) === selected)}
              emptyText={TREE_GROUP_META[selected].empty}
            />
          </GroupPanel>
        )}
      </div>
    </QueryState>
  );
}

/** Closed or all trees in one panel, most recent activity first. */
function ClosedTrees({ view }: { view: "closed" | "all" }) {
  const scope = useScope();
  const trees = useTrees({ status: "all", ...scope.query });
  // Closed trees: marked done, completed, or archived.
  const list = (trees.data?.trees ?? []).filter((t) => view === "all" || treeClosed(t) || t.settledAt !== null);
  const empty = view === "closed" ? "Nothing closed. Done, completed and archived trees land here." : "No trees yet. Let an agent plan one.";
  return (
    <QueryState
      isPending={trees.isPending}
      error={trees.error}
      hasData={trees.data !== undefined}
      stale={trees.isPlaceholderData}
      skeleton={
        <Panel flush title="Trees">
          <TreeRowsSkeleton />
        </Panel>
      }
    >
      <Panel flush title="Trees" meta={<span className="tabular">{list.length}</span>}>
        <TreeList trees={list} emptyText={empty} />
      </Panel>
    </QueryState>
  );
}

function TreeList({ trees, emptyText }: { trees: readonly TreeSummary[]; emptyText: string }) {
  if (trees.length === 0) return <EmptyState inset>{emptyText}</EmptyState>;
  return (
    <RowList>
      {trees.map((t) => (
        <TreeItem key={t.id} tree={t} />
      ))}
    </RowList>
  );
}

/** A tree row that expands to its members in level order. */
function TreeItem({ tree }: { tree: TreeSummary }) {
  const [open, setOpen] = useState(false);
  return (
    <Row bare>
      <div className="flex items-stretch">
        <DisclosureButton
          open={open}
          aria-label={`${open ? "Hide" : "Show"} tasks of ${tree.title}`}
          onClick={() => setOpen((o) => !o)}
          className="w-9 shrink-0 justify-center transition-colors duration-150 hover:bg-panel"
        />
        <div className="min-w-0 flex-1 [&>div]:pl-0">
          <TreeRow tree={tree} />
        </div>
      </div>
      {open ? <TreeMembers id={tree.id} /> : null}
    </Row>
  );
}

function TreeMembers({ id }: { id: number }) {
  const tree = useTree(id);
  const navigate = useNavigate();
  return (
    <div className="border-t border-line bg-bg">
      {tree.isPending ? (
        <TreeViewSkeleton />
      ) : tree.error !== null || tree.data === undefined ? (
        <div className="p-3">
          <ErrorText error={tree.error} />
        </div>
      ) : (
        <TreeView tree={tree.data} selected={null} onSelect={(task) => void navigate(`/tasks/${task}`)} />
      )}
    </div>
  );
}
