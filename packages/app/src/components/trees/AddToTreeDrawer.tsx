import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { useInvalidateWork, useTrees } from "../../api/queries.ts";
import type { StartMode, Task } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";
import { treeMeta } from "../../lib/trees.ts";
import { Button, Drawer, ErrorText, Field, Input, QueryState, Radio, Segmented, cx } from "../ui.tsx";

const MODE_OPTIONS: { value: StartMode; label: string }[] = [
  { value: "auto", label: "Auto" },
  { value: "manual", label: "Manual" },
];

/** "new" or an active tree's id. */
type TreeChoice = "new" | number;

/**
 * Put a task outside any tree into one: a new tree with it as the root (title defaults to the task's), or an active
 * tree, where it joins as a root (set its needs in the tree view).
 */
export function AddToTreeDrawer({ task, onClose }: { task: Task; onClose: () => void }) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const trees = useTrees();
  const list = trees.data?.trees ?? [];
  const [picked, setPicked] = useState<TreeChoice>("new");
  const [title, setTitle] = useState(task.title);
  const [mode, setMode] = useState<StartMode>("auto");

  const add = useMutation({
    mutationFn: async (choice: TreeChoice) => {
      if (choice === "new") await api.createTree({ title: title.trim(), startMode: mode, tasks: [{ taskId: task.id }] });
      else await api.addToTree(choice, task.id);
    },
    onSuccess: async () => {
      await invalidate();
      onClose();
    },
  });

  const target = picked === "new" ? null : (list.find((t) => t.id === picked) ?? null);
  const blocked = picked === "new" && title.trim() === "";
  const footer = (
    <>
      <span className="min-w-0 flex-1 font-mono text-meta-sm text-muted md:text-right">
        {blocked ? "required: title" : target === null ? `#${task.id} roots a new tree` : `#${task.id} joins ${target.title} as a root`}
      </span>
      <Button variant="primary" busy={add.isPending} disabled={blocked} onClick={() => add.mutate(picked)}>
        {picked === "new" ? "Create tree" : "Add to tree"}
      </Button>
    </>
  );

  const option = (key: TreeChoice, label: string, meta: string) => (
    <label
      key={key}
      className={cx(
        "flex min-h-11 cursor-pointer items-center gap-3 border-b border-line px-3 py-2 last:border-b-0 hover:bg-panel md:min-h-10",
        picked === key && "bg-accent/5",
      )}
    >
      <Radio name="tree" checked={picked === key} onChange={() => setPicked(key)} />
      <span className="min-w-0 flex-1">
        <span className="block text-small font-medium break-words">{label}</span>
        <span className="mt-0.5 block truncate font-mono text-meta text-muted tabular">{meta}</span>
      </span>
    </label>
  );

  return (
    <Drawer title="Add to tree" onClose={onClose} footer={footer}>
      <div className="flex flex-col gap-4">
        <p className="text-small text-muted">
          <span className="font-mono tabular">#{task.id}</span> {task.title} joins as a root. Give it needs, or add tasks that need it, in the
          tree view.
        </p>
        <QueryState isPending={trees.isPending} error={trees.error}>
          <div role="radiogroup" aria-label="Tree" className="border border-line">
            {option("new", "New tree", "with this task as its root")}
            {list.map((t) => option(t.id, t.title, treeMeta(t)))}
          </div>
        </QueryState>
        {picked === "new" ? (
          <>
            <Field label="Title">{(id) => <Input id={id} data-autofocus value={title} onChange={(e) => setTitle(e.target.value)} />}</Field>
            <div className="flex min-w-0 flex-col items-start gap-1.5">
              <span className="label-caps">Start mode</span>
              <Segmented label="Start mode" options={MODE_OPTIONS} value={mode} onChange={setMode} />
              <p className="font-mono text-meta text-muted">
                {mode === "auto" ? "A task starts on its own once every need is completed." : "A task reads ready when its needs are completed; you start it."}
              </p>
            </div>
          </>
        ) : null}
        <ErrorText error={add.error} />
      </div>
    </Drawer>
  );
}
