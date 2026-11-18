import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { useInvalidateWork } from "../../api/queries.ts";
import type { StartMode, Task } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";
import { moveItem } from "../../lib/tasks.ts";
import { treeMembers, type TreeShape } from "../../lib/trees.ts";
import { IconChevronDown, IconChevronUp } from "../icons.tsx";
import { Button, Drawer, ErrorText, Field, Input, Segmented } from "../ui.tsx";


const SHAPES: { value: TreeShape; label: string }[] = [
  { value: "parallel", label: "Parallel" },
  { value: "chain", label: "Chain" },
];

const MODES: { value: StartMode; label: string }[] = [
  { value: "auto", label: "Auto" },
  { value: "manual", label: "Manual" },
];

/**
 * Make a tree of backlog tasks (session batch selection): title (defaults to the first task's), Parallel (all roots)
 * or Chain (each needs the previous, reordered with up/down), start mode. Opens the new tree.
 */
export function NewTreeDrawer({ tasks, onClose, onCreated }: { tasks: readonly Task[]; onClose: () => void; onCreated?: () => void }) {
  const api = useApi();
  const navigate = useNavigate();
  const invalidate = useInvalidateWork();
  const [title, setTitle] = useState(tasks[0]?.title ?? "");
  const [shape, setShape] = useState<TreeShape>("parallel");
  const [mode, setMode] = useState<StartMode>("manual");
  const [order, setOrder] = useState<readonly Task[]>(tasks);

  const create = useMutation({
    mutationFn: () => api.createTree({ title: title.trim(), startMode: mode, tasks: treeMembers(order, shape) }),
    onSuccess: async (tree) => {
      await invalidate();
      onCreated?.();
      onClose();
      await navigate(`/trees/${tree.id}`);
    },
  });

  const blocked = title.trim() === "" || order.length === 0;
  const footer = (
    <>
      <span className="min-w-0 flex-1 font-mono text-meta-sm text-muted md:text-right">
        {title.trim() === "" ? "required: title" : shape === "chain" ? `${order.length} in a chain` : `${order.length} roots`}
      </span>
      <Button variant="primary" busy={create.isPending} disabled={blocked} onClick={() => create.mutate()}>
        Create tree
      </Button>
    </>
  );

  return (
    <Drawer title={`New tree from ${tasks.length}`} onClose={onClose} footer={footer}>
      <div className="flex flex-col gap-4">
        <Field label="Title">{(id) => <Input id={id} data-autofocus value={title} onChange={(e) => setTitle(e.target.value)} />}</Field>
        <div className="flex min-w-0 flex-col items-start gap-1.5">
          <span className="label-caps">Shape</span>
          <Segmented label="Shape" options={SHAPES} value={shape} onChange={setShape} />
          <p className="font-mono text-meta text-muted">
            {shape === "parallel" ? "Every task is a root: none waits on another." : "Each task needs the one above it, so they run one after another."}
          </p>
        </div>
        <ol aria-label="Tasks" className="border border-line">
          {order.map((t, i) => (
            <li key={t.id} className="flex min-h-11 items-center gap-2 border-b border-line px-3 py-1.5 last:border-b-0 md:min-h-9">
              {shape === "chain" ? <span className="w-4 shrink-0 font-mono text-meta text-muted tabular">{i + 1}</span> : null}
              <span className="min-w-0 flex-1 truncate text-small">
                <span className="mr-1.5 font-mono text-meta text-muted tabular">#{t.id}</span>
                {t.title}
              </span>
              {shape === "chain" ? (
                <span className="flex shrink-0">
                  <Button
                    variant="ghost"
                    small
                    aria-label={`Move #${t.id} up`}
                    disabled={i === 0}
                    onClick={() => setOrder((o) => moveItem(o, i, -1))}
                    className="w-8 px-0 md:w-6"
                  >
                    <IconChevronUp size={12} />
                  </Button>
                  <Button
                    variant="ghost"
                    small
                    aria-label={`Move #${t.id} down`}
                    disabled={i === order.length - 1}
                    onClick={() => setOrder((o) => moveItem(o, i, 1))}
                    className="w-8 px-0 md:w-6"
                  >
                    <IconChevronDown size={12} />
                  </Button>
                </span>
              ) : null}
            </li>
          ))}
        </ol>
        <div className="flex min-w-0 flex-col items-start gap-1.5">
          <span className="label-caps">Start mode</span>
          <Segmented label="Start mode" options={MODES} value={mode} onChange={setMode} />
          <p className="font-mono text-meta text-muted">
            {mode === "auto" ? "A task starts on its own once every need is completed. Roots wait for you." : "A task reads ready when its needs are completed; you start it."}
          </p>
        </div>
        <ErrorText error={create.error} />
      </div>
    </Drawer>
  );
}
