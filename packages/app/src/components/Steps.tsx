// Stepped setup UI shared by the connection screen and T3 setup: mono step
// rows (a square marker in the step's tone, or the square run-tone spinner on
// the active step). The remedy command box is CommandBox in ui.
import type { ReactNode } from "react";
import { Marker, Row, cx, type ChipTone } from "./ui.tsx";

export type StepStatus = "idle" | "active" | "ok" | "need" | "bad";

const STEP_TONE: Record<StepStatus, ChipTone> = { idle: "idle", active: "run", ok: "ok", need: "need", bad: "bad" };

const NOTE_CLASS: Record<StepStatus, string> = {
  idle: "text-muted",
  active: "text-muted",
  ok: "text-muted",
  need: "text-need-fg",
  bad: "text-bad",
};

/** Ordered list of `Step` rows inside a flush Panel. */
export function Steps({ label, children }: { label: string; children: ReactNode }) {
  return <ol aria-label={label}>{children}</ol>;
}

/** `tabular` keeps a live count from jittering. */
export function Step({ label, note, status, tabular = false }: { label: string; note: string; status: StepStatus; tabular?: boolean }) {
  return (
    <Row bare className="flex min-h-10 items-center gap-3 px-3 md:min-h-8">
      <span aria-hidden className="flex size-2 shrink-0 items-center justify-center">
        {status === "active" ? (
          <span className="size-2 animate-spin border-[1.5px] border-run border-t-transparent motion-reduce:animate-none" />
        ) : (
          <Marker tone={STEP_TONE[status]} />
        )}
      </span>
      <span className="font-mono text-meta whitespace-nowrap text-fg">{label}</span>
      <span title={note} className={cx("ml-auto min-w-0 truncate font-mono text-meta", tabular && "tabular", NOTE_CLASS[status])}>
        {note}
      </span>
    </Row>
  );
}
