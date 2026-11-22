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
  className,
  children,
}: {
  tone?: "need" | "bad" | "plain";
  actions?: ReactNode;
  /** Outer spacing only (margins). */
  className?: string;
  children: ReactNode;
}) {
  return (
    <div
      role={tone === "bad" ? "alert" : undefined}
      className={cx(
        toneClass(tone),
        "flex flex-wrap items-center gap-x-3 gap-y-2 border border-[color-mix(in_srgb,var(--c)_50%,transparent)] bg-[color-mix(in_srgb,var(--c)_9%,var(--surface))] px-3 py-2 text-small",
        className,
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

export function Muted({ className, children }: { className?: string; children: ReactNode }) {
  return <p className={cx("text-small text-muted", className)}>{children}</p>;
}

/**
 * A one-line problem inside a row or beside a control (last error, failed upload, blocked reason): mono red text.
 * Block errors with a recovery use `Alert tone="bad"` (or `ErrorText`).
 */
export function InlineError({
  truncate = false,
  inline = false,
  className,
  children,
}: {
  /** One line with the full text in the tooltip. */
  truncate?: boolean;
  /** A span inside a line (a status note in a mono meta line). */
  inline?: boolean;
  className?: string;
  children: ReactNode;
}) {
  const Tag = inline ? "span" : "p";
  return (
    <Tag title={truncate && typeof children === "string" ? children : undefined} className={cx("font-mono break-words text-bad", !inline && "text-meta", truncate && "truncate", className)}>
      {children}
    </Tag>
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
 * stray Enter never confirms. `alternative` is an extra secondary action between them (e.g. a local-only fallback);
 * `disabled` locks every button while that alternative runs.
 */
export function ConfirmInline({
  confirmLabel,
  onConfirm,
  onCancel,
  danger = false,
  busy = false,
  disabled = false,
  alternative,
  error,
  children,
}: {
  confirmLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
  danger?: boolean;
  busy?: boolean;
  disabled?: boolean;
  alternative?: ReactNode;
  error?: unknown;
  children: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-2">
      <Alert
        tone="need"
        actions={
          <>
            <Button small variant={danger ? "danger" : "primary"} busy={busy} disabled={disabled} onClick={onConfirm}>
              {confirmLabel}
            </Button>
            {alternative}
            <Button small variant="ghost" autoFocus disabled={busy || disabled} onClick={onCancel}>
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
