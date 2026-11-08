import { useId, type ComponentProps, type ReactNode } from "react";
import { ApiError } from "../api/client.ts";

export function cx(...parts: (string | false | null | undefined)[]): string {
  return parts.filter(Boolean).join(" ");
}

// ---- Buttons ----

type Variant = "primary" | "secondary" | "danger" | "ghost";

const VARIANTS: Record<Variant, string> = {
  primary: "bg-blue-600 text-white hover:bg-blue-500 border-transparent",
  secondary:
    "bg-white text-neutral-800 border-neutral-300 hover:bg-neutral-100 dark:bg-neutral-900 dark:text-neutral-100 dark:border-neutral-700 dark:hover:bg-neutral-800",
  danger:
    "bg-white text-red-700 border-red-300 hover:bg-red-50 dark:bg-neutral-900 dark:text-red-400 dark:border-red-900 dark:hover:bg-red-950",
  ghost:
    "border-transparent text-neutral-600 hover:bg-neutral-100 dark:text-neutral-300 dark:hover:bg-neutral-800",
};

export function buttonClass(variant: Variant = "secondary", small = false): string {
  return cx(
    "inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-md border font-medium transition-colors disabled:pointer-events-none disabled:opacity-50",
    small ? "px-2 py-0.5 text-xs" : "px-3 py-1.5 text-sm",
    VARIANTS[variant],
  );
}

type ButtonProps = ComponentProps<"button"> & { variant?: Variant; small?: boolean; busy?: boolean };

export function Button({ variant, small, busy = false, className, disabled, type, children, ...rest }: ButtonProps) {
  return (
    <button
      type={type ?? "button"}
      disabled={disabled === true || busy}
      className={cx(buttonClass(variant, small), className)}
      {...rest}
    >
      {busy ? <span className="size-3 animate-spin rounded-full border-2 border-current border-t-transparent" /> : null}
      {children}
    </button>
  );
}

/** External link styled as a button (opens in a new tab). */
export function ExternalButton({
  href,
  variant,
  small,
  children,
}: {
  href: string;
  variant?: Variant;
  small?: boolean;
  children: ReactNode;
}) {
  return (
    <a href={href} target="_blank" rel="noreferrer" className={buttonClass(variant, small)}>
      {children}
    </a>
  );
}

// ---- Badge ----

export type Tone = "gray" | "blue" | "amber" | "green" | "red" | "violet" | "teal";

const TONES: Record<Tone, string> = {
  gray: "bg-neutral-100 text-neutral-700 ring-neutral-300 dark:bg-neutral-800 dark:text-neutral-300 dark:ring-neutral-700",
  blue: "bg-blue-50 text-blue-700 ring-blue-200 dark:bg-blue-950 dark:text-blue-300 dark:ring-blue-900",
  amber: "bg-amber-50 text-amber-800 ring-amber-200 dark:bg-amber-950 dark:text-amber-300 dark:ring-amber-900",
  green: "bg-green-50 text-green-700 ring-green-200 dark:bg-green-950 dark:text-green-300 dark:ring-green-900",
  red: "bg-red-50 text-red-700 ring-red-200 dark:bg-red-950 dark:text-red-300 dark:ring-red-900",
  violet: "bg-violet-50 text-violet-700 ring-violet-200 dark:bg-violet-950 dark:text-violet-300 dark:ring-violet-900",
  teal: "bg-teal-50 text-teal-700 ring-teal-200 dark:bg-teal-950 dark:text-teal-300 dark:ring-teal-900",
};

export function Badge({ tone = "gray", children }: { tone?: Tone; children: ReactNode }) {
  return (
    <span
      className={cx(
        "inline-flex items-center whitespace-nowrap rounded px-1.5 py-0.5 text-xs font-medium ring-1 ring-inset",
        TONES[tone],
      )}
    >
      {children}
    </span>
  );
}

// ---- Form controls ----

export const inputClass =
  "rounded-md border border-neutral-300 bg-white px-2.5 py-1.5 text-sm text-neutral-900 placeholder:text-neutral-400 focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500/30 disabled:opacity-60 dark:border-neutral-700 dark:bg-neutral-900 dark:text-neutral-100";

export function Input({ className, ...rest }: ComponentProps<"input">) {
  return <input className={cx(inputClass, "w-full", className)} {...rest} />;
}

export function Textarea({ className, ...rest }: ComponentProps<"textarea">) {
  return <textarea className={cx(inputClass, "w-full font-mono", className)} {...rest} />;
}

export function Select({ className, ...rest }: ComponentProps<"select">) {
  return <select className={cx(inputClass, "pr-8", className)} {...rest} />;
}

/** Label + control. `children` receives the generated id for the control. */
export function Field({
  label,
  hint,
  className,
  children,
}: {
  label: string;
  hint?: ReactNode;
  className?: string;
  children: (id: string) => ReactNode;
}) {
  const id = useId();
  return (
    <div className={cx("flex flex-col gap-1", className)}>
      <label htmlFor={id} className="text-xs font-medium text-neutral-600 dark:text-neutral-400">
        {label}
      </label>
      {children(id)}
      {hint === undefined ? null : <p className="text-xs text-neutral-500">{hint}</p>}
    </div>
  );
}

// ---- Feedback ----

export function errorText(error: unknown): string | null {
  if (error === null || error === undefined) return null;
  if (error instanceof ApiError) return `${error.message} (${error.status})`;
  if (error instanceof Error) return error.message;
  return String(error);
}

export function ErrorText({ error }: { error: unknown }) {
  const text = errorText(error);
  if (text === null) return null;
  return (
    <p className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900 dark:bg-red-950 dark:text-red-300">
      {text}
    </p>
  );
}

export function Muted({ children }: { children: ReactNode }) {
  return <p className="text-sm text-neutral-500">{children}</p>;
}

/** Loading / error / empty wrapper for a query result. */
export function QueryState({
  isPending,
  error,
  empty,
  emptyText = "Nothing here.",
  children,
}: {
  isPending: boolean;
  error: unknown;
  empty?: boolean;
  emptyText?: string;
  children: ReactNode;
}) {
  if (isPending) return <Muted>Loading…</Muted>;
  if (error !== null && error !== undefined) return <ErrorText error={error} />;
  if (empty === true) return <Muted>{emptyText}</Muted>;
  return children;
}

// ---- Layout ----

export function Card({ title, actions, className, children }: {
  title?: ReactNode;
  actions?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section
      className={cx(
        "rounded-lg border border-neutral-200 bg-white p-4 dark:border-neutral-800 dark:bg-neutral-900",
        className,
      )}
    >
      {title === undefined && actions === undefined ? null : (
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <h2 className="text-sm font-semibold">{title}</h2>
          {actions === undefined ? null : <div className="flex flex-wrap gap-2">{actions}</div>}
        </div>
      )}
      {children}
    </section>
  );
}

export function PageHeader({ title, subtitle, actions }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        <h1 className="text-xl font-semibold break-words">{title}</h1>
        {subtitle === undefined ? null : <div className="mt-1 text-sm text-neutral-500">{subtitle}</div>}
      </div>
      {actions === undefined ? null : <div className="flex flex-wrap gap-2">{actions}</div>}
    </div>
  );
}

/** Definition list grid; children are `<Detail>` rows. */
export function Details({ children }: { children: ReactNode }) {
  return <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1.5 text-sm">{children}</dl>;
}

export function Detail({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-neutral-500">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </>
  );
}

// ---- Modal ----

export function Modal({
  title,
  onClose,
  wide = false,
  children,
}: {
  title: string;
  onClose: () => void;
  wide?: boolean;
  children: ReactNode;
}) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-2 sm:p-6"
      onKeyDown={(e) => {
        if (e.key === "Escape") onClose();
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className={cx(
          "w-full rounded-lg border border-neutral-200 bg-white shadow-xl dark:border-neutral-800 dark:bg-neutral-900",
          wide ? "max-w-3xl" : "max-w-lg",
        )}
      >
        <div className="flex items-center justify-between border-b border-neutral-200 px-4 py-3 dark:border-neutral-800">
          <h2 className="font-semibold">{title}</h2>
          <Button variant="ghost" small onClick={onClose} aria-label="Close">
            ✕
          </Button>
        </div>
        <div className="p-4">{children}</div>
      </div>
    </div>
  );
}
