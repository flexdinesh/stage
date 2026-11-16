import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { useInvalidateWork, useTasks } from "../../api/queries.ts";
import type { Task, TreeDetail } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";
import { TaskStateBadge } from "../StateBadge.tsx";
import { Button, Checkbox, Drawer, ErrorText, Field, Input, QueryState, Radio, cx } from "../ui.tsx";

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
 * choose which members it needs. A task in another tree moves (spliced out there).
 */
export function AddExistingTaskDrawer({ tree, onClose }: { tree: TreeDetail; onClose: () => void }) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const tasks = useTasks();
  const [query, setQuery] = useState("");
  const [picked, setPicked] = useState<Task | null>(null);
  const [needs, setNeeds] = useState<number[]>([]);
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

  const toggle = (id: number, on: boolean) => setNeeds((prev) => (on ? [...prev, id].toSorted((a, b) => a - b) : prev.filter((n) => n !== id)));

  const footer = (
    <>
      <span className="min-w-0 flex-1 font-mono text-meta-sm text-muted md:text-right">
        {picked === null ? "required: a task" : chosen.length === 0 ? `#${picked.id} joins as a root` : `#${picked.id} needs ${chosen.map((n) => `#${n}`).join(" ")}`}
      </span>
      <Button variant="primary" busy={add.isPending} disabled={picked === null} onClick={() => picked !== null && add.mutate(picked)}>
        Add to tree
      </Button>
    </>
  );

  return (
    <Drawer title="Add existing task" onClose={onClose} footer={footer}>
      <div className="flex flex-col gap-4">
        <Field label="Find a task">
          {(id) => (
            <Input id={id} type="search" data-autofocus placeholder="Title or #id" value={query} onChange={(e) => setQuery(e.target.value)} />
          )}
        </Field>
        <QueryState isPending={tasks.isPending} error={tasks.error}>
          <div role="radiogroup" aria-label="Task" className="border border-line">
            {results.length === 0 ? (
              <p className="px-3 py-2.5 text-small text-muted">No task matches. Tasks already in this tree are not listed.</p>
            ) : (
              results.map((t) => (
                <label
                  key={t.id}
                  className={cx(
                    "flex min-h-11 cursor-pointer items-center gap-3 border-b border-line px-3 py-2 last:border-b-0 hover:bg-panel md:min-h-10",
                    picked?.id === t.id && "bg-accent/5",
                  )}
                >
                  <Radio name="task" checked={picked?.id === t.id} onChange={() => setPicked(t)} />
                  <span className="min-w-0 flex-1">
                    <span className="mr-1.5 font-mono text-meta text-muted tabular">#{t.id}</span>
                    <span className="text-small font-medium break-words">{t.title}</span>
                    <span className="mt-0.5 block truncate font-mono text-meta text-muted">
                      {t.projectTitle}
                      {t.treeTitle === null ? "" : ` · moves from ${t.treeTitle}`}
                    </span>
                  </span>
                  <TaskStateBadge state={t.state} />
                </label>
              ))
            )}
          </div>
        </QueryState>
        {picked === null ? null : (
          <fieldset disabled={!canNeed} className="flex min-w-0 flex-col gap-1.5 disabled:opacity-55">
            <legend className="label-caps mb-1.5">Needs</legend>
            <div className="border border-line">
              {tree.tasks.map((m) => (
                <label key={m.id} className="flex min-h-11 cursor-pointer items-center gap-3 border-b border-line px-3 last:border-b-0 hover:bg-panel md:min-h-8">
                  <Checkbox checked={chosen.includes(m.id)} onChange={(e) => toggle(m.id, e.target.checked)} />
                  <span className="font-mono text-meta text-muted tabular">#{m.id}</span>
                  <span className="min-w-0 flex-1 truncate text-small">{m.title}</span>
                </label>
              ))}
            </div>
            <p className="font-mono text-meta text-muted">
              {canNeed
                ? "None checked: it joins as a root."
                : picked.state !== "backlog"
                  ? "It has already run, so it joins without needs."
                  : "It has a start time; clear it to give it needs."}
            </p>
          </fieldset>
        )}
        <ErrorText error={add.error} />
      </div>
    </Drawer>
  );
}
