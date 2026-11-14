import { useEffect } from "react";
import { Link, useParams } from "react-router";
import { useThread } from "../api/queries.ts";
import type { ThreadDetail } from "../api/types.ts";
import { RunOrderPanel } from "../components/Continuations.tsx";
import { PullRequestsPanel } from "../components/PullRequests.tsx";
import { RunsPanel } from "../components/RunsPanel.tsx";
import { CopyButton } from "../components/CopyButton.tsx";
import { LastWordPanel } from "../components/LastWord.tsx";
import { useMarkNotificationGroupsRead } from "../components/Notifications.tsx";
import { SessionOpenButton, SessionRows } from "../components/SessionList.tsx";
import { TaskStateBadge } from "../components/StateBadge.tsx";
import { Alert, Detail, Details, PageHeader, Panel, QueryState, buttonClass } from "../components/ui.tsx";
import { dateTime } from "../lib/format.ts";
import { sessionPath } from "../lib/sessions.ts";
import { closeSystemNotifications } from "../lib/push.ts";

export function SessionDetailPage() {
  const { id = "" } = useParams();
  const query = useThread(id);
  return <QueryState isPending={query.isPending} error={query.error}>{query.data === undefined ? null : <SessionDetail key={id} data={query.data} />}</QueryState>;
}

function SessionDetail({ data }: { data: ThreadDetail }) {
  const { thread, children, continuations, task } = data;
  const taskId = task?.id;
  // Viewing clears system notifications here and marks their groups read, so
  // other devices close theirs on their next sync.
  const markViewed = useMarkNotificationGroupsRead();
  useEffect(() => {
    void closeSystemNotifications(`thread-${thread.t3ThreadId}`);
    if (taskId !== undefined) void closeSystemNotifications(`task-${taskId}`);
    markViewed(taskId === undefined ? [`thread:${thread.t3ThreadId}`] : [`thread:${thread.t3ThreadId}`, `task:${taskId}`]);
  }, [thread.t3ThreadId, taskId, markViewed]);
  return <>
    <PageHeader title={thread.title} meta={<><Link to="/sessions" className="hover:underline">Sessions</Link>{` / ${thread.projectTitle ?? thread.t3ProjectId}`}{thread.archived ? " · archived" : ""}</>} actions={<><SessionOpenButton thread={thread} />{task === null ? null : <Link to={`/tasks/${task.id}`} className={buttonClass()}>Task #{task.id}</Link>}</>} />
    <div className="mb-3.5 flex flex-wrap items-center gap-2"><span className="text-small text-muted">Session</span><TaskStateBadge state={thread.state} />{thread.parentThreadId === null ? null : <Link to={sessionPath(thread.parentThreadId)} className="text-small underline">Inspect parent session</Link>}</div>
    <div className="flex flex-col gap-3.5">
      {thread.pendingRequestCount > 0 ? <Alert tone="need">{thread.pendingRequestCount} pending request{thread.pendingRequestCount === 1 ? "" : "s"}. Resolve input in T3 before queued delivery can proceed.</Alert> : null}
      <div className="grid items-start gap-3.5 lg:grid-cols-[minmax(0,1fr)_340px]">
        <div className="flex min-w-0 flex-col gap-3.5">
          <LastWordPanel thread={thread} />
          <RunOrderPanel thread={thread} entries={continuations} />
          <RunsPanel runs={thread.recentRuns ?? []} runCount={thread.runCount} />
          <Panel title="Child sessions" meta={`${children.length}`} flush><SessionRows threads={children} emptyText="No child sessions. Delegated work appears here when T3 reports it." /></Panel>
        </div>
        <div className="flex min-w-0 flex-col gap-3.5">
          <PullRequestsPanel prs={thread.pullRequests} />
          <Panel title="Session details">
            <Details>
              <Detail label="Session ID"><span className="flex min-w-0 flex-wrap items-center gap-2"><span className="break-all">{thread.t3ThreadId}</span><CopyButton text={thread.t3ThreadId} label="Copy" /></span></Detail>
              <Detail label="Project">{thread.projectId === null ? thread.projectTitle ?? thread.t3ProjectId : <Link to={`/projects/${thread.projectId}`} className="hover:underline">{thread.projectTitle ?? thread.t3ProjectId}</Link>}</Detail>
              <Detail label="Source">{thread.source}</Detail>
              <Detail label="Run status">{thread.status}</Detail>
              <Detail label="Settled">{thread.settled ? dateTime(thread.settledAt) : "No. Settle in T3."}</Detail>
              <Detail label="Pending input">{thread.pendingRequestCount}</Detail>
              <Detail label="Provider">{thread.providerInstanceId ?? "T3 default"}</Detail>
              <Detail label="Model">{thread.model ?? "T3 default"}</Detail>
              <Detail label="Runtime">{thread.runtimeMode ?? "T3 default"}</Detail>
              <Detail label="Interaction">{thread.interactionMode ?? "default"}</Detail>
              <Detail label="Branch">{thread.branch ?? "Project root"}</Detail>
              <Detail label="Worktree">{thread.worktreePath ?? "Project root"}</Detail>
              <Detail label="Active run">{thread.activeRunId ?? "None"}</Detail>
              <Detail label="Latest run">{thread.latestRunId ?? "None"}</Detail>
              <Detail label="Created">{dateTime(thread.createdAt)}</Detail>
              <Detail label="Activity">{dateTime(thread.updatedAt)}</Detail>
              <Detail label="Synced">{dateTime(thread.syncedAt)}</Detail>
            </Details>
          </Panel>
        </div>
      </div>
    </div>
  </>;
}
