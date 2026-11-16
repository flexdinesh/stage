import { Link } from "react-router";
import type { Thread } from "../api/types.ts";
import { dateTime, relativeTime } from "../lib/format.ts";
import { sessionPath } from "../lib/sessions.ts";
import { LastWordLine } from "./LastWord.tsx";
import { PrChips } from "./PullRequests.tsx";
import { AnswerButton } from "./NeedsInputPanel.tsx";
import { RowCopyLink } from "./StageLinks.tsx";
import { TaskStateBadge } from "./StateBadge.tsx";
import { AppButton, Button, ExternalButton, cx } from "./ui.tsx";
import { useShowT3Links } from "../lib/hostState.ts";

/** Needs input with questions stage can answer: Answer leads, Open in T3 steps back. */
const answerable = (thread: Thread) => thread.state === "needs_input" && thread.pendingQuestionCount > 0;

export function SessionOpenButton({ thread }: { thread: Thread }) {
  const showT3 = useShowT3Links();
  if (!showT3) return null;
  const variant = thread.state === "needs_input" && !answerable(thread) ? "primary" : "secondary";
  if (thread.t3DeepLink !== null) return <AppButton small variant={variant} href={thread.t3DeepLink}>Open in T3</AppButton>;
  const href = thread.t3WebUrl ?? thread.t3AppUrl;
  return href === null ? null : <ExternalButton small variant={variant} href={href}>Open in T3</ExternalButton>;
}

export function SessionRows({ threads, emptyText = "No sessions match. Clear filters or try another search." }: { threads: Thread[]; emptyText?: string }) {
  if (threads.length === 0) return <p className="px-3 py-3 text-small text-muted">{emptyText}</p>;
  return (
    <ul>
      {threads.map((thread) => (
        <li key={thread.t3ThreadId} className={cx("group/row grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-3 gap-y-2 border-b border-l-2 border-l-transparent border-line px-3 py-2.5 last:border-b-0 md:grid-cols-[118px_minmax(0,1fr)_auto] md:items-center", thread.state === "needs_input" && "border-l-need bg-need/5")}>
          <span className="order-2 md:order-1"><TaskStateBadge state={thread.state} /></span>
          <div className="order-1 min-w-0 md:order-2">
            <Link to={sessionPath(thread.t3ThreadId)} className="text-body font-medium break-words hover:underline">{thread.title}</Link>
            <div className="mt-1 flex flex-wrap gap-x-2.5 gap-y-1 font-mono text-meta text-muted">
              <span>{thread.projectTitle ?? thread.t3ProjectId}</span>
              <span>{thread.model ?? thread.providerInstanceId ?? "T3 default"}</span>
              <span>{thread.source}</span>
              {thread.parentThreadId === null ? null : <span>child session</span>}
              {thread.archived ? <span>archived</span> : null}
              <time dateTime={thread.updatedAt} title={dateTime(thread.updatedAt)}>{relativeTime(thread.updatedAt)}</time>
              <PrChips prs={thread.pullRequests} detailTo={sessionPath(thread.t3ThreadId)} />
            </div>
            <LastWordLine state={thread.state} preview={thread.lastWordPreview} className="mt-1 hidden md:block" />
          </div>
          <LastWordLine state={thread.state} preview={thread.lastWordPreview} className="order-3 col-span-2 md:hidden" />
          <div className="order-3 col-span-2 flex flex-wrap gap-2 md:col-span-1">
            {answerable(thread) ? <AnswerButton to={sessionPath(thread.t3ThreadId)} /> : null}
            <SessionOpenButton thread={thread} />
            <RowCopyLink subject={{ kind: "session", id: thread.t3ThreadId, title: thread.title }} />
          </div>
        </li>
      ))}
    </ul>
  );
}

export function SessionPager({ page, total, size, disabled = false, onPage }: { page: number; total: number; size: number; disabled?: boolean; onPage: (page: number) => void }) {
  const first = total === 0 ? 0 : page * size + 1;
  const last = Math.min(total, (page + 1) * size);
  return (
    <nav aria-label="Session pages" className="flex flex-wrap items-center justify-between gap-2">
      <span className="tabular font-mono text-meta text-muted" aria-live="polite">{first}-{last} of {total}</span>
      <div className="flex gap-2">
        <Button small disabled={disabled || page === 0} onClick={() => onPage(page - 1)}>Previous</Button>
        <Button small disabled={disabled || last >= total} onClick={() => onPage(page + 1)}>Next</Button>
      </div>
    </nav>
  );
}
