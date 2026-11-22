import type { ComponentProps } from "react";
import { IconChevronDown } from "../icons.tsx";
import { cx } from "./cx.ts";

/** The fold chevron: points down open, right closed; 150ms turn, none with reduced motion. */
export function DisclosureChevron({ open, size = 12, className }: { open: boolean; size?: number; className?: string }) {
  return (
    <span aria-hidden className={cx("inline-flex shrink-0 transition-[color,transform] duration-150 motion-reduce:transition-none", !open && "-rotate-90", className)}>
      <IconChevronDown size={size} />
    </span>
  );
}

/**
 * A button that folds a section ("21 quiet projects", a group header): chevron then label, `aria-expanded`.
 * Style the label with `className` (type, color, height).
 */
export function DisclosureButton({
  open,
  className,
  type,
  children,
  ...rest
}: Omit<ComponentProps<"button">, "className" | "aria-expanded"> & { open: boolean; className?: string }) {
  return (
    <button type={type ?? "button"} aria-expanded={open} className={cx("group/fold inline-flex items-center gap-1.5", className)} {...rest}>
      <DisclosureChevron open={open} className="text-muted group-hover/fold:text-fg" />
      {children}
    </button>
  );
}
