import type { ReactNode } from "react";
import { ApiError } from "../../api/client.ts";
import { cx } from "./cx.ts";
import { toneClass } from "./tone.tsx";

export function errorText(error: unknown): string | null {
  if (error === null || error === undefined) return null;
  if (error instanceof ApiError) return `${error.message} (${error.status})`;
  if (error instanceof Error) return error.message;
  return String(error);
}

/** Tinted callout; `need` for waiting-on-you, `bad` for errors. */
export function Alert({
  tone = "need",
  actions,
  children,
}: {
  tone?: "need" | "bad" | "plain";
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div
      role={tone === "bad" ? "alert" : undefined}
      className={cx(
        toneClass(tone),
        "flex flex-wrap items-center gap-x-3 gap-y-2 border border-[color-mix(in_srgb,var(--c)_50%,transparent)] bg-[color-mix(in_srgb,var(--c)_9%,var(--surface))] px-3 py-2 text-small",
      )}
    >
      <span className="size-[7px] shrink-0 bg-[var(--c)]" />
      <div className="min-w-0 flex-1">{children}</div>
      {actions === undefined ? null : <div className="flex flex-wrap gap-2">{actions}</div>}
    </div>
  );
}

export function ErrorText({ error }: { error: unknown }) {
  const text = errorText(error);
  if (text === null) return null;
  return <Alert tone="bad">{text}</Alert>;
}

export function Muted({ children }: { children: ReactNode }) {
  return <p className="text-small text-muted">{children}</p>;
}
