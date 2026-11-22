import { useId, type ComponentProps, type ReactNode } from "react";
import { IconCheck } from "../icons.tsx";
import { cx } from "./cx.ts";

const inputBase =
  "border border-line-strong bg-surface px-2.5 text-input text-fg transition-[border-color,box-shadow] duration-150 focus:border-accent focus:shadow-[0_0_0_1px_var(--accent)] focus:outline-none disabled:opacity-55";
export const inputClass = `h-control ${inputBase}`;

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

/** A single-line control's box: the input's, or the compact one for dense panels. */
export function controlClass(small: boolean): string {
  return small ? `h-control-sm py-0 text-input-sm ${inputBase}` : inputClass;
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
            // The group's 1px border on each side brings it to control height.
            small ? "h-control-sm px-2.5 text-meta-sm md:px-2" : "h-[calc(var(--control-h)-2px)] px-3 text-detail md:px-2.5",
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
