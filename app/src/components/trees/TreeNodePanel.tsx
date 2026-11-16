import { useMutation } from "@tanstack/react-query";
import { useId, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { useInvalidateWork } from "../../api/queries.ts";
import type { StartMode, Task, TreeDetail, UpdateTaskRequest, Workspace } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";
import { handoffHint } from "../../lib/handoff.ts";
import { baseCandidates, needCandidates } from "../../lib/trees.ts";
import { HandoffToggle } from "../Handoff.tsx";
import { IconClose } from "../icons.tsx";
import { Button, ErrorText, Panel, Segmented, Select, buttonClass } from "../ui.tsx";
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
    <section className="flex min-w-0 flex-col gap-2 border-b border-line px-3 py-3">
      <h3 className="label-caps">
        {label}
        {meta === undefined ? null : <span className="ml-1.5 tracking-normal tabular">· {meta}</span>}
      </h3>
      {children}
    </section>
  );
}

/** A member line: `#id` and title (selects that node), with an optional trailing control. */
function MemberLine({ task, onSelect, trailing }: { task: Task; onSelect: (id: number) => void; trailing?: ReactNode }) {
  return (
    <li className="flex min-h-8 items-center gap-2">
      <button
        type="button"
        onClick={() => onSelect(task.id)}
        className="flex min-w-0 flex-1 items-baseline gap-1.5 text-left text-small hover:underline"
      >
        <span className="font-mono text-meta text-muted tabular">#{task.id}</span>
        <span className="min-w-0 truncate">{task.title}</span>
      </button>
      {trailing}
    </li>
  );
}

/** Selected base value: "" (stable base) or the need id whose branch the worktree starts from. */
function baseValue(w: Workspace, candidates: readonly Task[]): string {
  if (w.type !== "worktree" || w.base !== "predecessor") return "";
  const id = w.baseTaskId ?? candidates[0]?.id;
  return id === undefined ? "" : String(id);
}

/**
 * lg+ side column for the selected node: needs (remove, cycle-safe add), dependents, start mode, worktree base,
 * handoff, Open task and Remove from tree. Edits apply at once; needs, mode, base and handoff change only in backlog.
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
  const api = useApi();
  const invalidate = useInvalidateWork();
  const baseId = useId();
  const [confirmRemove, setConfirmRemove] = useState(false);
  const editable = task.state === "backlog";
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
    onSuccess: onClose,
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
      </div>

      <Section label="Needs" meta={needs.length}>
        {needs.length === 0 ? (
          <p className="text-small text-muted">None. It is a root: it starts when you start it.</p>
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
          <p className="font-mono text-meta text-muted">Needs change only while it is in backlog.</p>
        )}
      </Section>

      <Section label="Dependents" meta={dependents.length}>
        {dependents.length === 0 ? (
          <p className="text-small text-muted">Nothing needs it yet.</p>
        ) : (
          <ul className="flex flex-col">
            {dependents.map((d) => (
              <MemberLine key={d.id} task={d} onSelect={onSelect} trailing={<NodeChip task={d} />} />
            ))}
          </ul>
        )}
      </Section>

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
          {task.effectiveStartMode === "manual"
            ? "Reads ready when its needs settle; you start it."
            : "Starts on its own once every need settles."}
        </p>
      </Section>

      {task.needs.length === 0 ? null : (
        <Section label="Worktree base">
          {w.type !== "worktree" ? (
            <p className="text-small text-muted">Runs at the project root, so there is no base to pick.</p>
          ) : (
            <>
              <Select
                small
                id={baseId}
                aria-label="Worktree base"
                value={baseValue(w, bases)}
                disabled={!editable || update.isPending}
                onChange={(e) => setBase(e.target.value)}
                className="w-full font-mono"
              >
                <option value="">Stable base ({w.baseRef})</option>
                {bases.map((b) => (
                  <option key={b.id} value={b.id}>
                    #{b.id}&apos;s branch
                  </option>
                ))}
              </Select>
              <p className="font-mono text-meta text-muted">
                {bases.length === 0
                  ? "Its needs run in other projects; only the stable base applies."
                  : `A need's branch stacks on its unmerged work; falls back to ${w.baseRef}.`}
              </p>
            </>
          )}
        </Section>
      )}

      {task.needs.length === 0 ? null : (
        <div className="border-b border-line px-3 py-2">
          <HandoffToggle
            checked={task.handoff}
            disabled={!editable || update.isPending}
            onChange={(handoff) => update.mutate({ handoff })}
            hint={handoffHint(task)}
          />
        </div>
      )}

      <div className="flex flex-col gap-2 px-3 py-3">
        <ErrorText error={error} />
        <div className="flex flex-wrap items-center gap-2">
          <Link to={`/tasks/${task.id}`} className={buttonClass("secondary", true)}>
            Open task
          </Link>
          {confirmRemove ? (
            <>
              <Button variant="danger" small busy={remove.isPending} onClick={() => remove.mutate()} className="ml-auto">
                Confirm remove
              </Button>
              <Button variant="ghost" small onClick={() => setConfirmRemove(false)}>
                Cancel
              </Button>
            </>
          ) : (
            <Button variant="danger" small onClick={() => setConfirmRemove(true)} className="ml-auto">
              Remove from tree
            </Button>
          )}
        </div>
        {confirmRemove ? (
          <p className="font-mono text-meta text-muted">
            {dependents.length === 0 ? "The task stays, outside the tree." : "The task stays; its dependents take over its needs."}
          </p>
        ) : null}
      </div>
    </Panel>
  );
}
