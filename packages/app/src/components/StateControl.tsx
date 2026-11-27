import { useState, type ReactNode } from "react";
import { useHarnesses } from "../api/queries.ts";
import type { Task, TreeDetail } from "../api/types.ts";
import { stateMove, stateSummary } from "../lib/completion.ts";
import { statusChips } from "../lib/tasks.ts";
import { CompletionDrawer } from "./Completion.tsx";
import { useLaunchGate } from "./LaunchGate.tsx";
import { statusChipView } from "./StateBadge.tsx";
import { Combobox, Marker, Muted } from "./ui.tsx";

/**
 * The State picker: the task's current status and the one manual change it allows (Completed, or back from it), each
 * with its consequence on the same line. Picking a change opens its confirm drawer; the picker always shows the current
 * state. `action` holds buttons that act (Start, Retry, Open in T3), never a state change. `small` for dense panels.
 */
export function StateControl({ task, tree, action, small = false }: { task: Task; tree: TreeDetail | null; action?: ReactNode; small?: boolean }) {
  const [confirming, setConfirming] = useState(false);
  const { descriptor } = useHarnesses();
  const blocked = useLaunchGate()(task) !== null;
  const current = statusChipView(statusChips(task, { blocked })[0] ?? task.state);
  const move = stateMove(task, new Set((tree?.tasks ?? []).flatMap((t) => (t.acceptanceId === null ? [] : [t.id]))));
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <Combobox
        small={small}
        aria-label="Task state"
        listLabel="Task states"
        value="current"
        onChange={() => setConfirming(true)}
        className="w-full font-mono"
        leading={<Marker tone={current.tone} />}
        options={[
          { value: "current", label: current.label },
          { value: "move", label: statusChipView(move.to).label, tag: move.detail, disabled: move.disabled },
        ]}
      />
      <Muted>{stateSummary(task, undefined, descriptor(task.harness))}</Muted>
      {action}
      {confirming ? <CompletionDrawer task={task} reopen={move.kind === "uncomplete"} to={move.to} onClose={() => setConfirming(false)} /> : null}
    </div>
  );
}
