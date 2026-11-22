import type { ReactNode } from "react";
import { cx } from "./cx.ts";

/** Semantic tone → CSS class from index.css (sets --c / --cf). */
export type ChipTone = "need" | "run" | "done" | "ok" | "bad" | "idle" | "plain";

const TONE_CLASS: Record<ChipTone, string> = {
  need: "tone-need",
  run: "tone-run",
  done: "tone-done",
  ok: "tone-ok",
  bad: "tone-bad",
  idle: "tone-idle",
  plain: "tone-plain",
};

export const toneClass = (t: ChipTone) => TONE_CLASS[t];

/** Tinted-border chip; `dot` adds the square state marker. */
export function Chip({
  tone = "plain",
  dot = false,
  title,
  className,
  children,
}: {
  tone?: ChipTone;
  dot?: boolean;
  title?: string;
  className?: string | undefined;
  children: ReactNode;
}) {
  return (
    <span title={title} className={cx("chip", dot && "chip-dot", TONE_CLASS[tone], className)}>
      {children}
    </span>
  );
}

export function Kbd({ className, children }: { className?: string; children: ReactNode }) {
  return (
    <kbd className={cx("border border-line bg-panel px-1 font-mono text-label leading-4 text-muted", className)}>
      {children}
    </kbd>
  );
}
