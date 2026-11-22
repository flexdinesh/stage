import type { ComponentProps, ReactNode } from "react";
import { IconChevronDown } from "../icons.tsx";
import { cx } from "./cx.ts";

/** An icon from components/icons.tsx: `size` in px; decorative unless labelled. */
export type IconComponent = (props: { size?: number; label?: string }) => ReactNode;

export type Variant = "primary" | "secondary" | "danger" | "ghost";

const VARIANTS: Record<Variant, string> = {
  primary: "border-accent bg-accent text-on-accent hover:opacity-85",
  secondary: "border-line-strong bg-surface text-fg hover:bg-panel",
  danger: "border-bad/45 bg-surface text-bad hover:bg-bad/8",
  ghost: "border-transparent text-muted hover:bg-panel hover:text-fg",
};

const BUTTON_BASE =
  "inline-flex items-center justify-center gap-1.5 whitespace-nowrap border transition-[background-color,opacity,color] duration-150 disabled:pointer-events-none disabled:opacity-45";

export function buttonClass(variant: Variant = "secondary", small = false): string {
  return cx(BUTTON_BASE, small ? "h-control-sm px-2.5 text-meta font-medium md:px-2" : "h-control px-3.5 text-small font-medium md:px-3", VARIANTS[variant]);
}

/** A square icon-only button's box: control height on both axes. */
export function iconButtonClass(variant: Variant = "ghost", small = false): string {
  return cx(BUTTON_BASE, "shrink-0", small ? "size-control-sm" : "size-control", VARIANTS[variant]);
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

type IconButtonProps = Omit<ComponentProps<"button">, "children" | "aria-label"> & {
  /** Accessible name; also the tooltip unless `title` is given. */
  label: string;
  icon: IconComponent;
  variant?: Variant;
  small?: boolean;
};

/** Square icon-only button. Icons draw at 16px (14px small); the label names it for screen readers and the tooltip. */
export function IconButton({ label, icon: Icon, variant, small = false, className, title, type, ...rest }: IconButtonProps) {
  return (
    <button type={type ?? "button"} aria-label={label} title={title ?? label} className={cx(iconButtonClass(variant, small), className)} {...rest}>
      <Icon size={small ? 14 : 16} />
    </button>
  );
}

/** Text that acts (a tree node title, a snippet name): inherits type, underlines on hover. */
export function TextButton({ className, type, ...rest }: ComponentProps<"button">) {
  return (
    <button
      type={type ?? "button"}
      className={cx("min-w-0 text-left underline-offset-2 hover:underline disabled:no-underline disabled:opacity-45", className)}
      {...rest}
    />
  );
}

/**
 * A secondary button that opens a choice (project scope, server, a "More" menu): its label, then a chevron.
 * `mono` sets the label as metadata (a project or server name).
 */
export function PickerButton({
  mono = false,
  className,
  type,
  children,
  ...rest
}: Omit<ComponentProps<"button">, "className"> & { mono?: boolean; className?: string }) {
  return (
    <button
      type={type ?? "button"}
      className={cx(BUTTON_BASE, VARIANTS.secondary, "h-control", mono ? "px-2.5 font-mono text-meta" : "px-3.5 text-small font-medium md:px-3", className)}
      {...rest}
    >
      {children}
      <IconChevronDown size={12} />
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
