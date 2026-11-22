import { useId } from "react";
import type { Handoff } from "../api/types.ts";
import { dateTime } from "../lib/format.ts";
import { handoffSummary, recordedSource } from "../lib/handoff.ts";
import { browserTimeZone, wallTime } from "../lib/schedules.ts";
import { RefText } from "./StageLinks.tsx";
import { Checkbox, CopyButton } from "./ui.tsx";

/** "Hand off context" checkbox row: the whole row is the label (44px phone, 32px laptop), mono hint below. */
export function HandoffToggle({
  checked,
  onChange,
  hint,
  disabled = false,
}: {
  checked: boolean;
  onChange: (checked: boolean) => void;
  hint: string;
  disabled?: boolean;
}) {
  const hintId = useId();
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <label className="-mx-1.5 flex min-h-11 cursor-pointer items-center gap-3 px-1.5 transition-colors duration-150 hover:bg-panel has-disabled:cursor-not-allowed md:min-h-8">
        <Checkbox checked={checked} disabled={disabled} aria-describedby={hintId} onChange={(e) => onChange(e.target.checked)} />
        <span className="text-small text-fg">Hand off context</span>
      </label>
      <p id={hintId} className="font-mono text-meta text-muted">
        {hint}
      </p>
    </div>
  );
}

/** The current launch attempt's handoff: a ruled section with the sent block (collapsed), or a skip line. */
export function HandoffRecord({ handoff }: { handoff: Handoff }) {
  if (handoff.status === "skipped") {
    return <p className="font-mono text-meta text-muted">Handoff skipped: {handoff.reason ?? "no reason recorded"}</p>;
  }
  return (
    <section className="flex min-w-0 flex-col gap-2 border-t border-line pt-3" aria-label="Handoff">
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <h3 className="min-w-0 truncate label-caps text-fg">Handoff from {recordedSource(handoff)}</h3>
        <span className="font-mono text-meta-sm text-muted" title={dateTime(handoff.at)}>
          sent {wallTime(handoff.at, browserTimeZone(), { short: true })}
        </span>
        <div className="ml-auto">
          <CopyButton text={handoff.text} label="Copy handoff" />
        </div>
      </div>
      <p className="font-mono text-meta text-muted">{handoffSummary(handoff)}</p>
      <details>
        <summary className="cursor-pointer text-small text-muted hover:text-fg">Handoff text</summary>
        <pre className="mt-2 max-h-96 overflow-auto font-mono text-small leading-relaxed whitespace-pre-wrap"><RefText text={handoff.text} /></pre>
      </details>
    </section>
  );
}
