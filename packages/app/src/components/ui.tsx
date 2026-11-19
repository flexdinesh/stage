import { useEffect, useId, useRef, type ComponentProps, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { ApiError } from "../api/client.ts";
import { IconCheck, IconClose } from "./icons.tsx";

export function cx(...parts: (string | false | null | undefined)[]): string {
  return parts.filter(Boolean).join(" ");
}

/** Current or highlighted row (sidebar nav, palette, pickers): Bench Gray fill boxed by an inset Strong Hairline. */
export const activeRowClass = "bg-panel shadow-[inset_0_0_0_1px_var(--line-strong)]";

// ---- Tones ----

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

// ---- Buttons ----

type Variant = "primary" | "secondary" | "danger" | "ghost";

const VARIANTS: Record<Variant, string> = {
  primary: "border-accent bg-accent text-on-accent hover:opacity-85",
  secondary: "border-line-strong bg-surface text-fg hover:bg-panel",
  danger: "border-bad/45 bg-surface text-bad hover:bg-bad/8",
  ghost: "border-transparent text-muted hover:bg-panel hover:text-fg",
};

export function buttonClass(variant: Variant = "secondary", small = false): string {
  return cx(
    "inline-flex items-center justify-center gap-1.5 whitespace-nowrap border font-medium transition-[background-color,opacity,color] duration-150 disabled:pointer-events-none disabled:opacity-45",
    small ? "h-8 px-2.5 text-meta md:h-6 md:px-2" : "h-10 px-3.5 text-small md:h-8 md:px-3",
    VARIANTS[variant],
  );
}

type ButtonProps = ComponentProps<"button"> & { variant?: Variant; small?: boolean; busy?: boolean };

export function Button({ variant, small, busy = false, className, disabled, type, children, ...rest }: ButtonProps) {
  return (
    <button
      type={type ?? "button"}
      disabled={disabled === true || busy}
      aria-busy={busy || undefined}
      className={cx(buttonClass(variant, small), className)}
      {...rest}
    >
      {busy ? <span className="size-3 animate-spin border-[1.5px] border-current border-t-transparent" /> : null}
      {children}
    </button>
  );
}

/** External link styled as a button (opens in a new tab). */
export function ExternalButton({
  href,
  variant,
  small,
  className,
  children,
}: {
  href: string;
  variant?: Variant;
  small?: boolean;
  className?: string;
  children: ReactNode;
}) {
  return (
    <a href={href} target="_blank" rel="noreferrer" className={cx(buttonClass(variant, small), className)}>
      {children}
    </a>
  );
}

/** Link to the T3 app's t3code:// scheme: same tab, so the OS hands it to the app instead of opening a blank tab. */
export function AppButton({
  href,
  variant,
  small,
  className,
  children,
}: {
  href: string;
  variant?: Variant;
  small?: boolean;
  className?: string;
  children: ReactNode;
}) {
  return (
    <a href={href} className={cx(buttonClass(variant, small), className)}>
      {children}
    </a>
  );
}

// ---- Form controls ----

const inputBase =
  "border border-line-strong bg-surface px-2.5 text-input text-fg transition-[border-color,box-shadow] duration-150 focus:border-accent focus:shadow-[0_0_0_1px_var(--accent)] focus:outline-none disabled:opacity-55";
export const inputClass = `h-10 md:h-8 ${inputBase}`;

export function Input({ className, ...rest }: ComponentProps<"input">) {
  return <input className={cx(inputClass, "w-full", className)} {...rest} />;
}

export function Textarea({ className, ...rest }: ComponentProps<"textarea">) {
  return (
    <textarea
      className={cx(inputBase, "w-full py-2 font-mono text-input-mono leading-relaxed", className)}
      {...rest}
    />
  );
}

export function Select({ className, small = false, ...rest }: ComponentProps<"select"> & { small?: boolean }) {
  return (
    <select
      className={cx(inputBase, small ? "h-8 py-0 text-input-sm md:h-6" : "h-10 md:h-8", "select-chevron appearance-none pr-8", className)}
      {...rest}
    />
  );
}

/** Square checkbox: native input (keyboard, forms, a11y) with an Ink fill and authored check. */
export function Checkbox({ className, ...rest }: Omit<ComponentProps<"input">, "type">) {
  return (
    <span className={cx("relative inline-flex size-4 shrink-0", className)}>
      <input
        type="checkbox"
        className="peer size-4 cursor-pointer appearance-none border border-line-strong bg-surface transition-colors duration-150 checked:border-accent checked:bg-accent focus-visible:shadow-[0_0_0_1px_var(--surface),0_0_0_2px_var(--accent)] focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-55"
        {...rest}
      />
      <span className="pointer-events-none absolute inset-0 hidden items-center justify-center text-on-accent peer-checked:flex">
        <IconCheck size={12} />
      </span>
    </span>
  );
}

/** Square radio (Zero Radius Rule: never a circle): native input with an Ink square when checked. */
export function Radio({ className, ...rest }: Omit<ComponentProps<"input">, "type">) {
  return (
    <span className={cx("relative inline-flex size-4 shrink-0", className)}>
      <input
        type="radio"
        className="peer size-4 cursor-pointer appearance-none border border-line-strong bg-surface transition-colors duration-150 checked:border-accent focus-visible:shadow-[0_0_0_1px_var(--surface),0_0_0_2px_var(--accent)] focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-55"
        {...rest}
      />
      <span className="pointer-events-none absolute inset-[4px] hidden bg-accent peer-checked:block" />
    </span>
  );
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
    <div className={cx("flex min-w-0 flex-col gap-1.5", className)}>
      <label htmlFor={id} className="label-caps">
        {label}
      </label>
      {children(id)}
      {hint === undefined ? null : <p className="text-meta text-muted">{hint}</p>}
    </div>
  );
}

/** Joined button group for small enumerations (runtime mode, time window). */
export function Segmented<T extends string>({
  options,
  value,
  onChange,
  label,
  small = false,
  fill = false,
}: {
  /** `short` replaces `label` below 768px. */
  options: readonly { value: T; label: string; short?: string }[];
  value: T;
  onChange: (value: T) => void;
  label: string;
  small?: boolean;
  /** Phone: span the full width in equal cells (never wraps). */
  fill?: boolean;
}) {
  return (
    <div
      role="group"
      aria-label={label}
      className={cx(
        "max-w-full border border-line-strong bg-surface",
        fill ? "flex w-full md:inline-flex md:w-auto" : "inline-flex flex-wrap",
      )}
    >
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          aria-pressed={o.value === value}
          aria-label={o.short === undefined ? undefined : o.label}
          onClick={() => onChange(o.value)}
          className={cx(
            "border-r border-line-strong font-mono whitespace-nowrap transition-colors duration-150 last:border-r-0",
            small ? "h-8 px-2.5 text-meta-sm md:h-6 md:px-2" : "h-[38px] px-3 text-detail md:h-[30px] md:px-2.5",
            fill && "min-w-0 flex-1 max-md:px-1 md:flex-none",
            o.value === value ? "bg-accent text-on-accent" : "text-muted hover:bg-panel hover:text-fg",
          )}
        >
          {o.short === undefined ? (
            o.label
          ) : (
            <>
              <span className="md:hidden">{o.short}</span>
              <span className="max-md:hidden">{o.label}</span>
            </>
          )}
        </button>
      ))}
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
        TONE_CLASS[tone],
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

function Skeleton() {
  return (
    <div aria-busy="true" aria-label="Loading" className="flex flex-col gap-2">
      {[0, 1, 2].map((i) => (
        <div key={i} className="h-9 animate-pulse border border-line bg-panel" />
      ))}
    </div>
  );
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
  if (isPending) return <Skeleton />;
  if (error !== null && error !== undefined) return <ErrorText error={error} />;
  if (empty === true) return <Muted>{emptyText}</Muted>;
  return children;
}

// ---- Layout ----

/** Bordered panel with an uppercase mono header bar. `flush` drops body padding (for row lists). */
export function Panel({
  title,
  meta,
  actions,
  flush = false,
  fill = false,
  collapsed = false,
  className,
  children,
}: {
  title?: ReactNode;
  meta?: ReactNode;
  actions?: ReactNode;
  flush?: boolean;
  /** Body stretches to the panel height (e.g. a grid row set by a taller sibling). */
  fill?: boolean;
  /** Header only; the body is not rendered. */
  collapsed?: boolean;
  className?: string | undefined;
  children: ReactNode;
}) {
  const hasHeader = title !== undefined || actions !== undefined || meta !== undefined;
  return (
    <section className={cx("min-w-0 border border-line bg-surface", fill && "flex flex-col", className)}>
      {hasHeader ? (
        <div className={cx("relative flex min-h-8 flex-wrap items-center gap-x-2 gap-y-1 bg-panel px-3 py-1", !collapsed && "border-b border-line")}>
          {title === undefined ? null : <h2 className="label-caps text-fg [text-box:trim-both_cap_alphabetic]">{title}</h2>}
          {meta === undefined ? null : <span className="block font-mono text-meta-sm text-muted [text-box:trim-both_cap_alphabetic]">{meta}</span>}
          {actions === undefined ? null : <div className="relative ml-auto flex flex-wrap items-center gap-1.5">{actions}</div>}
        </div>
      ) : null}
      {collapsed ? null : <div className={cx(!flush && "p-3", fill && "flex flex-1 flex-col")}>{children}</div>}
    </section>
  );
}

/** Page title with a `//` meta line under it. `subtitle` is accepted as an alias of `meta`. */
export function PageHeader({
  title,
  meta,
  subtitle,
  actions,
}: {
  title: ReactNode;
  meta?: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
}) {
  const line = meta ?? subtitle;
  return (
    <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
      <div className="min-w-0">
        <h1 className="text-headline leading-tight font-bold tracking-[-0.025em] break-words text-balance">
          {title}
        </h1>
        {line === undefined ? null : (
          <div className="mt-1 font-mono text-detail text-muted">
            <span className="font-bold text-fg">{"//"}</span> {line}
          </div>
        )}
      </div>
      {actions === undefined ? null : <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

/** Definition list grid; children are `<Detail>` rows. */
export function Details({ children }: { children: ReactNode }) {
  return <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1.5 text-small">{children}</dl>;
}

export function Detail({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted">{label}</dt>
      <dd className="min-w-0 font-mono text-detail break-words">{children}</dd>
    </>
  );
}

// ---- Drawer ----

const FOCUSABLE = 'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])';

/** Side drawer (laptop) / full-screen sheet (phone). Esc closes, focus is trapped and restored. */
export function Drawer({
  title,
  onClose,
  wide = false,
  footer,
  children,
}: {
  title: string;
  onClose: () => void;
  wide?: boolean;
  footer?: ReactNode;
  children: ReactNode;
}) {
  const panel = useRef<HTMLDivElement>(null);
  const onCloseRef = useRef(onClose);
  useEffect(() => {
    onCloseRef.current = onClose;
  });

  useEffect(() => {
    const previous = document.activeElement;
    const el = panel.current;
    const first = el?.querySelector<HTMLElement>("[data-autofocus]") ?? el?.querySelector<HTMLElement>(FOCUSABLE);
    first?.focus();
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.stopPropagation();
        onCloseRef.current();
        return;
      }
      if (e.key !== "Tab" || el === null) return;
      const items = [...el.querySelectorAll<HTMLElement>(FOCUSABLE)];
      const head = items[0];
      const tail = items[items.length - 1];
      if (head === undefined || tail === undefined) return;
      if (e.shiftKey && document.activeElement === head) {
        e.preventDefault();
        tail.focus();
      } else if (!e.shiftKey && document.activeElement === tail) {
        e.preventDefault();
        head.focus();
      }
    }
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = overflow;
      if (previous instanceof HTMLElement) previous.focus();
    };
  }, []);

  return createPortal(
    <div className="fixed inset-0 z-50">
      <div className="absolute inset-0 bg-scrim" onClick={onClose} />
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className={cx(
          "absolute inset-0 flex flex-col bg-bg md:inset-y-0 md:right-0 md:left-auto md:w-full md:border-l md:border-line-strong",
          wide ? "md:max-w-[680px]" : "md:max-w-[580px]",
        )}
      >
        <div className="flex h-[calc(3.25rem+env(safe-area-inset-top))] shrink-0 items-center gap-3 border-b border-line pt-[env(safe-area-inset-top)] pr-[max(1rem,env(safe-area-inset-right))] pl-[max(1rem,env(safe-area-inset-left))] md:h-12 md:pt-0 md:pr-4 md:pl-4">
          <h2 className="text-title font-semibold">{title}</h2>
          <div className="ml-auto flex items-center gap-3">
            <Kbd className="max-md:hidden">Esc</Kbd>
            <Button variant="ghost" small onClick={onClose} aria-label="Close">
              <IconClose />
            </Button>
          </div>
        </div>
        <div
          className={cx(
            "min-h-0 flex-1 overflow-y-auto p-4 max-md:pr-[max(1rem,env(safe-area-inset-right))] max-md:pl-[max(1rem,env(safe-area-inset-left))]",
            footer === undefined && "max-md:pb-[calc(1rem+env(safe-area-inset-bottom))]",
          )}
        >
          {children}
        </div>
        {footer === undefined ? null : (
          <div className="flex shrink-0 flex-wrap items-center gap-2 border-t border-line bg-panel px-4 py-3 max-md:pr-[max(1rem,env(safe-area-inset-right))] max-md:pb-[calc(0.75rem+env(safe-area-inset-bottom))] max-md:pl-[max(1rem,env(safe-area-inset-left))]">
            {footer}
          </div>
        )}
      </div>
    </div>,
    document.body,
  );
}
