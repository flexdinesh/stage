import { useMutation } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { useInvalidateWork, useTask } from "../../api/queries.ts";
import type { TreeDetail, TreeSteerer } from "../../api/types.ts";
import { deliveryError, deliveryLine, holdLabel, steererPhase } from "../../lib/steering.ts";
import { useNow } from "../../lib/now.ts";
import { treeClosed } from "../../lib/trees.ts";
import { useApi } from "../../platform/context.tsx";
import { IconSteer } from "../icons.tsx";
import { TaskStateBadge } from "../StateBadge.tsx";
import { TaskFormModal } from "../TaskForm.tsx";
import { Alert, Button, ConfirmInline, Drawer, ErrorText, IdTag, Meta, Muted, TextLink } from "../ui.tsx";

/**
 * The tree view's steering strip (ADR 0013), under the header: Add steerer while none; the active steerer (state, link,
 * delivery, hold) with Replace and Stop steering; a Waiting Yellow alert once the steerer stopped, since member waits
 * then go to the user. A closed tree without a steerer shows nothing.
 */
export function SteeringStrip({ tree }: { tree: TreeDetail }) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const [drawer, setDrawer] = useState<"add" | "replace" | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [warnings, setWarnings] = useState<string[]>([]);
  const steerer = tree.steerer;
  const stop = useMutation({
    mutationFn: () => api.endTreeSteering(tree.id),
    onSuccess: (res) => {
      setConfirming(false);
      setWarnings(res.warnings);
    },
    onSettled: invalidate,
  });
  if (steerer === null && treeClosed(tree) && warnings.length === 0) return null;

  const open = (next: "add" | "replace") => {
    setWarnings([]);
    setDrawer(next);
  };

  return (
    <div className="flex flex-col gap-2">
      {steerer === null ? (
        <StripBox>
          <Muted className="min-w-0 flex-1">No steerer. A steerer task watches this tree, answers its members and accepts their work.</Muted>
          {treeClosed(tree) ? null : (
            <Button small onClick={() => open("add")} className="ml-auto">
              Add steerer
            </Button>
          )}
        </StripBox>
      ) : steererPhase(steerer.state) === "stopped" ? (
        <Alert
          tone="need"
          actions={
            <>
              <Button small variant="primary" onClick={() => open("replace")}>
                Replace
              </Button>
              <Button small variant="ghost" onClick={() => setConfirming(true)}>
                Stop steering
              </Button>
            </>
          }
        >
          Steerer stopped. Needs input goes to you. <SteererLink steerer={steerer} /> <TaskStateBadge state={steerer.state} className="ml-1 align-middle" />
        </Alert>
      ) : (
        <ActiveStrip tree={tree} steerer={steerer} onReplace={() => open("replace")} onStop={() => setConfirming(true)} />
      )}
      {confirming && steerer !== null ? (
        <ConfirmInline danger confirmLabel="Stop steering" busy={stop.isPending} onConfirm={() => stop.mutate()} onCancel={() => setConfirming(false)} error={stop.error}>
          Stops the steerer&apos;s session. Needs input goes to you.
        </ConfirmInline>
      ) : null}
      {warnings.length === 0 ? null : (
        <Alert tone="need" actions={<Button small variant="ghost" onClick={() => setWarnings([])}>Dismiss</Button>}>
          {warnings.map((w, i) => (
            <span key={i} className="block break-words">
              {w}
            </span>
          ))}
        </Alert>
      )}
      {drawer === "add" ? (
        <TaskFormModal steering={{ treeId: tree.id, treeTitle: tree.title }} onClose={() => setDrawer(null)} onSteered={(res) => setWarnings(res.warnings)} />
      ) : null}
      {drawer === "replace" && steerer !== null ? (
        <ReplaceDrawer tree={tree} steererTaskId={steerer.taskId} onClose={() => setDrawer(null)} onWarnings={setWarnings} />
      ) : null}
    </div>
  );
}

/** The strip's box: a Strong Hairline line like the task detail tree bar, no fill. */
function StripBox({ children }: { children: ReactNode }) {
  return (
    <section aria-label="Steering" className="flex flex-wrap items-center gap-x-3 gap-y-2 border border-line-strong bg-surface py-row pr-2.5 pl-3 md:pr-2">
      <span className="flex shrink-0 items-center gap-2 text-muted">
        <IconSteer />
        <span className="label-caps max-md:hidden">Steerer</span>
      </span>
      {children}
    </section>
  );
}

function SteererLink({ steerer }: { steerer: Pick<TreeSteerer, "taskId" | "title"> }) {
  return (
    <span className="inline-flex min-w-0 items-baseline gap-1.5">
      <IdTag id={steerer.taskId} title={`Steerer task #${steerer.taskId}`} />
      <TextLink to={`/tasks/${steerer.taskId}`} className="min-w-0 font-medium break-words">
        {steerer.title}
      </TextLink>
    </span>
  );
}

function ActiveStrip({ tree, steerer, onReplace, onStop }: { tree: TreeDetail; steerer: TreeSteerer; onReplace: () => void; onStop: () => void }) {
  // The delivery line's "2m ago" keeps moving between tree refreshes.
  const now = useNow(30_000);
  const error = deliveryError(steerer);
  const starting = steererPhase(steerer.state) === "starting";
  return (
    <StripBox>
      <span className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1">
        <TaskStateBadge state={steerer.state} />
        <SteererLink steerer={steerer} />
      </span>
      <Meta className="tabular">
        {starting ? "Starting" : deliveryLine(steerer, now)} · hold {holdLabel(tree.steeringHoldSeconds).toLowerCase()}
      </Meta>
      <span className="ml-auto flex shrink-0 gap-1.5">
        <Button small onClick={onReplace}>
          Replace
        </Button>
        <Button small variant="ghost" onClick={onStop}>
          Stop steering
        </Button>
      </span>
      {error === null ? null : (
        <p title={error} className="basis-full truncate font-mono text-meta text-need-fg">
          Last delivery failed: {error}
        </p>
      )}
    </StripBox>
  );
}

/** Replace opens the steerer drawer prefilled from the current steerer task, once it has loaded. */
function ReplaceDrawer({ tree, steererTaskId, onClose, onWarnings }: { tree: TreeDetail; steererTaskId: number; onClose: () => void; onWarnings: (w: string[]) => void }) {
  const current = useTask(steererTaskId);
  if (current.data === undefined) {
    return (
      <Drawer title={`Replace steerer #${steererTaskId}`} onClose={onClose} wide>
        {current.error === null ? <Muted>Loading the current steerer…</Muted> : <ErrorText error={current.error} />}
      </Drawer>
    );
  }
  return (
    <TaskFormModal
      steering={{ treeId: tree.id, treeTitle: tree.title, replacing: current.data }}
      onClose={onClose}
      onSteered={(res) => onWarnings(res.warnings)}
    />
  );
}
