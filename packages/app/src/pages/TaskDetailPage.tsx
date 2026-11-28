import { LaunchRecovery } from "../components/LaunchRecovery.tsx";
import { useMutation } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { useApi, usePlatform } from "../platform/context.tsx";
import { useConnectionCapabilities, useConnectionOperation, useHarnesses, useInvalidateWork, useMachines, useSendMessage, useSessionRequests, useTask, useTaskActions } from "../api/queries.ts";
import { useSessionAction, useSessionDestination } from "../lib/hostState.ts";
import { SessionActionButton } from "../components/SessionAction.tsx";
import { RequestsPanel } from "../components/Requests.tsx";
import { FollowUpPanel } from "../components/FollowUp.tsx";
import { followUpView, mayFollowUp, replyPrompt } from "../lib/followup.ts";
import { type TaskDetail, type TaskEvent } from "../api/types.ts";
import { CopyLinkPair } from "../components/StageLinks.tsx";
import { HandoffRecord, SteeringBriefRecord } from "../components/Handoff.tsx";
import { IconArrowRight } from "../components/icons.tsx";
import { JournalPanel } from "../components/Journal.tsx";
import { AcceptedResult } from "../components/Completion.tsx";
import { ActorCause, ActorLabel } from "../components/ActorLabel.tsx";
import { BlockedReason, LaunchBlockAlert, useStartTask, useStopTask } from "../components/LaunchGate.tsx";
import { DeclaredActions } from "../components/DeclaredActions.tsx";
import { SessionToolbarActions } from "../components/SessionToolbarActions.tsx";
import { splitToolbarActions } from "../lib/connectionSetup.ts";
import { HarnessFacts } from "../components/HarnessPresentation.tsx";
import { taskWorkspaceLocation } from "../lib/taskForm.ts";
import { StateControl } from "../components/StateControl.tsx";
import { LastWordPanel } from "../components/LastWord.tsx";
import { UsagePanel } from "../components/Usage.tsx";
import { OpeningPrompt, PromptPanel } from "../components/PromptPanel.tsx";
import { TaskStateBadge, taskStateLabel } from "../components/StateBadge.tsx";
import { ImageStrip } from "../components/Images.tsx";
import { NoTreePanel, TreeBar, TreePanel } from "../components/trees/TreePanel.tsx";
import { SteeringPanel } from "../components/SteeringPanel.tsx";
import { heldAlert } from "../lib/steering.ts";
import { useNow } from "../lib/now.ts";
import { TaskFormModal } from "../components/TaskForm.tsx";
import {
  Alert,
  Button,
  Chip,
  CopyButton,
  Detail,
  Details,
  EmptyState,
  ErrorText,
  Meta,
  Muted,
  PageHeader,
  Panel,
  QueryState,
  RelTime,
  Row,
  Skeleton,
  SkeletonBar,
  TextLink,
  cx,
  ConfirmInline,
} from "../components/ui.tsx";
import { dateTime } from "../lib/format.ts";
import { pendingHandoffNote } from "../lib/handoff.ts";
import { useIdParam } from "../lib/params.ts";
import { SETTINGS_PATH } from "../lib/settings.ts";
import { browserTimeZone, wallTime } from "../lib/schedules.ts";
import { useMarkNotificationGroupsRead } from "../components/Notifications.tsx";
import { canStart, canStop, isActive } from "../lib/tasks.ts";
import { treeTaskPath } from "../lib/trees.ts";
import { isRetired, resumeCommand, retiredHint, retiredNotice } from "../lib/harnesses.ts";
import { ReplacedChip } from "../components/HarnessName.tsx";
import { LaunchMeta } from "../components/LaunchMeta.tsx";
import { launchParts } from "../lib/capabilities.ts";
import { HarnessConnectionState } from "../components/ConnectionState.tsx";
import { machineTag, withMachine } from "../lib/machines.ts";
import { NotFoundPage } from "./NotFoundPage.tsx";

export function TaskDetailPage() {
  const id = useIdParam();
  return id === null ? <NotFoundPage /> : <TaskDetailView id={id} />;
}

function TaskDetailView({ id }: { id: number }) {
  const task = useTask(id);
  // Viewing the task clears its system notification here and marks its group
  // read, so other devices close theirs on their next sync.
  const { notifications } = usePlatform();
  const markViewed = useMarkNotificationGroupsRead();
  useEffect(() => {
    void notifications?.closeShown(`task-${id}`);
    markViewed([`task:${id}`]);
  }, [notifications, id, markViewed]);
  return (
    <QueryState isPending={task.isPending} error={task.error} hasData={task.data !== undefined} skeleton={<TaskDetailSkeleton />}>
      {task.data === undefined ? null : <TaskBody key={id} task={task.data} />}
    </QueryState>
  );
}

/** Mono text lines as bars (prompt, replies). */
function LineBars({ widths }: { widths: readonly string[] }) {
  return (
    <div className="flex flex-col gap-2.5 py-1">
      {widths.map((w, i) => (
        <SkeletonBar key={i} className={cx("h-3", w)} />
      ))}
    </div>
  );
}

function StatePanelSkeleton({ className }: { className: string }) {
  return (
    <Panel title="State" className={className}>
      <div className="flex flex-col gap-2.5">
        <SkeletonBar className="h-control w-full" />
        <SkeletonBar className="h-3 w-40" />
      </div>
    </Panel>
  );
}

/**
 * TaskBody's frame while the task loads: header, chips, action row, then its two columns (State first on phones).
 * Titles known before the data render as text; everything else is bars.
 */
function TaskDetailSkeleton() {
  return (
    <Skeleton label="Loading task">
      <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-2.5 py-0.5">
          <SkeletonBar className="h-7 w-72 max-w-full" />
          <SkeletonBar className="h-3 w-36 lg:hidden" />
        </div>
        <SkeletonBar className="h-control-sm w-32" />
      </div>
      <div className="flex flex-col gap-3.5">
        <div className="flex flex-wrap items-center gap-2">
          <SkeletonBar className="h-control w-24" />
          <SkeletonBar className="h-control w-24" />
          <SkeletonBar className="h-control w-14" />
          <SkeletonBar className="ml-auto h-control w-20" />
        </div>
        <StatePanelSkeleton className="lg:hidden" />
        <div className="grid grid-cols-1 gap-3.5 lg:grid-cols-[minmax(0,1fr)_340px]">
          <div className="flex min-w-0 flex-col gap-3.5">
            <Panel title="Prompt">
              <LineBars widths={["w-full", "w-11/12", "w-full", "w-4/5", "w-2/3"]} />
            </Panel>
            <Panel title={<SkeletonBar onPanel className="h-2.5 w-28" />}>
              <LineBars widths={["w-5/6", "w-full", "w-1/2"]} />
            </Panel>
          </div>
          <div className="flex min-w-0 flex-col gap-3.5">
            <StatePanelSkeleton className="max-lg:hidden" />
            <Panel title="Tree">
              <LineBars widths={["w-full", "w-3/5"]} />
            </Panel>
            <Panel title={<SkeletonBar onPanel className="h-2.5 w-24" />}>
              <div className="flex flex-col gap-3 py-0.5">
                {["w-20", "w-16", "w-32", "w-12"].map((w) => (
                  <div key={w} className="flex items-center gap-4">
                    <SkeletonBar className="h-3 w-14" />
                    <SkeletonBar className={cx("h-3", w)} />
                  </div>
                ))}
              </div>
            </Panel>
          </div>
        </div>
      </div>
    </Skeleton>
  );
}

/** "10:42:03" today, "Nov 3 10:42" otherwise. */
function stamp(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const time = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
  if (d.toDateString() === new Date().toDateString()) return time;
  const day = d.toLocaleDateString([], { month: "short", day: "numeric" });
  return `${day} ${time.slice(0, 5)}`;
}

function TaskBody({ task }: { task: TaskDetail }) {
  const api = useApi();
  const { kindName, descriptor } = useHarnesses();
  const navigate = useNavigate();
  const invalidate = useInvalidateWork();
  const [form, setForm] = useState<"edit" | "duplicate" | null>(null);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const { start, block: launchBlock } = useStartTask(task);
  const remove = useMutation({
    mutationFn: () => api.deleteTask(task.id),
    onSuccess: async () => {
      await navigate("/");
      await invalidate();
    },
  });
  const archive = useMutation({
    mutationFn: (archived: boolean) => (archived ? api.archiveTask(task.id) : api.unarchiveTask(task.id)),
    onSettled: invalidate,
  });
  const archived = task.archivedAt !== null;

  const waiting = task.state === "needs_input";
  // A held wait: the tree's steerer answers first; the countdown moves on its own.
  const held = waiting ? task.hold : null;
  const now = useNow(15_000, held !== null);
  // A replaced connection keeps Start / Stop visible but disabled, with the reason.
  const retired = isRetired(task);
  const live = { ...task, connectionActive: true };
  const startable = canStart(live);
  const stoppable = canStop(live);
  const { stop, block: stopBlock } = useStopTask(task, stoppable);
  const actionError = start.error ?? stop.error ?? remove.error ?? archive.error;
  const block = startable && !retired ? launchBlock : null;
  const retiredReason = retired ? retiredHint(kindName(task.harness)) : undefined;
  const lockedHint = retiredReason ?? block?.reason;
  // Open in the harness, or (a harness without a destination to open) copy the command that resumes it in its own client.
  const action = useSessionAction()(task);
  const { actions, run } = useTaskActions(task, !retired && task.session !== null);
  const sessionActions = splitToolbarActions(actions);
  const phoneBar = action !== null || stoppable || actions.length > 0;
  const phoneBarRef = useRef<HTMLDivElement>(null);
  const [phoneBarHeight, setPhoneBarHeight] = useState(0);
  useEffect(() => {
    const bar = phoneBarRef.current;
    if (!phoneBar || bar === null) return;
    const observer = new ResizeObserver(() => setPhoneBarHeight(bar.offsetHeight));
    observer.observe(bar, { box: "border-box" });
    return () => observer.disconnect();
  }, [phoneBar]);
  // Requests the harness lets Stage answer: the panel below leads, the harness's own client stays as the alternative.
  const { requests, ready: requestsReady } = useSessionRequests({ taskId: task.id, connectionId: task.connectionId, needsInput: task.state === "needs_input" });
  const answerable = requests.length > 0;
  // A follow-up into the session: leads under the alert when its turn ended asking you, else sits under Last word.
  // The draft and the send live here so both survive the panel moving and the session working.
  const sendOp = useConnectionOperation(task.connectionId, "send", task.externalSessionId !== null && mayFollowUp(task.state));
  // Requests Stage can't list (the harness declares them): a needs-input session with none listed waits on one there.
  const unlisted = descriptor(task.harness)?.unlistedRequests ?? "";
  const followUp = followUpView(task, { offered: sendOp.offered, requestsReady, requestCount: requests.length, unlisted: unlisted !== "" });
  const replyLeads = followUp?.placement === "lead";
  const sendMessage = useSendMessage(task.id);
  const [draft, setDraft] = useState("");
  const [sentAt, setSentAt] = useState<string | null>(null);
  const composer =
    followUp === null ? null : (
      <FollowUpPanel
        view={followUp}
        harness={kindName(task.harness)}
        draft={draft}
        onDraft={setDraft}
        onSend={(text) =>
          // A Reply answers this wait: guard it by the task's revision so the first answer wins.
          sendMessage.mutate(replyLeads ? { text, expectedStateRevision: task.stateRevision } : { text }, {
            onSuccess: () => {
              // Keep whatever was typed while it sent.
              setDraft((d) => (d === text ? "" : d));
              setSentAt(new Date().toISOString());
            },
          })
        }
        sending={sendMessage.isPending}
        sentAt={sentAt}
        error={sendMessage.error}
      />
    );

  const openInApp = (variant: "primary" | "secondary", className?: string) => (
    <SessionActionButton action={action} name={kindName(task.harness)} variant={variant} connectionId={task.connectionId} {...(className === undefined ? {} : { className })} />
  );

  return (
    <div style={phoneBar ? { paddingBottom: phoneBarHeight } : undefined}>
      <PageHeader
        title={task.title}
        actions={<CopyLinkPair small subject={{ kind: "task", id: task.id, title: task.title }} />}
        trail={[
          { label: task.projectTitle, to: `/projects/${task.projectId}` },
          ...(task.tree === null ? [] : [{ label: task.tree.title, to: treeTaskPath(task.tree.id, task.id) }]),
          { label: `task ${task.id}` },
        ]}
        {...(task.startAt === null && task.scheduleId === null
          ? {}
          : {
              meta: (
                <>
                  {task.startAt === null ? null : (
                    <span title={dateTime(task.startAt)}>{`starts ${wallTime(task.startAt, task.startTz ?? browserTimeZone(), { zone: true })}`}</span>
                  )}
                  {task.startAt !== null && task.scheduleId !== null ? " · " : null}
                  {task.scheduleId === null ? null : (
                    <>
                      {"from "}
                      <TextLink to={`/schedules/${task.scheduleId}`} className="text-fg">
                        {task.scheduleTitle ?? `schedule #${task.scheduleId}`}
                      </TextLink>
                    </>
                  )}
                </>
              ),
            })}
      />
      {/* State is in the State panel and launch metadata is in the session panel. */}
      {archived || task.adopted || retired ? (
        <div className="-mt-1 mb-3 flex flex-wrap gap-1.5">
          {archived ? <Chip title={`Archived ${dateTime(task.archivedAt ?? "")}`}>archived</Chip> : null}
          {task.adopted ? <Chip title="An existing harness session tracked by Stage">tracked session</Chip> : null}
          <ReplacedChip task={task} />
        </div>
      ) : null}

      <div className="flex flex-col gap-3.5">
        {task.tree === null ? null : <TreeBar task={task} tree={task.tree} />}
        {waiting ? (
          <Alert tone="need" actions={action === null ? undefined : openInApp(answerable || replyLeads ? "secondary" : "primary", "max-md:hidden")}>
            {held !== null
              ? `${heldAlert(held, now)}${unlisted !== "" ? ` ${unlisted}` : ""}`
              : answerable
              ? `${kindName(task.harness)} is waiting for your answer.`
              : replyLeads
                ? replyPrompt(kindName(task.harness))
                : `${kindName(task.harness)} needs input${unlisted !== "" ? `. ${unlisted}` : action?.kind === "resume" ? ". Answer it in a terminal with the resume command." : ""}`}
          </Alert>
        ) : null}
        <RequestsPanel taskId={task.id} requests={requests} harness={kindName(task.harness)} needsInput={task.state === "needs_input"} stateRevision={task.stateRevision} />
        {replyLeads ? composer : null}
        {retired ? <Alert tone="plain">{retiredNotice(task, kindName(task.harness))}</Alert> : null}
        {block === null ? null : <LaunchBlockAlert block={block} tone="plain" />}
        {stopBlock === null || retired ? null : <LaunchBlockAlert block={stopBlock} tone="plain" />}

        <div className="flex flex-wrap items-center gap-2">
          {startable ? (
            <BlockedReason reason={lockedHint}>
              <Button
                variant="primary"
                busy={start.isPending}
                disabled={retired || block !== null}
                onClick={() => start.mutate()}
                {...(lockedHint !== undefined || task.startAt === null ? {} : { title: "Starts now instead of at the scheduled time" })}
              >
                {task.state !== "backlog" ? "Restart" : task.startAt !== null ? "Start now" : "Start"}
              </Button>
            </BlockedReason>
          ) : null}
          {phoneBar ? (
            <div
              ref={phoneBarRef}
              className="fixed inset-x-0 bottom-[calc(3.5rem+env(safe-area-inset-bottom))] z-30 flex max-h-[45dvh] flex-wrap gap-2 overflow-y-auto border-t border-line bg-bg px-3 py-2.5 md:contents"
            >
              {openInApp(waiting && !answerable && !replyLeads ? "primary" : "secondary", cx("max-md:flex-1", waiting && "md:hidden"))}
              {stoppable ? (
                <BlockedReason reason={retiredReason ?? stopBlock?.reason} className="max-md:flex-1">
                  <Button busy={stop.isPending} disabled={retired || stopBlock !== null} onClick={() => stop.mutate()} className="max-md:flex-1">
                    Stop
                  </Button>
                </BlockedReason>
              ) : null}
              <SessionToolbarActions key={`buttons:${task.launchAttempt}:${task.externalSessionId}`} actions={sessionActions.buttons} run={run} />
              <DeclaredActions key={`forms:${task.launchAttempt}:${task.externalSessionId}`} actions={sessionActions.forms} run={run} connectionId={task.connectionId} className="max-md:basis-full" />
            </div>
          ) : null}
          {task.adopted ? null : (
            <Button onClick={() => setForm("duplicate")}>
              Duplicate
            </Button>
          )}
          <Button onClick={() => setForm("edit")}>Edit</Button>
          {/* Archive (standalone tasks; a member closes with its tree), then Delete far right, only before it ever started. */}
          {archived ? (
            <Button className="ml-auto" busy={archive.isPending} onClick={() => archive.mutate(false)}>
              Unarchive
            </Button>
          ) : task.treeId === null ? (
            <Button
              className="ml-auto"
              disabled={isActive(task)}
              title={isActive(task) ? "Stop the task first" : "Hide it from the Dashboard and lists; activity on it unarchives it"}
              busy={archive.isPending}
              onClick={() => archive.mutate(true)}
            >
              Archive
            </Button>
          ) : null}
          {task.deletable ? (
            <Button
              variant="danger"
              className={cx(!archived && task.treeId !== null && "ml-auto")}
              disabled={isActive(task)}
              title={isActive(task) ? "Stop the task first" : undefined}
              onClick={() => setConfirmingDelete(true)}
            >
              Delete
            </Button>
          ) : null}
        </div>
        {confirmingDelete && task.deletable ? (
          <ConfirmInline danger confirmLabel="Delete" busy={remove.isPending} onConfirm={() => remove.mutate()} onCancel={() => setConfirmingDelete(false)}>
            Delete task &ldquo;{task.title}&rdquo;? It never started; this can&apos;t be undone.
          </ConfirmInline>
        ) : null}

        <StatePanel task={task} className="lg:hidden" />
        <ErrorText error={actionError} />
        <LaunchRecovery task={task} />
        {task.lastError === null || task.state === "launching" ? null : (
          <Alert tone="bad">
            <span className="font-medium">Last error:</span> {task.lastError}
          </Alert>
        )}


        <div className="grid grid-cols-1 gap-3.5 lg:grid-cols-[minmax(0,1fr)_340px]">
          <div className="flex min-w-0 flex-col gap-3.5">
            {task.adopted && task.externalSessionId !== null ? (
              <OpeningPrompt taskId={task.id} connectionId={task.connectionId} harness={kindName(task.harness)} live={isActive(task)} />
            ) : (
              <PromptPanel text={task.prompt}>
                {pendingHandoffNote(task) === null ? null : (
                  <p className="font-mono text-meta text-muted">{pendingHandoffNote(task)}</p>
                )}
                <ImageStrip attachments={task.attachments} label="Prompt images" />
                {task.currentHandoffs.map((h) => (
                  <HandoffRecord key={`${h.attempt}:${h.sourceTaskId ?? h.session?.externalSessionId}`} handoff={h} />
                ))}
                {task.currentSteering === null ? null : <SteeringBriefRecord steering={task.currentSteering} />}
              </PromptPanel>
            )}
            {task.acceptances.map((acceptance) => <AcceptedResult key={acceptance.id} acceptance={acceptance} current={acceptance.id === task.acceptanceId} harness={task.harness} />)}
            <LastWordPanel task={task} />
            {replyLeads ? null : composer}
            <JournalPanel owner={{ taskId: task.id }} projectId={task.projectId} />
            <TimelinePanel events={task.events} />
          </div>
          <div className="flex min-w-0 flex-col gap-3.5">
            <StatePanel task={task} className="max-lg:hidden" />
            {task.steering === null ? null : <SteeringPanel task={task} steering={task.steering} />}
            {task.tree !== null ? <TreePanel task={task} tree={task.tree} /> : task.steering?.active === true ? null : <NoTreePanel task={task} />}
            <SessionPanel task={task} />
            <UsagePanel task={task} />
            <ConfigPanel task={task} />
          </div>
        </div>
      </div>

      {form === "edit" ? <TaskFormModal task={task} onClose={() => setForm(null)} /> : null}
      {form === "duplicate" ? <TaskFormModal duplicate={task} onClose={() => setForm(null)} onSaved={(saved) => {
        if (saved.kind === "task") void navigate(`/tasks/${saved.task.id}`);
      }} /> : null}
    </div>
  );
}

/** The task's State picker: first in the side column on lg+, under the action row on phones (one shows per breakpoint). */
function StatePanel({ task, className }: { task: TaskDetail; className: string }) {
  return (
    <Panel title="State" className={className}>
      <StateControl task={task} tree={task.tree} />
    </Panel>
  );
}

function SessionPanel({ task }: { task: TaskDetail }) {
  const destination = useSessionDestination();
  const { kindName, descriptor } = useHarnesses();
  const name = kindName(task.harness);
  const title = `${name} session`;
  const scope = task.workspaceId ? { workspaceId: task.workspaceId } : undefined;
  const caps = useConnectionCapabilities(task.connectionId, true, scope);
  const meta = <LaunchMeta parts={launchParts(task.executionConfig, caps.data)} reported={task.reported} harness={name} />;
  const session = task.session;
  if (session === null) {
    const seen = task.state === "launching" ? (task.launch?.session ?? null) : null;
    return (
      <Panel title={title}>
        <div className="flex flex-col gap-3">
          {seen === null ? (
            <Muted>{task.state === "launching" ? "Waiting for confirmation of the existing launch." : task.acceptanceId === null ? "No session yet. Start the task to launch one." : "Completed without an execution reference."}</Muted>
          ) : (
            <div className="flex flex-col gap-1">
              <Muted>Started; Stage has not recorded it yet.</Muted>
              <p className="break-all font-mono text-meta">{seen.externalSessionId}</p>
            </div>
          )}
          {meta}
        </div>
      </Panel>
    );
  }
  return (
    <Panel title={title}>
      <Details>
        <Detail label="Session id">
          <span className="flex min-w-0 items-center gap-2">
            <span className="min-w-0 truncate" title={session.externalSessionId}>
              {session.externalSessionId}
            </span>
            <CopyButton text={session.externalSessionId} label="Copy" title={session.externalSessionId} />
          </span>
        </Detail>
      </Details>
      <div className="mt-3 flex flex-col gap-3 empty:hidden">
        {meta}
        <HarnessFacts facts={task.facts} />
      </div>
      {descriptor(task.harness)?.webAddress === true && destination(task) === null && !isRetired(task) && resumeCommand(task) === null ? (
        <TextLink to={SETTINGS_PATH.connections} className="mt-3 inline-block text-small text-fg">
          Configure browser address
        </TextLink>
      ) : null}
    </Panel>
  );
}

const WORKSPACE_SOURCE = { pinned: "pinned at launch", chosen: "selected", primary: "primary" };

function ConfigPanel({ task }: { task: TaskDetail }) {
  const { kindName, connection } = useHarnesses();
  const { machines } = useMachines();
  const workspace = task.workspace;
  const creation = task.events.find((event) => event.fromState === null);
  const location = workspace == null
    ? (task.bindingId == null ? null : `Binding #${task.bindingId}`)
    : taskWorkspaceLocation(workspace);
  return (
    <Panel title="Config">
      <Details>
        <Detail label="Harness">
          <HarnessConnectionState connectionId={task.connectionId}>
            {withMachine(kindName(task.harness), machineTag(machines, connection(task.connectionId)?.machineId ?? 0))}
            {task.connectionLabel === "" ? null : <span className="text-muted"> {task.connectionLabel}</span>}
            {isRetired(task) ? <span className="text-muted"> (replaced)</span> : null}
          </HarnessConnectionState>
        </Detail>
        {location === null ? null : (
          <Detail label="Workspace">
            <span className="wrap-anywhere">{location}</span>
            {workspace == null ? null : <span className="text-muted"> · {WORKSPACE_SOURCE[workspace.source]}</span>}
          </Detail>
        )}
        {task.startAt === null ? null : (
          <Detail label="Starts">
            <span title={dateTime(task.startAt)}>{wallTime(task.startAt, task.startTz ?? browserTimeZone(), { zone: true })}</span>
          </Detail>
        )}
        {task.needs.length === 0 ? null : (
          <Detail label="Needs">
            <span className="flex flex-wrap gap-x-2">
              {task.needs.map((n) => (
                <TextLink key={n} to={`/tasks/${n}`}>
                  #{n}
                </TextLink>
              ))}
            </span>
          </Detail>
        )}
        {task.originSession === null ? null : (
          <Detail label="Origin session">
            <span className="wrap-anywhere">{task.originSessionTitle ?? task.originSession.externalSessionId}</span>
          </Detail>
        )}
        {task.scheduleId === null ? null : (
          <Detail label="Schedule">
            <TextLink to={`/schedules/${task.scheduleId}`}>{task.scheduleTitle ?? `#${task.scheduleId}`}</TextLink>
          </Detail>
        )}
        <Detail label="Attempts">{task.launchAttempt}</Detail>
        <Detail label="Created">
          <RelTime at={task.createdAt} /> · <ActorLabel work={creation ?? {}} />
        </Detail>
        <Detail label="Launched">
          <RelTime at={task.launchedAt} />
        </Detail>
        <Detail label="Finished">
          <RelTime at={task.finishedAt} />
        </Detail>
        <Detail label="Completed">
          <RelTime at={task.completedAt} />
        </Detail>
      </Details>
    </Panel>
  );
}

function TimelinePanel({ events }: { events: TaskEvent[] }) {
  return (
    <Panel title="Timeline" meta={`${events.length} event${events.length === 1 ? "" : "s"}`} flush>
      {events.length === 0 ? (
        <EmptyState inset>No events yet. State changes appear here once the task moves.</EmptyState>
      ) : (
        <ol>
          {events.map((e) => (
            <Row key={e.id} className="grid grid-cols-[auto_1fr] items-center gap-x-3 sm:grid-cols-[88px_1fr]">
              <Meta className="tabular" title={dateTime(e.at)}>
                {stamp(e.at)}
              </Meta>
              <span className="flex min-w-0 flex-wrap items-center gap-1.5">
                {e.fromState === null || e.fromState === e.toState ? null : (
                  <Meta className="inline-flex items-center gap-1.5">
                    {taskStateLabel(e.fromState)}
                    <IconArrowRight size={12} label="to" />
                  </Meta>
                )}
                <TaskStateBadge state={e.toState} />
                {e.detail === "" ? null : (
                  <Meta className="min-w-0 break-words">{e.detail}</Meta>
                )}
                <Meta><ActorLabel work={e} /></Meta>
                <Meta className="empty:hidden"><ActorCause work={e} taskId={e.taskId} /></Meta>
              </span>
            </Row>
          ))}
        </ol>
      )}
    </Panel>
  );
}
