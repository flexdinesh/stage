import { useMutation } from "@tanstack/react-query";
import { useId, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { useInvalidateWork } from "../../api/queries.ts";
import { useNewTask } from "../NewTask.tsx";
import type { StartMode, Task, TreeDetail, UpdateTaskRequest, Workspace } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";
import { handoffHint } from "../../lib/handoff.ts";
import { baseCandidates, needCandidates, startModeCaption, treeClosed } from "../../lib/trees.ts";
import { CompletionButton } from "../Completion.tsx";
import { HandoffToggle } from "../Handoff.tsx";
import { IconClose, IconPlus } from "../icons.tsx";
import { AddExistingTaskDrawer } from "./AddExistingTaskDrawer.tsx";
import { Combobox } from "../Combobox.tsx";
import { Button, ErrorText, Panel, Segmented, buttonClass } from "../ui.tsx";
import { NeedsPicker } from "./NeedsPicker.tsx";
import { NodeChip } from "./TreeNode.tsx";

type ModeChoice = "inherit" | StartMode;

const MODE_OPTIONS: { value: ModeChoice; label: string }[] = [
  { value: "inherit", label: "Inherit" },
  { value: "auto", label: "Auto" },
  { value: "manual", label: "Manual" },
];

function Section({ label, meta, children }: { label: string; meta?: ReactNode; children: ReactNode }) {
  return (
    <section className="flex min-w-0 flex-col gap-2 border-b border-line px-3 py-3 last:border-b-0">
      <h3 className="label-caps">
        {label}
        {meta === undefined ? null : <span className="ml-1.5 tracking-normal tabular">· {meta}</span>}
      </h3>
      {children}
    </section>
  );
}

/** A member line: `#id` and title (selects that node, else opens the task), with an optional trailing control. */
function MemberLine({ task, onSelect, trailing }: { task: Task; onSelect?: ((id: number) => void) | undefined; trailing?: ReactNode }) {
  const cls = "flex min-w-0 flex-1 items-baseline gap-1.5 text-left text-small hover:underline";
  const label = (
    <>
      <span className="font-mono text-meta text-muted tabular">#{task.id}</span>
      <span className="min-w-0 truncate">{task.title}</span>
    </>
  );
  return (
    <li className="flex min-h-8 items-center gap-2">
      {onSelect === undefined ? (
        <Link to={`/tasks/${task.id}`} className={cls}>
          {label}
        </Link>
      ) : (
        <button type="button" onClick={() => onSelect(task.id)} className={cls}>
          {label}
        </button>
      )}
      {trailing}
    </li>
  );
}

/** Selected base value: "" (stable base) or the need id whose branch the worktree starts from. */
function baseValue(w: Workspace, candidates: readonly Task[]): string {
  if (w.type !== "worktree" || w.base !== "predecessor") return "";
  const id = w.baseTaskId ?? (candidates.length === 1 ? candidates[0]?.id : undefined);
  return id === undefined || !candidates.some((c) => c.id === id) ? "unavailable" : String(id);
}

/**
 * lg+ side column for the selected node: the task with Complete and Open task, then its tree controls. Edits apply at once.
 */
export function TreeNodePanel({
  tree,
  task,
  onSelect,
  onClose,
}: {
  tree: TreeDetail;
  task: Task;
  onSelect: (id: number) => void;
  onClose: () => void;
}) {
  return (
    <Panel
      title={`Task #${task.id}`}
      flush
      actions={
        <Button variant="ghost" small onClick={onClose} aria-label="Close task panel">
          <IconClose size={14} />
        </Button>
      }
    >
      <div className="flex min-w-0 flex-col gap-1.5 border-b border-line px-3 py-3">
        <div>
          <NodeChip task={task} />
        </div>
        <Link to={`/tasks/${task.id}`} className="text-body font-medium break-words hover:underline">
          {task.title}
        </Link>
        <p className="font-mono text-meta text-muted">{task.projectTitle}</p>
        <div className="mt-1.5 flex flex-wrap items-center gap-2">
          <CompletionButton task={task} />
          <Link to={`/tasks/${task.id}`} className={buttonClass("secondary")}>
            Open task
          </Link>
        </div>
      </div>
      <TreeNodeEditor tree={tree} task={task} onSelect={onSelect} onRemoved={onClose} />
    </Panel>
  );
}

/**
 * A member's tree controls (tree view side panel, task detail Tree panel): needs (remove, cycle-safe add),
 * dependents (with Add task after and Link existing after: a task added from here runs after this one; several run
 * in parallel with each other), start mode, worktree base, handoff, then Remove from tree. Edits apply at once; needs, mode,
 * base and handoff change only in backlog. A done or archived tree is read-only (reopen it first). Member lines select
 * (`onSelect`) or open the task.
 */
export function TreeNodeEditor({
  tree,
  task,
  onSelect,
  onRemoved,
}: {
  tree: TreeDetail;
  task: Task;
  onSelect?: (id: number) => void;
  onRemoved?: () => void;
}) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const baseId = useId();
  const newTask = useNewTask();
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [linking, setLinking] = useState(false);
  const closed = treeClosed(tree);
  const editable = task.state === "backlog" && !closed;
  const byId = new Map(tree.tasks.map((t) => [t.id, t]));
  const needs = task.needs.flatMap((id) => byId.get(id) ?? []);
  const dependents = task.dependents.flatMap((id) => byId.get(id) ?? []);
  const candidates = needCandidates(tree.tasks, task.id);
  const bases = baseCandidates(task, tree);

  const setNeeds = useMutation({ mutationFn: (next: number[]) => api.setNeeds(task.id, next), onSettled: invalidate });
  const update = useMutation({
    mutationFn: (body: UpdateTaskRequest) => api.updateTask(task.id, body),
    onSettled: invalidate,
  });
  const remove = useMutation({
    mutationFn: () => api.removeFromTree(tree.id, task.id),
    onSuccess: () => onRemoved?.(),
    onSettled: invalidate,
  });
  const error = setNeeds.error ?? update.error ?? remove.error;

  const mode: ModeChoice = task.startMode ?? "inherit";
  const w = task.workspace;
  const setBase = (value: string) => {
    if (w.type !== "worktree") return;
    const picked = Number(value);
    const next: Workspace =
      value === ""
        ? { type: "worktree", baseRef: w.baseRef, branch: w.branch, base: "ref" }
        : { type: "worktree", baseRef: w.baseRef, branch: w.branch, base: "predecessor", baseTaskId: picked };
    update.mutate({ workspace: next });
  };

  return (
    <>
      <Section label="Needs" meta={needs.length}>
        {needs.length === 0 ? (
          <p className="text-small text-muted">{editable ? "None. A root: runs in parallel, starts when you start it." : "None."}</p>
        ) : (
          <ul className="flex flex-col">
            {needs.map((n) => (
              <MemberLine
                key={n.id}
                task={n}
                onSelect={onSelect}
                trailing={
                  <>
                    <NodeChip task={n} />
                    {editable ? (
                        <Button
                          variant="ghost"
                          small
                          aria-label={`Remove need #${n.id}`}
                          title={`Remove need #${n.id}`}
                          disabled={setNeeds.isPending}
                          onClick={() => setNeeds.mutate(task.needs.filter((id) => id !== n.id))}
                          className="w-8 px-0 md:w-6"
                        >
                          <IconClose size={12} />
                        </Button>
                    ) : null}
                  </>
                }
              />
            ))}
          </ul>
        )}
        {editable ? (
          <NeedsPicker candidates={candidates} disabled={setNeeds.isPending} onAdd={(id) => setNeeds.mutate([...task.needs, id])} />
        ) : (
          <p className="font-mono text-meta text-muted">
            {closed ? "Reopen the tree to change needs." : "Needs change only while it is in backlog."}
          </p>
        )}
      </Section>

      <Section label="Dependents" meta={dependents.length}>
        {dependents.length === 0 ? (
          <p className="text-small text-muted">Nothing runs after it yet.</p>
        ) : (
          <ul className="flex flex-col">
            {dependents.map((d) => (
              <MemberLine key={d.id} task={d} onSelect={onSelect} trailing={<NodeChip task={d} />} />
            ))}
          </ul>
        )}
        {closed ? null : (
          <div className="flex flex-wrap items-center gap-2">
            <Button
              small
              title={`New task that runs after #${task.id}`}
              onClick={() => newTask(task.projectId, undefined, { treeId: tree.id, treeTitle: tree.title, needs: [task.id], startMode: tree.startMode })}
            >
              <IconPlus size={12} />
              Add task after
            </Button>
            <Button small title={`Pick a task to run after #${task.id}`} onClick={() => setLinking(true)}>
              Link existing after
            </Button>
            <p className="w-full font-mono text-meta text-muted">Added tasks run after #{task.id}, in parallel with each other.</p>
          </div>
        )}
      </Section>
      {linking ? <AddExistingTaskDrawer tree={tree} after={task.id} onClose={() => setLinking(false)} /> : null}

      {/* Start mode, base and handoff only shape a launch: once it has run they no longer apply. */}
      {editable ? (
      <Section label="Start mode">
        <fieldset disabled={!editable || update.isPending} className="min-w-0 disabled:opacity-55">
          <Segmented
            label="Start mode"
            options={MODE_OPTIONS}
            value={mode}
            onChange={(m) => update.mutate({ startMode: m === "inherit" ? null : m })}
          />
        </fieldset>
        <p className="font-mono text-meta text-muted">
          {mode === "inherit" ? `Tree is ${tree.startMode}. ` : ""}
          {startModeCaption(task)}
        </p>
      </Section>

      ) : null}

      {!editable || task.needs.length === 0 ? null : (
        <Section label="Worktree base">
          {w.type !== "worktree" ? (
            <p className="text-small text-muted">Runs at the project root, so there is no base to pick.</p>
          ) : (
            <>
              <Combobox
                small
                id={baseId}
                aria-label="Worktree base"
                value={baseValue(w, bases)}
                disabled={!editable || update.isPending}
                onChange={setBase}
                listLabel="Worktree bases"
                className="w-full font-mono"
                options={[
                  { value: "", label: `Stable base (${w.baseRef})` },
                  ...(baseValue(w, bases) === "unavailable"
                    ? [{ value: "unavailable", label: "Predecessor unavailable: choose a base", disabled: true }]
                    : []),
                  ...bases.map((b) => ({ value: String(b.id), label: `#${b.id}'s branch` })),
                ]}
              />
              <p className="font-mono text-meta text-muted">
                {bases.length === 0
                  ? "No eligible predecessor. Choose the stable base explicitly."
                  : "An unavailable predecessor blocks launch. Choose the stable base explicitly to continue."}
              </p>
            </>
          )}
        </Section>
      )}

      {!editable || task.needs.length === 0 ? null : (
        <div className="border-b border-line px-3 py-2">
          <HandoffToggle
            checked={task.handoff}
            disabled={!editable || update.isPending}
            onChange={(handoff) => update.mutate({ handoff })}
            hint={handoffHint(task)}
          />
        </div>
      )}

      {closed && error === null ? null : (
        <div className="flex flex-col gap-2 px-3 py-3">
          <ErrorText error={error} />
          {closed ? null : (
            <div className="flex flex-wrap items-center justify-end gap-2">
              {confirmRemove ? (
                <>
                  <Button variant="danger" small busy={remove.isPending} onClick={() => remove.mutate()}>
                    Confirm remove
                  </Button>
                  <Button variant="ghost" small onClick={() => setConfirmRemove(false)}>
                    Cancel
                  </Button>
                </>
              ) : (
                <Button variant="danger" small onClick={() => setConfirmRemove(true)}>
                  Remove from tree
                </Button>
              )}
            </div>
          )}
          {confirmRemove ? (
            <p className="font-mono text-meta text-muted">
              {dependents.length === 0 ? "The task stays, outside the tree." : "The task stays; its dependents take over its needs."}
            </p>
          ) : null}
        </div>
      )}
    </>
  );
}
