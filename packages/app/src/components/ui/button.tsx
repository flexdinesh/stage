import type { ComponentProps, ReactNode } from "react";
import { cx } from "./cx.ts";

export type Variant = "primary" | "secondary" | "danger" | "ghost";

const VARIANTS: Record<Variant, string> = {
  primary: "border-accent bg-accent text-on-accent hover:opacity-85",
  secondary: "border-line-strong bg-surface text-fg hover:bg-panel",
  danger: "border-bad/45 bg-surface text-bad hover:bg-bad/8",
  ghost: "border-transparent text-muted hover:bg-panel hover:text-fg",
};

export function buttonClass(variant: Variant = "secondary", small = false): string {
  return cx(
    "inline-flex items-center justify-center gap-1.5 whitespace-nowrap border font-medium transition-[background-color,opacity,color] duration-150 disabled:pointer-events-none disabled:opacity-45",
    small ? "h-control-sm px-2.5 text-meta md:px-2" : "h-control px-3.5 text-small md:px-3",
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
