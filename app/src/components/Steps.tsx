// Stepped setup UI shared by the connection screen and T3 setup: mono step
// rows (a square marker in the step's tone, or the square run-tone spinner on
// the active step) and the remedy command box with Copy.
import type { ReactNode } from "react";
import { useCopy } from "./StageLinks.tsx";
import { Button, cx, toneClass, type ChipTone } from "./ui.tsx";

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
    <li className="flex min-h-10 items-center gap-3 border-b border-line px-3 last:border-b-0 md:min-h-8">
      <span aria-hidden className="flex size-2 shrink-0 items-center justify-center">
        {status === "active" ? (
          <span className="size-2 animate-spin border-[1.5px] border-run border-t-transparent motion-reduce:animate-none" />
        ) : (
          <span className={cx("size-1.5 bg-(--c) transition-colors duration-150", toneClass(STEP_TONE[status]))} />
        )}
      </span>
      <span className="font-mono text-meta whitespace-nowrap text-fg">{label}</span>
      <span title={note} className={cx("ml-auto min-w-0 truncate font-mono text-meta", tabular && "tabular", NOTE_CLASS[status])}>
        {note}
      </span>
    </li>
  );
}

/** Copy button whose label confirms the copy (announced politely). */
export function CopyAction({ text, label, small = true }: { text: string; label: string; small?: boolean }) {
  const [status, copy] = useCopy();
  return (
    <>
      <Button small={small} onClick={() => copy(text)}>
        {status === "copied" ? "Copied" : status === "failed" ? "Copy failed" : label}
      </Button>
      <span className="sr-only" aria-live="polite">
        {status === "copied" ? `${label}: copied` : status === "failed" ? `${label}: copy failed` : ""}
      </span>
    </>
  );
}

/** A remedy command in a Bench Gray mono box with Copy. */
export function CommandBox({ command }: { command: string }) {
  return (
    <div className="flex items-start gap-2 border border-line bg-panel px-3 py-2">
      <code className="min-w-0 flex-1 font-mono text-detail break-all text-fg">{command}</code>
      <CopyAction text={command} label="Copy" />
    </div>
  );
}
