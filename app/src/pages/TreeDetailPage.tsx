import { useMutation } from "@tanstack/react-query";
import { useId, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import { ApiError } from "../api/client.ts";
import { useInvalidateWork, useTree } from "../api/queries.ts";
import type { StartMode, TreeDetail } from "../api/types.ts";
import { useApi } from "../platform/context.tsx";
import { IconBack, IconChevronDown, IconPlus } from "../components/icons.tsx";
import { useNewTask } from "../components/NewTask.tsx";
import { AddExistingTaskDrawer } from "../components/trees/AddExistingTaskDrawer.tsx";
import { AdoptSessionDrawer } from "../components/trees/AdoptSessionDrawer.tsx";
import { TreeNodePanel } from "../components/trees/TreeNodePanel.tsx";
import { TreeView, TreeViewSkeleton } from "../components/trees/TreeView.tsx";
import { Alert, Button, Chip, ErrorText, Input, Muted, PageHeader, Panel, Segmented, buttonClass, cx } from "../components/ui.tsx";
import { useIdParam } from "../lib/params.ts";
import { treeMeta, treeSettled } from "../lib/trees.ts";
import { NotFoundPage } from "./NotFoundPage.tsx";

const MODE_OPTIONS: { value: StartMode; label: string }[] = [
  { value: "auto", label: "Auto" },
  { value: "manual", label: "Manual" },
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
  return <TreeBody tree={tree.data} />;
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
        It was dissolved, or its last task left it (an empty tree is deleted). Its tasks, if any, are still in stage.
      </p>
      <Link to="/trees" className={buttonClass("secondary")}>
        <IconBack /> Back to trees
      </Link>
    </>
  );
}

type Drawer = "existing" | "session" | null;

/** Overflow menu (popover): Rename, Add existing task, Add session, Dissolve. */
function TreeMenu({ onPick }: { onPick: (item: "rename" | "existing" | "session" | "dissolve") => void }) {
  const menuId = useId();
  const menu = useRef<HTMLDivElement>(null);
  const item = (key: "rename" | "existing" | "session" | "dissolve", label: string, danger = false) => (
    <button
      type="button"
      onClick={() => {
        menu.current?.hidePopover();
        onPick(key);
      }}
      className={cx("border-b border-line px-3 py-2.5 text-left text-ui last:border-b-0 hover:bg-panel md:py-2", danger && "text-bad")}
    >
      {label}
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
          {item("existing", "Add existing task")}
          {item("session", "Add session")}
          {item("dissolve", "Dissolve tree", true)}
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
  const [picked, setPicked] = useState<number | null>(null);
  const [renaming, setRenaming] = useState(false);
  const [drawer, setDrawer] = useState<Drawer>(null);
  const [dissolving, setDissolving] = useState(false);
  const selected = tree.tasks.find((t) => t.id === picked) ?? null;
  const readyCount = tree.tasks.filter((t) => t.readiness === "ready" && !t.adopted).length;
  const settled = treeSettled(tree.tasks);

  const start = useMutation({ mutationFn: () => api.startTree(tree.id), onSettled: invalidate });
  const mode = useMutation({ mutationFn: (startMode: StartMode) => api.updateTree(tree.id, { startMode }), onSettled: invalidate });
  const dissolve = useMutation({
    mutationFn: () => api.deleteTree(tree.id),
    onSuccess: async () => {
      await navigate("/trees");
      await invalidate();
    },
  });
  const startFailures = start.data?.errors ?? [];

  const addTask = () => {
    const anchor = selected ?? tree.tasks[0];
    newTask(anchor?.projectId, undefined, {
      treeId: tree.id,
      treeTitle: tree.title,
      needs: selected === null ? [] : [selected.id],
    });
  };

  const onMenu = (item: "rename" | "existing" | "session" | "dissolve") => {
    if (item === "rename") setRenaming(true);
    else if (item === "dissolve") setDissolving(true);
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
            {treeMeta(tree)}
            {settled ? (
              <Chip tone="ok" dot className="ml-2">
                settled
              </Chip>
            ) : null}
          </>
        }
        actions={
          <>
            <Segmented label="Tree start mode" options={MODE_OPTIONS} value={tree.startMode} onChange={(m) => m !== tree.startMode && mode.mutate(m)} />
            <TreeMenu onPick={onMenu} />
            <Button onClick={addTask} title={selected === null ? "New root task in this tree" : `New task that needs #${selected.id}`}>
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
        }
      />

      <div className="flex flex-col gap-3.5">
        {dissolving ? (
          <Alert
            tone="bad"
            actions={
              <>
                <Button variant="danger" small busy={dissolve.isPending} onClick={() => dissolve.mutate()}>
                  Dissolve
                </Button>
                <Button variant="ghost" small onClick={() => setDissolving(false)}>
                  Cancel
                </Button>
              </>
            }
          >
            Dissolve this tree? Its {tree.tasks.length} tasks stay in stage; their needs and tree membership go.
          </Alert>
        ) : null}
        <ErrorText error={mode.error ?? start.error ?? dissolve.error} />
        {startFailures.length === 0 ? null : (
          <Alert tone="bad">
            {startFailures.map((f) => (
              <span key={f.taskId} className="block">
                #{f.taskId} did not start: {f.error}
              </span>
            ))}
          </Alert>
        )}

        <div className="grid grid-cols-1 gap-3.5 lg:grid-cols-[minmax(0,1fr)_340px]">
          <Panel
            title="Tree"
            flush
            meta={
              <>
                <span className="max-lg:hidden">hover to trace needs · click to edit</span>
                <span className="lg:hidden">tap a task to open it</span>
              </>
            }
          >
            <TreeView tree={tree} selected={picked} onSelect={(id) => setPicked((cur) => (cur === id ? null : id))} />
          </Panel>
          <div className="flex min-w-0 flex-col gap-3.5 max-lg:hidden">
            {selected === null ? (
              <Panel title="Task">
                <Muted>Select a task to edit its needs, start mode, worktree base and handoff.</Muted>
              </Panel>
            ) : (
              <TreeNodePanel key={selected.id} tree={tree} task={selected} onSelect={setPicked} onClose={() => setPicked(null)} />
            )}
          </div>
        </div>
      </div>

      {drawer === "existing" ? <AddExistingTaskDrawer tree={tree} onClose={() => setDrawer(null)} /> : null}
      {drawer === "session" ? <AdoptSessionDrawer tree={tree} onClose={() => setDrawer(null)} /> : null}
    </>
  );
}
