import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { useInvalidateWork, useTasks } from "../../api/queries.ts";
import type { Task, TreeDetail } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";
import { joinSummary } from "../../lib/trees.ts";
import { NodeChip } from "./TreeNode.tsx";
import { Button, Checkbox, ChoiceRow, Drawer, EmptyState, ErrorText, Field, FieldGroup, IdTag, Input, Meta, QueryState, Radio, cx, rowButtonClass } from "../ui.tsx";

const RESULT_LIMIT = 30;

/** Title or `#id` match, newest activity first; members of this tree are left out. */
function matches(tasks: readonly Task[], treeId: number, query: string): Task[] {
  const q = query.trim().toLowerCase().replace(/^#/, "");
  return tasks
    .filter((t) => t.treeId !== treeId && (q === "" || t.title.toLowerCase().includes(q) || String(t.id) === q))
    .toSorted((a, b) => b.updatedAt.localeCompare(a.updatedAt))
    .slice(0, RESULT_LIMIT);
}

/**
 * Add an existing task to the tree: search tasks by title or #id, pick one, then (backlog without a start time only)
 * choose which members it runs after (none: a parallel root). `after` (Existing task under a member's Dependents) starts with that
 * member checked. A task in another tree moves (spliced out there).
 */
export function AddExistingTaskDrawer({ tree, after, onClose }: { tree: TreeDetail; after?: number; onClose: () => void }) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const tasks = useTasks();
  const [query, setQuery] = useState("");
  const [picked, setPicked] = useState<Task | null>(null);
  const [needs, setNeeds] = useState<number[]>(after === undefined ? [] : [after]);
  const results = matches(tasks.data?.tasks ?? [], tree.id, query);
  const canNeed = picked !== null && picked.state === "backlog" && picked.startAt === null;
  const chosen = canNeed ? needs : [];

  const add = useMutation({
    mutationFn: (task: Task) => api.addToTree(tree.id, task.id, chosen.length === 0 ? undefined : chosen),
    onSuccess: async () => {
      await invalidate();
      onClose();
    },
  });

  const chosenTasks = tree.tasks.filter((m) => chosen.includes(m.id));
  const toggle = (id: number, on: boolean) => setNeeds((prev) => (on ? [...prev, id].toSorted((a, b) => a - b) : prev.filter((n) => n !== id)));

  const footer = (
    <>
      <Meta small className="min-w-0 flex-1 md:text-right">
        {picked === null ? "required: a task" : joinSummary(picked.id, chosenTasks, picked.startMode ?? tree.startMode)}
      </Meta>
      <Button variant="primary" busy={add.isPending} disabled={picked === null} onClick={() => picked !== null && add.mutate(picked)}>
        Add to tree
      </Button>
    </>
  );

  return (
    <Drawer title={after === undefined ? "Add existing task" : `Existing task after #${after}`} onClose={onClose} footer={footer}>
      <div className="flex flex-col gap-4">
        <Field label="Find a task">
          {(id) => (
            <Input id={id} type="search" data-autofocus placeholder="Title or #id" value={query} onChange={(e) => setQuery(e.target.value)} />
          )}
        </Field>
        <QueryState isPending={tasks.isPending} error={tasks.error}>
          <div role="radiogroup" aria-label="Task" className="border border-line">
            {results.length === 0 ? (
              <EmptyState inset>No task matches. Tasks already in this tree are not listed.</EmptyState>
            ) : (
              results.map((t) => (
                <label
                  key={t.id}
                  className={cx(rowButtonClass(picked?.id === t.id), "cursor-pointer border-b border-line last:border-b-0")}
                >
                  <Radio name="task" checked={picked?.id === t.id} onChange={() => setPicked(t)} />
                  <span className="min-w-0 flex-1">
                    <IdTag id={t.id} className="mr-1.5" />
                    <span className="text-small font-medium break-words">{t.title}</span>
                    <Meta className="mt-0.5 block truncate">
                      {t.projectTitle}
                      {t.treeTitle === null ? "" : ` · moves from ${t.treeTitle}`}
                    </Meta>
                  </span>
                  <NodeChip task={t} />
                </label>
              ))
            )}
          </div>
        </QueryState>
        {picked === null ? null : (
          <fieldset disabled={!canNeed} className="min-w-0 disabled:opacity-55">
            <FieldGroup
              label="Runs after"
              hint={
                canNeed
                  ? "None checked: a parallel root. Checked: it waits for them."
                  : picked.state !== "backlog"
                    ? "It has already run, so it joins as a parallel root."
                    : "It has a start time; clear it to give it needs."
              }
            >
              <div className="flex flex-col">
                {tree.tasks.map((m) => (
                  <ChoiceRow key={m.id} control={<Checkbox checked={chosen.includes(m.id)} onChange={(e) => toggle(m.id, e.target.checked)} />}>
                    <IdTag id={m.id} className="mr-1.5" />
                    {m.title}
                  </ChoiceRow>
                ))}
              </div>
            </FieldGroup>
          </fieldset>
        )}
        <ErrorText error={add.error} />
      </div>
    </Drawer>
  );
}
