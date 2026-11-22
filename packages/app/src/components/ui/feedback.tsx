import type { ReactNode } from "react";
import { ApiError } from "../../api/client.ts";
import { Button } from "./button.tsx";
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
      <span aria-hidden className="size-[7px] shrink-0 bg-(--c)" />
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

/**
 * A one-line problem inside a row or beside a control (last error, failed upload, blocked reason): mono red text.
 * Block errors with a recovery use `Alert tone="bad"` (or `ErrorText`).
 */
export function InlineError({
  truncate = false,
  className,
  children,
}: {
  /** One line with the full text in the tooltip. */
  truncate?: boolean;
  className?: string;
  children: ReactNode;
}) {
  return (
    <p title={truncate && typeof children === "string" ? children : undefined} className={cx("font-mono text-meta break-words text-bad", truncate && "truncate", className)}>
      {children}
    </p>
  );
}

/**
 * Nothing to list: a muted line, plus the next step when there is one. `inset` pads it to sit as a row inside a
 * flush Panel.
 */
export function EmptyState({ inset = false, action, className, children }: { inset?: boolean; action?: ReactNode; className?: string; children: ReactNode }) {
  return (
    <div className={cx("flex flex-wrap items-center gap-x-3 gap-y-2", inset && "px-3 py-row", className)}>
      <p className="min-w-0 text-small text-muted">{children}</p>
      {action === undefined ? null : <div className="flex flex-wrap items-center gap-2">{action}</div>}
    </div>
  );
}

/**
 * Inline confirmation for an irreversible or wide action (delete, disconnect, complete all): a Waiting Yellow Alert
 * naming what happens, the confirm button (red for destructive, Ink otherwise) and Cancel, which takes focus so a
 * stray Enter never confirms.
 */
export function ConfirmInline({
  confirmLabel,
  onConfirm,
  onCancel,
  danger = false,
  busy = false,
  error,
  children,
}: {
  confirmLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
  danger?: boolean;
  busy?: boolean;
  error?: unknown;
  children: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-2">
      <Alert
        tone="need"
        actions={
          <>
            <Button small variant={danger ? "danger" : "primary"} busy={busy} onClick={onConfirm}>
              {confirmLabel}
            </Button>
            <Button small variant="ghost" autoFocus disabled={busy} onClick={onCancel}>
              Cancel
            </Button>
          </>
        }
      >
        {children}
      </Alert>
      <ErrorText error={error} />
    </div>
  );
}
