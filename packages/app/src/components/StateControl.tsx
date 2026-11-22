import { useState, type ReactNode } from "react";
import { useHarnesses } from "../api/queries.ts";
import type { Task, TreeDetail } from "../api/types.ts";
import { stateMove, stateSummary } from "../lib/completion.ts";
import { statusChips } from "../lib/tasks.ts";
import { CompletionDrawer } from "./Completion.tsx";
import { statusChipView } from "./StateBadge.tsx";
import { Combobox, Marker, Muted } from "./ui.tsx";

/**
 * The State picker: the task's current status and the one manual change it allows (Completed, or back from it), each
 * with its consequence. Picking a change opens its confirm drawer; the picker always shows the current state. `action`
 * holds buttons that act (Start, Retry, Open in T3), never a state change.
 */
export function StateControl({ task, tree, action }: { task: Task; tree: TreeDetail | null; action?: ReactNode }) {
  const [confirming, setConfirming] = useState(false);
  const { descriptor } = useHarnesses();
  const current = statusChipView(statusChips(task)[0] ?? task.state);
  const move = stateMove(task, new Set((tree?.tasks ?? []).flatMap((t) => (t.acceptanceId === null ? [] : [t.id]))));
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <Combobox
        small
        aria-label="Task state"
        listLabel="Task states"
        value="current"
        onChange={() => setConfirming(true)}
        className="w-full font-mono"
        leading={<Marker tone={current.tone} />}
        options={[
          { value: "current", label: current.label, detail: "current" },
          { value: "move", label: statusChipView(move.to).label, detail: move.detail, disabled: move.disabled },
        ]}
      />
      <Muted>{stateSummary(task, undefined, descriptor(task.harness))}</Muted>
      {action}
      {confirming ? <CompletionDrawer task={task} reopen={move.kind === "uncomplete"} to={move.to} onClose={() => setConfirming(false)} /> : null}
    </div>
  );
}
