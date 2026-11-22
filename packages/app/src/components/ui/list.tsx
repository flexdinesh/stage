import type { ComponentProps, ReactNode } from "react";
import { cx } from "./cx.ts";

/** Current or highlighted row (sidebar nav, palette, pickers): Bench Gray fill boxed by an inset Strong Hairline. */
export const activeRowClass = "bg-panel shadow-[inset_0_0_0_1px_var(--line-strong)]";

/**
 * The needs-input edge (One Yellow Edge Rule): a 2px Waiting Yellow left edge. Rows that can carry it reserve the
 * space while clear so their content never shifts.
 */
export function needEdgeClass(need: boolean): string {
  return cx("border-l-2", need ? "border-l-need" : "border-l-transparent");
}

/** A ruled row's box: hairline under every row but the last, 12px sides, the row-padding token top and bottom. */
export const rowClass = "border-b border-line px-3 py-row last:border-b-0";

/** A ruled list inside a flush Panel (or a bordered box). Children are `Row`s or `<li>`s holding a row component. */
export function RowList({ label, className, children }: { label?: string; className?: string; children: ReactNode }) {
  return (
    <ul aria-label={label} className={className}>
      {children}
    </ul>
  );
}

/** One display row. `bare` drops the box (the child row component draws its own grid and padding). */
export function Row({
  need = false,
  bare = false,
  className,
  children,
  ...rest
}: Omit<ComponentProps<"li">, "className"> & { need?: boolean; bare?: boolean; className?: string }) {
  return (
    <li className={cx(bare ? "border-b border-line last:border-b-0" : rowClass, need && needEdgeClass(true), className)} {...rest}>
      {children}
    </li>
  );
}

/**
 * A whole-row control (pick a server, a task, a project, a history entry): full width, 44px on phone, hover fill,
 * and the active row box while `selected`.
 */
export function rowButtonClass(selected = false): string {
  return cx(
    "flex min-h-11 w-full min-w-0 items-center gap-2 px-3 py-1.5 text-left transition-colors duration-150 hover:bg-panel disabled:cursor-not-allowed disabled:opacity-45 disabled:hover:bg-transparent md:min-h-control",
    selected && activeRowClass,
  );
}

export function RowButton({
  selected = false,
  className,
  type,
  ...rest
}: Omit<ComponentProps<"button">, "className"> & { selected?: boolean; className?: string }) {
  return <button type={type ?? "button"} className={cx(rowButtonClass(selected), className)} {...rest} />;
}
