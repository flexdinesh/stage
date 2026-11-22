import { useMutation } from "@tanstack/react-query";
import { useId, useState, type ReactNode } from "react";
import { useInvalidateWork } from "../../api/queries.ts";
import { useNewTask } from "../NewTask.tsx";
import type { StartMode, Task, TreeDetail, UpdateTaskRequest, Workspace } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";
import { handoffHint } from "../../lib/handoff.ts";
import { baseCandidates, needCandidates, startModeCaption, treeClosed } from "../../lib/trees.ts";
import { StateControl } from "../StateControl.tsx";
import { HandoffToggle } from "../Handoff.tsx";
import { IconArrowRight, IconChainLink, IconClose, IconPlus } from "../icons.tsx";
import { AddExistingTaskDrawer } from "./AddExistingTaskDrawer.tsx";
import { Button, ConfirmInline, EmptyState, ErrorText, Hint, IconButton, IdTag, MetaLine, Muted, Panel, Segmented, Combobox, SubHeading, TextButton, TextLink, cx, rowClass } from "../ui.tsx";
import { NeedsPicker } from "./NeedsPicker.tsx";
import { NodeAction, NodeChip } from "./TreeNode.tsx";

type ModeChoice = "inherit" | StartMode;

const MODE_OPTIONS: { value: ModeChoice; label: string }[] = [
  { value: "inherit", label: "Inherit" },
  { value: "auto", label: "Auto" },
  { value: "manual", label: "Manual" },
];

function Section({ label, meta, children }: { label: string; meta?: ReactNode; children: ReactNode }) {
  return (
    <section className={cx(rowClass, "flex min-w-0 flex-col gap-2")}>
      <SubHeading title={label} {...(meta === undefined ? {} : { meta: <span className="tabular">{meta}</span> })} />
      {children}
    </section>
  );
}

/** A member line: `#id` and title (selects that node, else opens the task), with an optional trailing control. */
function MemberLine({ task, onSelect, trailing }: { task: Task; onSelect?: ((id: number) => void) | undefined; trailing?: ReactNode }) {
  const cls = "flex min-w-0 flex-1 items-baseline gap-1.5 text-small";
  const label = (
    <>
      <IdTag id={task.id} />
      <span className="min-w-0 truncate">{task.title}</span>
    </>
  );
  return (
    <li className="flex min-h-8 items-center gap-2">
      {onSelect === undefined ? (
        <TextLink to={`/tasks/${task.id}`} className={cx(cls, "text-left")}>
          {label}
        </TextLink>
      ) : (
        <TextButton onClick={() => onSelect(task.id)} className={cls}>
          {label}
        </TextButton>
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
 * lg+ side column for the selected node: Open (to the task page) in the header, the task, its State picker with its
 * one action (Start, Retry, Open in T3), then its tree controls. Edits apply at once.
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
        <>
          <TextLink
            to={`/tasks/${task.id}`}
            aria-label={`Open task #${task.id}`}
            className="inline-flex min-h-7 items-center gap-1 px-1.5 font-mono text-meta text-fg"
          >
            Open
            <IconArrowRight size={12} />
          </TextLink>
          <IconButton small label="Close task panel" icon={IconClose} onClick={onClose} />
        </>
      }
    >
      <div className={cx(rowClass, "flex min-w-0 flex-col gap-1.5")}>
        <p className="text-body font-medium break-words">{task.title}</p>
        <MetaLine>{task.projectTitle}</MetaLine>
      </div>
      <Section label="State">
        <StateControl task={task} tree={tree} action={<NodeAction task={task} pending={[]} review={false} />} />
      </Section>
      <TreeNodeEditor tree={tree} task={task} onSelect={onSelect} onRemoved={onClose} />
    </Panel>
  );
}

/**
 * A member's tree controls (tree view side panel, task detail Tree panel): needs (remove, cycle-safe add),
 * dependents (stacked New task and Existing task buttons, each captioned: a task added from here waits for this one),
 * start mode, worktree base, handoff, then Remove from tree. Edits apply at once; needs, mode,
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
  const newHintId = useId();
  const existingHintId = useId();
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
          <EmptyState>{editable ? "None. A root: runs in parallel, starts when you start it." : "None."}</EmptyState>
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
                        <IconButton
                          small
                          label={`Remove need #${n.id}`}
                          icon={IconClose}
                          disabled={setNeeds.isPending}
                          onClick={() => setNeeds.mutate(task.needs.filter((id) => id !== n.id))}
                        />
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
          <Hint>{closed ? "Reopen the tree to change needs." : "Needs change only while it is in backlog."}</Hint>
        )}
      </Section>

      <Section label="Dependents" meta={dependents.length}>
        {dependents.length === 0 ? (
          <EmptyState>Nothing runs after it yet.</EmptyState>
        ) : (
          <ul className="flex flex-col">
            {dependents.map((d) => (
              <MemberLine key={d.id} task={d} onSelect={onSelect} trailing={<NodeChip task={d} />} />
            ))}
          </ul>
        )}
        {closed ? null : (
          <div className="flex flex-col gap-2.5">
            <div className="flex flex-col gap-1">
              <Button
                small
                aria-describedby={newHintId}
                className="w-full justify-start"
                onClick={() => newTask(task.projectId, undefined, { treeId: tree.id, treeTitle: tree.title, needs: [task.id], startMode: tree.startMode })}
              >
                <IconPlus size={12} />
                New task
              </Button>
              <Hint id={newHintId}>Create a new task that waits for #{task.id}.</Hint>
            </div>
            <div className="flex flex-col gap-1">
              <Button small aria-describedby={existingHintId} className="w-full justify-start" onClick={() => setLinking(true)}>
                <IconChainLink />
                Existing task
              </Button>
              <Hint id={existingHintId}>Pick one from any project. It moves into this tree and waits for #{task.id}.</Hint>
            </div>
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
        <Hint>
          {mode === "inherit" ? `Tree is ${tree.startMode}. ` : ""}
          {startModeCaption(task)}
        </Hint>
      </Section>

      ) : null}

      {!editable || task.needs.length === 0 ? null : (
        <Section label="Worktree base">
          {w.type !== "worktree" ? (
            <Muted>Runs at the project root, so there is no base to pick.</Muted>
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
              <Hint>
                {bases.length === 0
                  ? "No eligible predecessor. Choose the stable base explicitly."
                  : "An unavailable predecessor blocks launch. Choose the stable base explicitly to continue."}
              </Hint>
            </>
          )}
        </Section>
      )}

      {!editable || task.needs.length === 0 ? null : (
        <div className={rowClass}>
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
          {closed ? null : confirmRemove ? (
            <ConfirmInline danger confirmLabel="Remove from tree" busy={remove.isPending} onConfirm={() => remove.mutate()} onCancel={() => setConfirmRemove(false)}>
              Remove this task from the tree?{" "}
              {dependents.length === 0 ? "The task stays, outside the tree." : "The task stays; its dependents take over its needs."}
            </ConfirmInline>
          ) : (
            <div className="flex flex-wrap items-center justify-end gap-2">
              <Button variant="danger" small onClick={() => setConfirmRemove(true)}>
                Remove from tree
              </Button>
            </div>
          )}
        </div>
      )}
    </>
  );
}
