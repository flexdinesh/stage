import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { useInvalidateWork } from "../../api/queries.ts";
import type { StartMode, Task } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";
import { moveItem } from "../../lib/tasks.ts";
import { treeMembers, type TreeShape } from "../../lib/trees.ts";
import { IconChevronDown, IconChevronUp } from "../icons.tsx";
import { Button, Drawer, ErrorText, Field, FieldGroup, IconButton, IdTag, Input, Meta, Row, Segmented } from "../ui.tsx";


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
  const [mode, setMode] = useState<StartMode>("auto");
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
      <Meta small className="min-w-0 flex-1 md:text-right">
        {title.trim() === "" ? "required: title" : shape === "chain" ? `${order.length} in a chain` : `${order.length} roots`}
      </Meta>
      <Button variant="primary" busy={create.isPending} disabled={blocked} onClick={() => create.mutate()}>
        Create tree
      </Button>
    </>
  );

  return (
    <Drawer title={`New tree from ${tasks.length}`} onClose={onClose} footer={footer}>
      <div className="flex flex-col gap-4">
        <Field label="Title">{(id) => <Input id={id} data-autofocus value={title} onChange={(e) => setTitle(e.target.value)} />}</Field>
        <FieldGroup
          label="Shape"
          hint={shape === "parallel" ? "Every task is a root: none waits on another." : "Each task needs the one above it, so they run one after another."}
        >
          <div>
            <Segmented label="Shape" options={SHAPES} value={shape} onChange={setShape} />
          </div>
        </FieldGroup>
        <ol aria-label="Tasks" className="border border-line">
          {order.map((t, i) => (
            <Row key={t.id} className="flex min-h-11 items-center gap-2 md:min-h-9">
              {shape === "chain" ? <Meta className="w-4 shrink-0 tabular">{i + 1}</Meta> : null}
              <span className="min-w-0 flex-1 truncate text-small">
                <IdTag id={t.id} className="mr-1.5" />
                {t.title}
              </span>
              {shape === "chain" ? (
                <span className="flex shrink-0">
                  <IconButton
                    small
                    label={`Move #${t.id} up`}
                    icon={IconChevronUp}
                    disabled={i === 0}
                    onClick={() => setOrder((o) => moveItem(o, i, -1))}
                  />
                  <IconButton
                    small
                    label={`Move #${t.id} down`}
                    icon={IconChevronDown}
                    disabled={i === order.length - 1}
                    onClick={() => setOrder((o) => moveItem(o, i, 1))}
                  />
                </span>
              ) : null}
            </Row>
          ))}
        </ol>
        <FieldGroup
          label="Start mode"
          hint={mode === "auto" ? "A task starts on its own once every need is completed. Roots wait for you." : "A task reads ready when its needs are completed; you start it."}
        >
          <div>
            <Segmented label="Start mode" options={MODES} value={mode} onChange={setMode} />
          </div>
        </FieldGroup>
        <ErrorText error={create.error} />
      </div>
    </Drawer>
  );
}
