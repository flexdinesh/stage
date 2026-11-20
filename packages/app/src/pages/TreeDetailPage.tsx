import { useMutation } from "@tanstack/react-query";
import { useId, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { ApiError } from "../api/client.ts";
import { useInvalidateWork, useTree } from "../api/queries.ts";
import type { StartMode, TreeDetail, TreeStatus } from "../api/types.ts";
import { useApi } from "../platform/context.tsx";
import { IconBack, IconChevronDown, IconPlus } from "../components/icons.tsx";
import { useNewTask } from "../components/NewTask.tsx";
import { AddExistingTaskDrawer } from "../components/trees/AddExistingTaskDrawer.tsx";
import { AdoptSessionDrawer } from "../components/trees/AdoptSessionDrawer.tsx";
import { TreeActivity } from "../components/trees/TreeActivity.tsx";
import { JournalPanel } from "../components/Journal.tsx";
import { TreeNodePanel } from "../components/trees/TreeNodePanel.tsx";
import { TreeGraph } from "../components/trees/TreeGraph.tsx";
import { TreeView, TreeViewSkeleton } from "../components/trees/TreeView.tsx";
import { Alert, Button, Chip, ErrorText, Input, PageHeader, Panel, Segmented, buttonClass, cx } from "../components/ui.tsx";
import { useIdParam } from "../lib/params.ts";
import { dateTime, relativeTime, shortDate } from "../lib/format.ts";
import { closeBlocker, closedLabel, countMembers, pickedTask, treeClosed, treeMeta, treeProjectLabel, treeSettled } from "../lib/trees.ts";
import { NotFoundPage } from "./NotFoundPage.tsx";

const MODE_OPTIONS: { value: StartMode; label: string }[] = [
  { value: "auto", label: "Auto" },
  { value: "manual", label: "Manual" },
];

const VIEW_OPTIONS: { value: "tasks" | "notes"; label: string }[] = [
  { value: "tasks", label: "Tasks" },
  { value: "notes", label: "Notes" },
];

const LAYOUT_OPTIONS: { value: "list" | "graph"; label: string }[] = [
  { value: "list", label: "List" },
  { value: "graph", label: "Graph" },
];

export function TreeDetailPage() {
  const id = useIdParam();
  return id === null ? <NotFoundPage /> : <TreeDetailView id={id} />;
}

function TreeDetailView({ id }: { id: number }) {
  const tree = useTree(id);
  if (tree.isPending) return <TreeLoading />;
  if (tree.error instanceof ApiError && tree.error.status === 404) return <TreeNotFound id={id} />;
  if (tree.data === undefined) return <ErrorText error={tree.error} />;
  return <><ErrorText error={tree.error} /><TreeBody key={id} tree={tree.data} /></>;
}

function TreeLoading() {
  return (
    <>
      <div className="mb-4 flex flex-col gap-2" aria-hidden>
        <div className="h-7 w-64 animate-pulse bg-panel" />
        <div className="h-3.5 w-80 max-w-full animate-pulse bg-panel" />
      </div>
      <Panel title="Tree" flush>
        <TreeViewSkeleton />
      </Panel>
    </>
  );
}

function TreeNotFound({ id }: { id: number }) {
  return (
    <>
      <PageHeader title="Tree not found" meta={`tree ${id}`} />
      <p className="mb-3 text-ui text-muted">
        This tree was deleted or does not exist. Empty and dissolved trees remain under Closed in Tasks.
      </p>
      <Link to="/tasks" className={buttonClass("secondary")}>
        <IconBack /> Back to tasks
      </Link>
    </>
  );
}

type Drawer = "existing" | "session" | null;

type MenuItem = "rename" | "existing" | "session" | "done" | "archive" | "dissolve" | "delete";

/**
 * Overflow menu (popover): Rename, Add existing task, Add session, then Mark done, Archive tree, Dissolve tree and
 * Delete tree (only when no member started). Done and archive are disabled with the reason while a member is in
 * progress; a closed tree drops the structural items and the status it already has.
 */
function TreeMenu({ tree, onPick }: { tree: TreeDetail; onPick: (item: MenuItem) => void }) {
  const menuId = useId();
  const menu = useRef<HTMLDivElement>(null);
  const closed = treeClosed(tree);
  const blocker = closeBlocker(tree.tasks);
  const item = (key: MenuItem, label: string, opts: { danger?: boolean; reason?: string | null } = {}) => (
    <button
      type="button"
      disabled={opts.reason !== undefined && opts.reason !== null}
      onClick={() => {
        menu.current?.hidePopover();
        onPick(key);
      }}
      className={cx(
        "flex flex-col border-b border-line px-3 py-2.5 text-left text-ui last:border-b-0 hover:bg-panel disabled:hover:bg-transparent md:py-2",
        opts.danger === true && "text-bad",
      )}
    >
      <span className={cx(opts.reason !== undefined && opts.reason !== null && "opacity-45")}>{label}</span>
      {opts.reason === undefined || opts.reason === null ? null : (
        <span className="font-mono text-meta-sm text-muted">{opts.reason}</span>
      )}
    </button>
  );
  return (
    <>
      <button type="button" popoverTarget={menuId} className={cx(buttonClass("secondary"), "[anchor-name:--tree-more]")}>
        More
        <IconChevronDown size={12} />
      </button>
      <div
        ref={menu}
        id={menuId}
        popover="auto"
        className="m-0 mt-1 w-52 border border-line-strong bg-surface text-fg [position-anchor:--tree-more] [top:anchor(bottom)] [right:anchor(right)] [left:auto]"
      >
        <div className="flex flex-col">
          {item("rename", "Rename")}
          {closed ? null : item("existing", "Add existing task")}
          {closed ? null : item("session", "Add session")}
          <div aria-hidden className="-mt-px h-px bg-line-strong" />
          {tree.status === "done" ? null : item("done", "Mark done", { reason: blocker })}
          {tree.status === "archived" ? null : item("archive", "Archive tree", { reason: blocker })}
          {tree.tasks.length === 0 ? null : item("dissolve", "Dissolve tree", { danger: true })}
          {tree.deletable ? item("delete", "Delete tree", { danger: true }) : null}
        </div>
      </div>
    </>
  );
}

function RenameForm({ tree, onDone }: { tree: TreeDetail; onDone: () => void }) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const [title, setTitle] = useState(tree.title);
  const rename = useMutation({
    mutationFn: (next: string) => api.updateTree(tree.id, { title: next }),
    onSuccess: onDone,
    onSettled: invalidate,
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const next = title.trim();
    if (next === "" || next === tree.title) onDone();
    else rename.mutate(next);
  };
  return (
    <form onSubmit={submit} onKeyDown={(e) => e.key === "Escape" && onDone()} className="flex min-w-0 flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <Input aria-label="Tree title" autoFocus value={title} onChange={(e) => setTitle(e.target.value)} className="min-w-0 flex-1 md:max-w-md" />
        <Button type="submit" variant="primary" busy={rename.isPending} disabled={title.trim() === ""}>
          Save
        </Button>
        <Button variant="ghost" onClick={onDone}>
          Cancel
        </Button>
      </div>
      <ErrorText error={rename.error} />
    </form>
  );
}

function TreeBody({ tree }: { tree: TreeDetail }) {
  const api = useApi();
  const navigate = useNavigate();
  const invalidate = useInvalidateWork();
  const newTask = useNewTask();
  const [params, setParams] = useSearchParams();
  const view = params.get("view") === "notes" ? "notes" : "tasks";
  const setView = (next: "tasks" | "notes") => {
    const updated = new URLSearchParams(params);
    if (next === "notes") updated.set("view", "notes");
    else updated.delete("view");
    setParams(updated);
  };
  const layout = params.get("layout") === "graph" ? "graph" : "list";
  const setLayout = (next: "list" | "graph") => {
    const updated = new URLSearchParams(params);
    if (next === "graph") updated.set("layout", "graph");
    else updated.delete("layout");
    setParams(updated);
  };
  // The selected member lives in `?task=` so task detail's Back to tree returns to it.
  const picked = pickedTask(params.get("task"), tree.tasks);
  const setPicked = (next: number | null) => {
    const updated = new URLSearchParams(params);
    if (next === null) updated.delete("task");
    else updated.set("task", String(next));
    setParams(updated, { replace: true });
  };
  const [renaming, setRenaming] = useState(false);
  const [drawer, setDrawer] = useState<Drawer>(null);
  const [confirming, setConfirming] = useState<"dissolve" | "delete" | null>(null);
  const selected = tree.tasks.find((t) => t.id === picked) ?? null;
  const readyCount = tree.tasks.filter((t) => t.readiness === "ready" && !t.adopted).length;
  const settled = treeSettled(tree.tasks);
  const closed = treeClosed(tree);
  const closedChip = closedLabel(tree, countMembers(tree.tasks));

  const start = useMutation({ mutationFn: () => api.startTree(tree.id), onSettled: invalidate });
  const mode = useMutation({ mutationFn: (startMode: StartMode) => api.updateTree(tree.id, { startMode }), onSettled: invalidate });
  const status = useMutation({ mutationFn: (next: TreeStatus) => api.updateTree(tree.id, { status: next }), onSettled: invalidate });
  const leave = async () => {
    await navigate("/tasks");
    await invalidate();
  };
  const dissolve = useMutation({
    mutationFn: () => api.dissolveTree(tree.id),
    onSuccess: async () => {
      setConfirming(null);
      setPicked(null);
      await invalidate();
    },
  });
  const remove = useMutation({ mutationFn: () => api.deleteTree(tree.id), onSuccess: leave });
  const startFailures = start.data?.errors ?? [];

  const addTask = () => {
    const anchor = selected ?? tree.tasks[0];
    newTask(anchor?.projectId, undefined, {
      treeId: tree.id,
      treeTitle: tree.title,
      needs: selected === null ? [] : [selected.id],
      startMode: tree.startMode,
    });
  };

  const onMenu = (item: MenuItem) => {
    if (item === "rename") setRenaming(true);
    else if (item === "done") status.mutate("done");
    else if (item === "archive") status.mutate("archived");
    else if (item === "dissolve" || item === "delete") setConfirming(item);
    else setDrawer(item);
  };

  return (
    <>
      {renaming ? (
        <div className="mb-4">
          <RenameForm tree={tree} onDone={() => setRenaming(false)} />
        </div>
      ) : null}
      <PageHeader
        title={renaming ? <span className="sr-only">{tree.title}</span> : tree.title}
        meta={
          <>
            <span className="text-fg [overflow-wrap:anywhere]">{treeProjectLabel(tree)}</span>
            <span className="mt-1 block">
              {treeMeta(tree)} · <span title={`Created ${dateTime(tree.createdAt)}`}>created {shortDate(tree.createdAt)}</span> ·{" "}
              <span title={`Last activity ${dateTime(tree.lastActivityAt)}`} className="text-fg">
                active {relativeTime(tree.lastActivityAt)}
              </span>
              {settled ? (
                <Chip tone="ok" dot className="ml-2">
                  completed
                </Chip>
              ) : null}
              {closedChip === null ? null : <Chip className="ml-2">{closedChip}</Chip>}
            </span>
          </>
        }
        actions={
          closed ? (
            <>
              <TreeMenu tree={tree} onPick={onMenu} />
              <Button busy={status.isPending} onClick={() => status.mutate("open")}>
                {tree.status === "archived" ? "Unarchive" : "Reopen"}
              </Button>
            </>
          ) : (
            <>
              <Segmented label="Tree start mode" options={MODE_OPTIONS} value={tree.startMode} onChange={(m) => m !== tree.startMode && mode.mutate(m)} />
              <TreeMenu tree={tree} onPick={onMenu} />
              <Button onClick={addTask} title={selected === null ? "New parallel root. Select a task to add after it." : `New task that runs after #${selected.id}`}>
                <IconPlus size={14} />
                {selected === null ? "Add task" : `Add task after #${selected.id}`}
              </Button>
              <Button
                variant="primary"
                busy={start.isPending}
                disabled={readyCount === 0}
                title={readyCount === 0 ? "No task is ready" : `Starts ${readyCount} ready task${readyCount === 1 ? "" : "s"}`}
                onClick={() => start.mutate()}
              >
                Start ready <span className="font-mono tabular">{readyCount}</span>
              </Button>
            </>
          )
        }
      />
      {tree.closedAt === null || !closed ? null : (
        <p className="-mt-2 mb-4 font-mono text-meta text-muted" title={dateTime(tree.closedAt)}>
          {tree.status === "archived" ? "Archived" : "Marked done"} {shortDate(tree.closedAt)}. Remaining tasks won&apos;t start.
        </p>
      )}

      <div className="flex flex-col gap-3.5">
        {confirming === "dissolve" ? (
          <Alert
            tone="bad"
            actions={
              <>
                <Button variant="danger" small busy={dissolve.isPending} onClick={() => dissolve.mutate()}>
                  Dissolve
                </Button>
                <Button variant="ghost" small onClick={() => setConfirming(null)}>
                  Cancel
                </Button>
              </>
            }
          >
            Dissolve this tree? Its {tree.tasks.length} tasks stay in stage; their needs and tree membership go. The tree is archived and its history stays here.
          </Alert>
        ) : confirming === "delete" ? (
          <Alert
            tone="bad"
            actions={
              <>
                <Button variant="danger" small busy={remove.isPending} onClick={() => remove.mutate()}>
                  Delete
                </Button>
                <Button variant="ghost" small onClick={() => setConfirming(null)}>
                  Cancel
                </Button>
              </>
            }
          >
            Delete this tree and its {tree.tasks.length} task{tree.tasks.length === 1 ? "" : "s"}? None has started. This can&apos;t
            be undone.
          </Alert>
        ) : null}
        <ErrorText error={mode.error ?? start.error ?? status.error ?? dissolve.error ?? remove.error} />
        {startFailures.length === 0 ? null : (
          <Alert tone="bad">
            {startFailures.map((f) => (
              <span key={f.taskId} className="block">
                #{f.taskId} did not start: {f.error}
              </span>
            ))}
          </Alert>
        )}

        <div className="self-start"><Segmented label="Tree view" options={VIEW_OPTIONS} value={view} onChange={setView} /></div>
        <div hidden={view !== "notes"}>
          <JournalPanel owner={{ treeId: tree.id }} tasks={tree.tasks} active={view === "notes"} />
        </div>
        <div hidden={view !== "tasks"}>
        <div className="grid grid-cols-1 items-start gap-3.5 lg:grid-cols-[minmax(0,1fr)_340px]">
          <Panel
            title="Tree"
            flush
            meta={
              <>
                <span className="max-lg:hidden">{layout === "graph" ? "steps run top to bottom · click a task to edit" : "click a task to edit"}</span>
                <span className="lg:hidden">tap a task to open it</span>
              </>
            }
            actions={<Segmented label="Tree layout" small options={LAYOUT_OPTIONS} value={layout} onChange={setLayout} />}
          >
            {tree.tasks.length === 0 ? (
              <p className="p-3 text-small text-muted">No tasks remain in this tree. Its notes and history are retained.</p>
            ) : layout === "graph" ? (
              <TreeGraph tree={tree} selected={picked} onSelect={(id) => setPicked(picked === id ? null : id)} />
            ) : (
              <TreeView tree={tree} selected={picked} onSelect={(id) => setPicked(picked === id ? null : id)} />
            )}
          </Panel>
          <TreeActivity tree={tree} className="lg:hidden" />
          <div className="flex min-w-0 flex-col gap-3.5 max-lg:hidden">
            {selected === null ? (
              <TreeActivity tree={tree} />
            ) : (
              <TreeNodePanel key={selected.id} tree={tree} task={selected} onSelect={setPicked} onClose={() => setPicked(null)} />
            )}
          </div>
        </div>
        </div>
      </div>

      {drawer === "existing" ? <AddExistingTaskDrawer tree={tree} onClose={() => setDrawer(null)} /> : null}
      {drawer === "session" ? <AdoptSessionDrawer tree={tree} onClose={() => setDrawer(null)} /> : null}
    </>
  );
}
