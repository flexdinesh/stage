import type { ReactNode } from "react";
import { capCount } from "../../lib/format.ts";
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

/**
 * A count on a square icon button's top-right corner (the button needs `relative`), capped at 99+, so the button
 * never widens. Plain is ink; need and bad fill their state color. Decorative: the button's name carries the count.
 */
export function CornerBadge({ count, tone = "plain" }: { count: number; tone?: "plain" | "need" | "bad" }) {
  return (
    <span aria-hidden="true" className={cx("corner-badge", tone === "plain" ? undefined : TONE_CLASS[tone])}>
      {capCount(count)}
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
