import { Suspense, lazy } from "react";
import type { TaskState, TaskSessionSnapshot } from "../api/types.ts";
import { Hint, Panel, RelTime, cx, linkClass, CopyButton } from "./ui.tsx";

// marked + DOMPurify load only when a reply is shown.
const ReplyMarkdown = lazy(() => import("./ReplyMarkdown.tsx"));

/** Rows that show the reply preview: the ones waiting on the user to read it. */
const PREVIEW_STATES: readonly TaskState[] = ["needs_input", "done"];

/**
 * The session's latest assistant reply (read-only glance; the harness stays the place to talk). Hidden until sync has read one.
 */
export function LastWordPanel({ reply, harness, href }: { reply: TaskSessionSnapshot["reply"] | undefined; harness: string; href: string | null }) {
  if (reply == null) return null;
  return (
    <Panel
      title="Last word"
      meta={<RelTime at={reply.updatedAt} className="tabular" />}
      actions={<CopyButton text={reply.text} label="Copy" />}
    >
      <Suspense fallback={<div className="prose-reply whitespace-pre-wrap">{reply.text}</div>}>
        <ReplyMarkdown text={reply.text} refs />
      </Suspense>
      {reply.truncated ? (
        <Hint className="mt-3 border-t border-line pt-2.5">
          Excerpt.{" "}
          {href === null ? (
            `Full reply in ${harness}`
          ) : (
            <a href={href} className={cx(linkClass("rest"), "text-fg")}>
              Full reply in {harness}
            </a>
          )}
        </Hint>
      ) : null}
    </Panel>
  );
}

/** One clamped line of the latest reply under a needs-input or done row's meta line. */
export function LastWordLine({
  state,
  preview,
  className,
}: {
  state: TaskState;
  preview: string | null;
  className?: string;
}) {
  if (preview === null || !PREVIEW_STATES.includes(state)) return null;
  return (
    <p title={preview} className={cx("truncate text-small text-muted", className)}>
      {preview}
    </p>
  );
}
