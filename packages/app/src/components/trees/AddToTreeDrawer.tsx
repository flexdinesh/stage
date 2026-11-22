import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { useInvalidateWork, useTree, useTrees } from "../../api/queries.ts";
import type { StartMode, Task } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";
import { joinSummary, treeMeta } from "../../lib/trees.ts";
import { Button, Drawer, ErrorText, Field, FieldGroup, Input, Meta, Muted, QueryState, Radio, Segmented, cx, Combobox, rowButtonClass } from "../ui.tsx";

const MODE_OPTIONS: { value: StartMode; label: string }[] = [
  { value: "auto", label: "Auto" },
  { value: "manual", label: "Manual" },
];

/** "new" or an active tree's id. */
type TreeChoice = "new" | number;

/**
 * Put a task outside any tree into one: a new tree with it as the root (title defaults to the task's), or an active
 * tree, where it joins as a parallel root unless After picks a member for it to run after (backlog without a start
 * time only).
 */
export function AddToTreeDrawer({ task, onClose }: { task: Task; onClose: () => void }) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const trees = useTrees();
  const list = trees.data?.trees ?? [];
  const [picked, setPicked] = useState<TreeChoice>("new");
  const [title, setTitle] = useState(task.title);
  const [mode, setMode] = useState<StartMode>("auto");
  const [after, setAfter] = useState<number | null>(null);
  const detail = useTree(typeof picked === "number" ? picked : 0, typeof picked === "number");
  const canNeed = task.state === "backlog" && task.startAt === null;
  const members = typeof picked === "number" && detail.data?.id === picked ? detail.data.tasks : [];
  const afterTask = canNeed ? (members.find((m) => m.id === after) ?? null) : null;
  const pick = (choice: TreeChoice) => {
    setPicked(choice);
    setAfter(null);
  };

  const add = useMutation({
    mutationFn: async (choice: TreeChoice) => {
      if (choice === "new") await api.createTree({ title: title.trim(), startMode: mode, tasks: [{ taskId: task.id }] });
      else await api.addToTree(choice, task.id, afterTask === null ? undefined : [afterTask.id]);
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
      <Meta small className="min-w-0 flex-1 md:text-right">
        {blocked
          ? "required: title"
          : target === null
            ? `#${task.id} roots a new tree`
            : joinSummary(task.id, afterTask === null ? [] : [afterTask], task.startMode ?? target.startMode, target.title)}
      </Meta>
      <Button variant="primary" busy={add.isPending} disabled={blocked} onClick={() => add.mutate(picked)}>
        {picked === "new" ? "Create tree" : "Add to tree"}
      </Button>
    </>
  );

  const option = (key: TreeChoice, label: string, meta: string) => (
    <label
      key={key}
      className={cx(rowButtonClass(picked === key), "cursor-pointer border-b border-line last:border-b-0")}
    >
      <Radio name="tree" checked={picked === key} onChange={() => pick(key)} />
      <span className="min-w-0 flex-1">
        <span className="block text-small font-medium break-words">{label}</span>
        <Meta className="mt-0.5 block truncate tabular">{meta}</Meta>
      </span>
    </label>
  );

  return (
    <Drawer title="Add to tree" onClose={onClose} footer={footer}>
      <div className="flex flex-col gap-4">
        <Muted>
          <span className="font-mono tabular">#{task.id}</span> {task.title} joins as a parallel root: it waits on nothing. To run it
          after a task, pick its tree, then that task under After.
        </Muted>
        <QueryState isPending={trees.isPending} error={trees.error}>
          <div role="radiogroup" aria-label="Tree" className="border border-line">
            {option("new", "New tree", "with this task as its root")}
            {list.map((t) => option(t.id, t.title, treeMeta(t)))}
          </div>
        </QueryState>
        {picked === "new" ? (
          <>
            <Field label="Title">{(id) => <Input id={id} data-autofocus value={title} onChange={(e) => setTitle(e.target.value)} />}</Field>
            <FieldGroup
              label="Start mode"
              hint={mode === "auto" ? "A task starts on its own once every need is completed." : "A task reads ready when its needs are completed; you start it."}
            >
              <div>
                <Segmented label="Start mode" options={MODE_OPTIONS} value={mode} onChange={setMode} />
              </div>
            </FieldGroup>
          </>
        ) : (
          <Field
            label="After"
            hint={
              !canNeed
                ? task.state !== "backlog"
                  ? "It has already run, so it joins as a parallel root."
                  : "It has a start time, so it joins as a parallel root."
                : afterTask === null
                  ? "A parallel root starts when you start it."
                  : `Waits until #${afterTask.id} is completed.`
            }
          >
            {(id) => (
              <Combobox
                id={id}
                value={afterTask === null ? "" : String(afterTask.id)}
                disabled={!canNeed || detail.isPending}
                onChange={(next) => setAfter(next === "" ? null : Number(next))}
                listLabel="Tree members"
                className="w-full"
                options={[
                  { value: "", label: "Nothing: runs in parallel" },
                  ...members.map((m) => ({ value: String(m.id), label: `#${m.id} ${m.title}` })),
                ]}
              />
            )}
          </Field>
        )}
        <ErrorText error={add.error ?? detail.error} />
      </div>
    </Drawer>
  );
}
