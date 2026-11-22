import { Suspense, lazy } from "react";
import { useHarnesses } from "../api/queries.ts";
import type { TaskState, TaskDetail } from "../api/types.ts";
import { Hint, Panel, RelTime, cx, linkClass, CopyButton } from "./ui.tsx";
import { useSessionDestination } from "../lib/hostState.ts";

// marked + DOMPurify load only when a reply is shown.
const ReplyMarkdown = lazy(() => import("./ReplyMarkdown.tsx"));

/** Rows that show the reply preview: the ones waiting on the user to read it. */
const PREVIEW_STATES: readonly TaskState[] = ["needs_input", "done"];

/**
 * The thread's latest assistant reply (read-only glance; T3 stays the place to talk). Hidden until sync has read one.
 */
export function LastWordPanel({ task }: { task: TaskDetail }) {
  const thread = task.thread;
  const reply = task.finalReply;
  const word = reply ? { ...reply, at: reply.updatedAt } : thread?.lastWord;
  const name = useHarnesses().kindName(task.harness);
  const destination = useSessionDestination();
  if (!word) return null;
  const earlier = word.runId !== null && thread?.latestRunId != null && word.runId !== thread.latestRunId;
  const href = destination(task);
  return (
    <Panel
      title="Last word"
      meta={
        <>
          <RelTime at={word.at} className="tabular" />
          {earlier ? " · earlier run" : null}
        </>
      }
      actions={<CopyButton text={word.text} label="Copy" />}
    >
      <Suspense fallback={<div className="prose-reply whitespace-pre-wrap">{word.text}</div>}>
        <ReplyMarkdown text={word.text} refs />
      </Suspense>
      {word.truncated ? (
        <Hint className="mt-3 border-t border-line pt-2.5">
          Excerpt.{" "}
          {href === null ? (
            `Full reply in ${name}`
          ) : (
            <a href={href} className={cx(linkClass("rest"), "text-fg")}>
              Full reply in {name}
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
