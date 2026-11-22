import { Children, Fragment, type ComponentProps, type ReactNode } from "react";
import { Link } from "react-router";
import { dateTime, relativeTime } from "../../lib/format.ts";
import { cx } from "./cx.ts";
import { toneClass, type ChipTone } from "./tone.tsx";

/** 6px square state marker (never a dot). Decorative: the text beside it names the state. */
export function Marker({ tone, size = "sm", className }: { tone: ChipTone; size?: "sm" | "md"; className?: string }) {
  return (
    <span
      aria-hidden
      className={cx("shrink-0 bg-(--c) transition-colors duration-150", size === "sm" ? "size-1.5" : "size-[7px]", toneClass(tone), className)}
    />
  );
}

/** Mono Slate metadata: project names, models, times, ids, counts. `small` for dense headers. */
export function Meta({ small = false, className, children, title }: { small?: boolean; className?: string; children: ReactNode; title?: string }) {
  return (
    <span title={title} className={cx("font-mono text-muted", small ? "text-meta-sm" : "text-meta", className)}>
      {children}
    </span>
  );
}

/** A metadata line: the present items joined with ` · `. Empty items (null, false, "") drop out. */
export function MetaLine({ className, children }: { className?: string; children: ReactNode }) {
  const items = Children.toArray(children).filter((c) => c !== "");
  return (
    <p className={cx("font-mono text-meta text-muted", className)}>
      {items.map((item, i) => (
        <Fragment key={i}>
          {i === 0 ? null : " · "}
          {item}
        </Fragment>
      ))}
    </p>
  );
}

/** A task or tree number before its title: `#51`, mono Slate, tabular, never wrapped apart from the title. */
export function IdTag({ id, title, className }: { id: number; title?: string; className?: string }) {
  return (
    <span title={title} className={cx("shrink-0 font-mono text-meta whitespace-nowrap text-muted tabular", className)}>
      #{id}
    </span>
  );
}

/** Help text under or beside a control: Geist (prose is never mono), Slate. `inline` for a span inside a label or line. */
export function Hint({
  id,
  inline = false,
  title,
  className,
  children,
}: {
  id?: string;
  inline?: boolean;
  title?: string;
  className?: string;
  children: ReactNode;
}) {
  const Tag = inline ? "span" : "p";
  return (
    <Tag id={id} title={title} className={cx("text-meta text-muted", className)}>
      {children}
    </Tag>
  );
}

/** Relative time with the exact time on hover. */
export function RelTime({ at, prefix, className }: { at: string | null; prefix?: string; className?: string }) {
  if (at === null) return <span className={className}>-</span>;
  return (
    <time dateTime={at} title={dateTime(at)} className={className}>
      {prefix === undefined ? null : `${prefix} `}
      {relativeTime(at)}
    </time>
  );
}

/**
 * Link styles.
 * - `hover` (default): a title or name link; inherits color, underlines on hover.
 * - `rest`: always underlined with a Strong Hairline that goes Ink on hover (links inside meta lines and prose).
 * - `muted`: Slate, Ink and underlined on hover (location trails, secondary links).
 */
export type LinkVariant = "hover" | "rest" | "muted";

const LINK: Record<LinkVariant, string> = {
  hover: "underline-offset-2 hover:underline",
  rest: "underline decoration-line-strong underline-offset-3 transition-[text-decoration-color] duration-150 hover:decoration-fg",
  muted: "text-muted underline-offset-2 transition-colors duration-150 hover:text-fg hover:underline",
};

export function linkClass(variant: LinkVariant = "hover"): string {
  return LINK[variant];
}

type TextLinkProps = Omit<ComponentProps<typeof Link>, "className"> & { variant?: LinkVariant; className?: string };

/** In-app link (router). For an external URL use `ExternalLink`. */
export function TextLink({ variant = "hover", className, ...rest }: TextLinkProps) {
  return <Link className={cx(LINK[variant], className)} {...rest} />;
}

/** External link: opens in a new tab. */
export function ExternalLink({
  href,
  variant = "rest",
  className,
  title,
  children,
}: {
  href: string;
  variant?: LinkVariant;
  className?: string;
  title?: string;
  children: ReactNode;
}) {
  return (
    <a href={href} target="_blank" rel="noreferrer" title={title} className={cx(LINK[variant], className)}>
      {children}
    </a>
  );
}
