import { Fragment, useContext, useEffect, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Link } from "react-router";
import { NavLabelContext, TrailSlotContext, type TrailSegment } from "../navContext.ts";
import { cx } from "./cx.ts";

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
      {hasHeader ? <PanelHeader title={title} meta={meta} actions={actions} ruled={!collapsed} /> : null}
      {collapsed ? null : <div className={cx(!flush && "p-3", fill && "flex flex-1 flex-col")}>{children}</div>}
    </section>
  );
}

/**
 * The Bench Gray header bar of a Panel (or of a panel-like surface: a popover, a drawer section): uppercase mono
 * title in Ink, mono meta beside it, actions right. `ruled` draws the hairline under it.
 */
export function PanelHeader({
  title,
  meta,
  actions,
  ruled = true,
  as: Heading = "h2",
  className,
}: {
  title?: ReactNode;
  meta?: ReactNode;
  actions?: ReactNode;
  ruled?: boolean;
  as?: "h2" | "h3";
  className?: string;
}) {
  return (
    <div className={cx("relative flex min-h-8 flex-wrap items-center gap-x-2 gap-y-1 bg-panel px-3 py-1", ruled && "border-b border-line", className)}>
      {title === undefined ? null : <Heading className="label-caps text-fg [text-box:trim-both_cap_alphabetic]">{title}</Heading>}
      {meta === undefined ? null : <span className="block font-mono text-meta-sm text-muted [text-box:trim-both_cap_alphabetic]">{meta}</span>}
      {actions === undefined ? null : <div className="relative ml-auto flex flex-wrap items-center gap-1.5">{actions}</div>}
    </div>
  );
}

/** A page section's heading outside a Panel (Dashboard "In flight"): title size, mono meta beside it, actions right. */
export function SectionHeading({
  id,
  title,
  meta,
  actions,
  className,
}: {
  /** For `aria-labelledby` on the section. */
  id?: string;
  title: ReactNode;
  meta?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cx("mb-2 flex flex-wrap items-baseline gap-x-3 gap-y-1", className)}>
      <h2 id={id} className="text-title font-semibold">
        {title}
      </h2>
      {meta === undefined ? null : <span className="font-mono text-meta text-muted">{meta}</span>}
      {actions === undefined ? null : <div className="ml-auto flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

/** A group's label inside a panel body or drawer ("Needs", "Add note", "This device"): uppercase mono Slate. */
export function SubHeading({
  id,
  title,
  meta,
  actions,
  className,
}: {
  /** For `aria-labelledby` on the group. */
  id?: string;
  title: ReactNode;
  meta?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cx("flex min-h-6 flex-wrap items-center gap-x-2 gap-y-1", className)}>
      <h3 id={id} className="label-caps">
        {title}
      </h3>
      {meta === undefined ? null : <span className="font-mono text-meta-sm text-muted">{meta}</span>}
      {actions === undefined ? null : <div className="ml-auto flex flex-wrap items-center gap-1.5">{actions}</div>}
    </div>
  );
}

/** A page's location as links ("stage / nav-history / task 12"); the last step is the current page. */
export function Trail({ segments, linkClass }: { segments: readonly TrailSegment[]; linkClass: string }) {
  return segments.map((s, i) => (
    <Fragment key={`${i}:${s.label}`}>
      {i === 0 ? null : <span aria-hidden> / </span>}
      {s.to === undefined ? (
        <span className={i === segments.length - 1 ? "text-fg" : undefined}>{s.label}</span>
      ) : (
        <Link to={s.to} className={linkClass}>
          {s.label}
        </Link>
      )}
    </Fragment>
  ));
}

const metaLinkClass = "text-fg underline-offset-2 hover:underline";
const barLinkClass = "text-muted underline-offset-2 transition-colors duration-150 hover:text-fg hover:underline";

/**
 * Page title, meta line and actions. `trail` is the page's location: in the laptop content bar when the shell has one,
 * and at the start of the meta line on narrower screens. The title (or the trail's last step) names the page in the
 * history list. `subtitle` is accepted as an alias of `meta`.
 */
export function PageHeader({
  title,
  meta,
  subtitle,
  actions,
  trail,
}: {
  title: ReactNode;
  meta?: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
  trail?: readonly TrailSegment[];
}) {
  const line = meta ?? subtitle;
  const slot = useContext(TrailSlotContext);
  const nameEntry = useContext(NavLabelContext);
  const name = typeof title === "string" ? title : trail?.at(-1)?.label;
  useEffect(() => {
    if (name !== undefined) nameEntry?.(name);
  }, [name, nameEntry]);
  const inBar = slot !== null && trail !== undefined;
  return (
    <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
      {inBar ? createPortal(<Trail segments={trail} linkClass={barLinkClass} />, slot) : null}
      <div className="min-w-0">
        <h1 className="text-headline leading-tight font-bold tracking-[-0.025em] break-words text-balance">
          {title}
        </h1>
        {line === undefined && trail === undefined ? null : (
          <div className={cx("mt-1 font-mono text-detail text-muted", inBar && line === undefined && "lg:hidden")}>
            <span className="font-bold text-fg">{"//"}</span>{" "}
            {trail === undefined ? null : (
              <span className={cx(inBar && "lg:hidden")}>
                <Trail segments={trail} linkClass={metaLinkClass} />
                {line === undefined ? null : " · "}
              </span>
            )}
            {line}
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
