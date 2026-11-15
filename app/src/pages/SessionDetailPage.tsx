import { useEffect } from "react";
import { Link, useParams } from "react-router";
import { useThread } from "../api/queries.ts";
import type { ThreadDetail } from "../api/types.ts";
import { RunOrderPanel } from "../components/Continuations.tsx";
import { PullRequestsPanel } from "../components/PullRequests.tsx";
import { RunsPanel } from "../components/RunsPanel.tsx";
import { CopyButton } from "../components/CopyButton.tsx";
import { CopyLinkPair } from "../components/StageLinks.tsx";
import { LastWordPanel } from "../components/LastWord.tsx";
import { SessionPromptPanel } from "../components/PromptPanel.tsx";
import { CreatedTasksPanel } from "../components/CreatedTasks.tsx";
import { useMarkNotificationGroupsRead } from "../components/Notifications.tsx";
import { SessionOpenButton, SessionRows } from "../components/SessionList.tsx";
import { TaskStateBadge } from "../components/StateBadge.tsx";
import { DetailViewTabs, Transcript, isLiveState, useDetailView } from "../components/TranscriptView.tsx";
import { NeedsInputSlot } from "../components/NeedsInputPanel.tsx";
import { Detail, Details, PageHeader, Panel, QueryState, buttonClass } from "../components/ui.tsx";
import { dateTime } from "../lib/format.ts";
import { sessionPath } from "../lib/sessions.ts";
import { usePlatform } from "../platform/context.tsx";

export function SessionDetailPage() {
  const { id = "" } = useParams();
  const query = useThread(id);
  return <QueryState isPending={query.isPending} error={query.error}>{query.data === undefined ? null : <SessionDetail key={id} data={query.data} />}</QueryState>;
}

function SessionDetail({ data }: { data: ThreadDetail }) {
  const { thread, children, continuations, task, createdTasks } = data;
  const taskId = task?.id;
  // Viewing clears system notifications here and marks their groups read, so
  // other devices close theirs on their next sync.
  const { notifications } = usePlatform();
  const markViewed = useMarkNotificationGroupsRead();
  const [view, setView] = useDetailView();
  useEffect(() => {
    void notifications.closeShown(`thread-${thread.t3ThreadId}`);
    if (taskId !== undefined) void notifications.closeShown(`task-${taskId}`);
    markViewed(taskId === undefined ? [`thread:${thread.t3ThreadId}`] : [`thread:${thread.t3ThreadId}`, `task:${taskId}`]);
  }, [notifications, thread.t3ThreadId, taskId, markViewed]);
  return <>
    <PageHeader title={thread.title} meta={<><Link to="/sessions" className="hover:underline">Sessions</Link>{` / ${thread.projectTitle ?? thread.t3ProjectId}`}{thread.archived ? " · archived" : ""}</>} actions={<><CopyLinkPair subject={{ kind: "session", id: thread.t3ThreadId, title: thread.title }} /><SessionOpenButton thread={thread} />{task === null ? null : <Link to={`/tasks/${task.id}`} className={buttonClass()}>Task #{task.id}</Link>}</>} />
    <div className="mb-3.5 flex flex-wrap items-center gap-2"><span className="text-small text-muted">Session</span><TaskStateBadge state={thread.state} />{thread.parentThreadId === null ? null : <Link to={sessionPath(thread.parentThreadId)} className="text-small underline">Inspect parent session</Link>}</div>
    <div className="mb-3.5"><DetailViewTabs view={view} onChange={setView} live={isLiveState(thread.state)} /></div>
    <div className="flex flex-col gap-3.5">
      <NeedsInputSlot pending={thread.pendingRequestCount} threadId={thread.t3ThreadId} links={thread} />
      <div className="grid items-start gap-3.5 lg:grid-cols-[minmax(0,1fr)_340px]">
        <div className="flex min-w-0 flex-col gap-3.5">
          {view === "transcript" ? <Transcript threadId={thread.t3ThreadId} jumpOffset="session" /> : <>
            <SessionPromptPanel detail={data} live={isLiveState(thread.state)} />
            <LastWordPanel thread={thread} />
            <CreatedTasksPanel threadId={thread.t3ThreadId} batch={createdTasks} />
            <RunOrderPanel thread={thread} entries={continuations} />
            <RunsPanel runs={thread.recentRuns ?? []} runCount={thread.runCount} />
            <Panel title="Child sessions" meta={`${children.length}`} flush><SessionRows threads={children} emptyText="No child sessions. Delegated work appears here when T3 reports it." /></Panel>
          </>}
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
