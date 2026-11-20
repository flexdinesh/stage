import type { Task } from "../../api/types.ts";
import { Combobox } from "../Combobox.tsx";

/**
 * Adds a need from cycle-safe candidates (see `needCandidates`): a combobox that resets after each pick. Disabled with
 * no candidates.
 */
export function NeedsPicker({
  candidates,
  onAdd,
  disabled = false,
  id,
}: {
  candidates: readonly Pick<Task, "id" | "title">[];
  onAdd: (id: number) => void;
  disabled?: boolean;
  id?: string;
}) {
  const none = candidates.length === 0;
  return (
    <Combobox
      small
      id={id}
      aria-label="Add a need"
      value=""
      disabled={disabled || none}
      onChange={(next) => {
        const picked = Number(next);
        if (Number.isSafeInteger(picked) && picked > 0) onAdd(picked);
      }}
      listLabel="Tasks"
      placeholder={none ? "No task can be added without a loop" : "Add a need…"}
      className="w-full"
      options={candidates.map((t) => ({ value: String(t.id), label: `#${t.id} ${t.title}` }))}
    />
  );
}
