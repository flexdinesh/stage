import { useId } from "react";
import type { Handoff, SteeringBrief } from "../api/types.ts";
import { dateTime } from "../lib/format.ts";
import { handoffSummary, recordedSource, steeringSummary } from "../lib/handoff.ts";
import { browserTimeZone, wallTime } from "../lib/schedules.ts";
import { RefText } from "./StageLinks.tsx";
import { Checkbox, ChoiceRow, CopyButton, Hint, SubHeading } from "./ui.tsx";

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
      <ChoiceRow
        control={<Checkbox checked={checked} disabled={disabled} aria-describedby={hintId} onChange={(e) => onChange(e.target.checked)} />}
        className="has-disabled:cursor-not-allowed"
      >
        Hand off context
      </ChoiceRow>
      <Hint id={hintId}>{hint}</Hint>
    </div>
  );
}

/** The current launch attempt's handoff: a ruled section with the sent block (collapsed), or a skip line. */
export function HandoffRecord({ handoff }: { handoff: Handoff }) {
  if (handoff.status === "skipped") {
    return <Hint>Handoff skipped: {handoff.reason ?? "no reason recorded"}</Hint>;
  }
  return (
    <section className="flex min-w-0 flex-col gap-2 border-t border-line pt-3" aria-label="Handoff">
      <SubHeading
        title={`Handoff from ${recordedSource(handoff)}`}
        meta={<span title={dateTime(handoff.at)}>sent {wallTime(handoff.at, browserTimeZone(), { short: true })}</span>}
        actions={<CopyButton text={handoff.text} label="Copy handoff" />}
      />
      <p className="font-mono text-meta text-muted">{handoffSummary(handoff)}</p>
      <details>
        <summary className="cursor-pointer text-small text-muted hover:text-fg">Handoff text</summary>
        <pre className="mt-2 max-h-96 overflow-auto font-mono text-small leading-relaxed whitespace-pre-wrap"><RefText text={handoff.text} /></pre>
      </details>
    </section>
  );
}

/** A steerer's launch brief: the same ruled, collapsed section as a handoff (context, not state). */
export function SteeringBriefRecord({ steering }: { steering: SteeringBrief }) {
  return (
    <section className="flex min-w-0 flex-col gap-2 border-t border-line pt-3" aria-label="Steering brief">
      <SubHeading title="Steering brief" meta={`attempt ${steering.attempt}`} actions={<CopyButton text={steering.brief} label="Copy brief" />} />
      <p className="font-mono text-meta text-muted">{steeringSummary(steering)}</p>
      <details>
        <summary className="cursor-pointer text-small text-muted hover:text-fg">Brief text</summary>
        <pre className="mt-2 max-h-96 overflow-auto font-mono text-small leading-relaxed whitespace-pre-wrap"><RefText text={steering.brief} /></pre>
      </details>
    </section>
  );
}
