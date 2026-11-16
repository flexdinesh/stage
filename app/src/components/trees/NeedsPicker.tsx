import type { Task } from "../../api/types.ts";
import { Select } from "../ui.tsx";

/**
 * Adds a need from cycle-safe candidates (see `needCandidates`): a select that resets after each pick. Disabled with
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
    <Select
      small
      id={id}
      aria-label="Add a need"
      value=""
      disabled={disabled || none}
      onChange={(e) => {
        const picked = Number(e.target.value);
        if (Number.isSafeInteger(picked) && picked > 0) onAdd(picked);
      }}
      className="w-full"
    >
      <option value="">{none ? "No task can be added without a loop" : "Add a need…"}</option>
      {candidates.map((t) => (
        <option key={t.id} value={t.id}>
          #{t.id} {t.title}
        </option>
      ))}
    </Select>
  );
}
