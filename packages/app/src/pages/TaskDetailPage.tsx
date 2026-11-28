import { useMutation } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Outlet, useMatch, useNavigate, useOutletContext } from "react-router";
import { useApi, usePlatform } from "../platform/context.tsx";
import { useHarnesses, useInvalidateWork, useMachines, useTask } from "../api/queries.ts";
import type { TaskDetail, TaskEvent } from "../api/types.ts";
import { CopyLinkPair } from "../components/StageLinks.tsx";
import { HandoffRecord, SteeringBriefRecord } from "../components/Handoff.tsx";
import { IconArrowRight } from "../components/icons.tsx";
import { JournalPanel } from "../components/Journal.tsx";
import { AcceptedResult } from "../components/Completion.tsx";
import { ActorCause, ActorLabel } from "../components/ActorLabel.tsx";
import { BlockedReason, LaunchBlockAlert, useStartTask } from "../components/LaunchGate.tsx";
import { taskWorkspaceLocation } from "../lib/taskForm.ts";
import { StateControl } from "../components/StateControl.tsx";
import { OpeningPrompt, PromptPanel } from "../components/PromptPanel.tsx";
import { TaskStateBadge, taskStateLabel } from "../components/StateBadge.tsx";
import { ImageStrip } from "../components/Images.tsx";
import { NoTreePanel, TreeBar, TreePanel } from "../components/trees/TreePanel.tsx";
import { SteeringPanel } from "../components/SteeringPanel.tsx";
import { SessionDraftProvider } from "../components/session/SessionDraft.tsx";
import { usePhoneActionBar } from "../components/session/PhoneActionBar.tsx";
import { heldAlert } from "../lib/steering.ts";
import { useNow } from "../lib/now.ts";
import { TaskFormModal } from "../components/TaskForm.tsx";
import {
  Alert, Button, Chip, ConfirmInline, Detail, Details, EmptyState, ErrorText, Marker, Meta, PageHeader,
  Panel, QueryState, RelTime, Row, Skeleton, SkeletonBar, TabNav, TextLink, cx,
} from "../components/ui.tsx";
import { dateTime } from "../lib/format.ts";
import { pendingHandoffNote } from "../lib/handoff.ts";
import { useIdParam } from "../lib/params.ts";
import { browserTimeZone, wallTime } from "../lib/schedules.ts";
import { useMarkNotificationGroupsRead } from "../components/Notifications.tsx";
import { canStart, isActive } from "../lib/tasks.ts";
import { parseAttempt, requestsPath, taskPath, taskSessionPath } from "../lib/taskSession.ts";
import { treeTaskPath } from "../lib/trees.ts";
import { isRetired, retiredHint, retiredNotice } from "../lib/harnesses.ts";
import { ReplacedChip } from "../components/HarnessName.tsx";
import { HarnessConnectionState } from "../components/ConnectionState.tsx";
import { machineTag, withMachine } from "../lib/machines.ts";
import { NotFoundPage } from "./NotFoundPage.tsx";

export function TaskDetailPage() {
  const id = useIdParam();
  return id === null ? <NotFoundPage /> : <TaskDetailView key={id} id={id} />;
}

function TaskDetailView({ id }: { id: number }) {
  const task = useTask(id);
  const sessionTab = useMatch("/tasks/:id/session/*") !== null;
  const attempt = parseAttempt(useMatch("/tasks/:id/session/:attempt")?.params.attempt);
  const { notifications } = usePlatform();
  const markViewed = useMarkNotificationGroupsRead();
  useEffect(() => {
    if (sessionTab) return;
    void notifications?.closeShown(`task-${id}`);
    markViewed([`task:${id}`]);
  }, [notifications, id, markViewed, sessionTab]);

  return (
    <QueryState isPending={task.isPending} error={task.error} hasData={task.data !== undefined} skeleton={<TaskDetailSkeleton session={sessionTab} />}>
      {task.data === undefined ? null : <TaskDetailLayout task={task.data} sessionTab={sessionTab} attempt={attempt} />}
    </QueryState>
  );
}

function AnswerLink({ taskId, className }: { taskId: number; className?: string }) {
  return (
    <TextLink to={requestsPath(taskId)} className={cx("inline-flex min-h-control items-center gap-1 text-small font-medium", className)}>
      Answer <IconArrowRight size={12} />
    </TextLink>
  );
}

function TaskDetailLayout({ task, sessionTab, attempt }: { task: TaskDetail; sessionTab: boolean; attempt: number | undefined | null }) {
  const { kindName } = useHarnesses();
  const harness = kindName(task.harness);
  const archived = task.archivedAt !== null;
  const retired = isRetired(task);
  const waiting = task.state === "needs_input";
  const held = waiting ? task.hold : null;
  const now = useNow(15_000, held !== null);
  const currentSession = sessionTab && (attempt === undefined || task.sessions.some((session) => session.current && session.attempt === attempt));

  return (
    <SessionDraftProvider taskId={task.id}>
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
      {archived || task.adopted || retired ? (
        <div className="-mt-1 mb-3 flex flex-wrap gap-1.5">
          {archived ? <Chip title={`Archived ${dateTime(task.archivedAt ?? "")}`}>archived</Chip> : null}
          {task.adopted ? <Chip title="An existing harness session tracked by Stage">tracked session</Chip> : null}
          <ReplacedChip task={task} />
        </div>
      ) : null}
      <div className="flex flex-col gap-3.5">
        {waiting ? (
          <Alert tone="need" actions={currentSession ? undefined : <AnswerLink taskId={task.id} />}>
            {held === null ? `${harness} needs input.` : heldAlert(held, now)}
          </Alert>
        ) : null}
        <TabNav
          label="Task detail"
          className="md:self-start"
          items={[
            { to: taskPath(task.id), label: "Task", end: true },
            {
              to: taskSessionPath(task.id),
              label: `${harness} session`,
              marker: waiting ? <><Marker tone="need" /><span className="sr-only">Needs input</span></> : undefined,
            },
          ]}
        />
        {retired ? <Alert tone="plain">{retiredNotice(task, harness)}</Alert> : null}
        <Outlet context={task} />
      </div>
    </SessionDraftProvider>
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

/** Keep the shared heading and tab bar in place while the selected view loads. */
function TaskDetailSkeleton({ session }: { session: boolean }) {
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
        <div className="flex gap-0.5">
          <SkeletonBar className="h-control w-24 max-md:flex-1" />
          <SkeletonBar className="h-control w-44 max-md:flex-1" />
        </div>
        {session ? (
          <>
            <Panel title={<SkeletonBar onPanel className="h-2.5 w-36" />} actions={<SkeletonBar onPanel className="h-control-sm w-24" />}>
              <LineBars widths={["w-1/3", "w-24"]} />
            </Panel>
            <div className="grid gap-3.5 lg:grid-cols-[minmax(0,1fr)_340px]">
              <Panel title="Message"><SkeletonBar className="h-20 w-full" /></Panel>
              <Panel title="Session details"><LineBars widths={["w-3/4", "w-1/2"]} /></Panel>
            </div>
          </>
        ) : (
          <>
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
          </>
        )}
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

export function TaskRoute() {
  const task = useOutletContext<TaskDetail>();
  const api = useApi();
  const { kindName } = useHarnesses();
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
  const retired = isRetired(task);
  const startable = canStart({ ...task, connectionActive: true });
  const block = startable && !retired ? launchBlock : null;
  const lockedHint = retired ? retiredHint(kindName(task.harness)) : block?.reason;
  const actionError = start.error ?? remove.error ?? archive.error;
  const { barProps, spacer } = usePhoneActionBar();

  return (
    <section aria-label="Task">
      <div className="flex flex-col gap-3.5">
        {task.tree === null ? null : <TreeBar task={task} tree={task.tree} />}
        {block === null ? null : <LaunchBlockAlert block={block} tone="plain" />}

        <div className="flex flex-wrap items-center gap-2">
          {startable || waiting ? (
            <div {...barProps}>
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
              {waiting ? <AnswerLink taskId={task.id} className="md:hidden" /> : null}
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
            <JournalPanel owner={{ taskId: task.id }} projectId={task.projectId} />
            <TimelinePanel events={task.events} />
          </div>
          <div className="flex min-w-0 flex-col gap-3.5">
            <StatePanel task={task} className="max-lg:hidden" />
            {task.steering === null ? null : <SteeringPanel task={task} steering={task.steering} />}
            {task.tree !== null ? <TreePanel task={task} tree={task.tree} /> : task.steering?.active === true ? null : <NoTreePanel task={task} />}
            <ConfigPanel task={task} />
          </div>
        </div>
      </div>

      {form === "edit" ? <TaskFormModal task={task} onClose={() => setForm(null)} /> : null}
      {form === "duplicate" ? <TaskFormModal duplicate={task} onClose={() => setForm(null)} onSaved={(saved) => {
        if (saved.kind === "task") void navigate(`/tasks/${saved.task.id}`);
      }} /> : null}
      {spacer}
    </section>
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
