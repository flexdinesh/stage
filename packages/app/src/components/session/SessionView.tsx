import { useEffect, useRef } from "react";
import { useLocation, useOutletContext, useParams } from "react-router";
import { ApiError } from "../../api/client.ts";
import {
  useConnectionCapabilities, useHarnesses, useSessionRequests, useTaskActions, useTaskActivity, useTaskSession,
} from "../../api/queries.ts";
import type { TaskDetail } from "../../api/types.ts";
import { harnessSessionSlots } from "../../harnesses/index.ts";
import { launchParts } from "../../lib/capabilities.ts";
import { splitToolbarActions } from "../../lib/connectionSetup.ts";
import { isRetired, retiredHint } from "../../lib/harnesses.ts";
import { useSessionAction } from "../../lib/hostState.ts";
import { liveActivity } from "../../lib/sessionActivity.ts";
import { composerDelivery, sessionMessage } from "../../lib/sessionComposer.ts";
import { SETTINGS_PATH } from "../../lib/settings.ts";
import { parseAttempt, REQUESTS_ANCHOR, requestsPath } from "../../lib/taskSession.ts";
import { canStop } from "../../lib/tasks.ts";
import { usePlatform } from "../../platform/context.tsx";
import { DeclaredActions } from "../DeclaredActions.tsx";
import { HarnessFacts } from "../HarnessPresentation.tsx";
import { IconArrowRight } from "../icons.tsx";
import { LastWordPanel } from "../LastWord.tsx";
import { BlockedReason, LaunchBlockAlert, useStopTask } from "../LaunchGate.tsx";
import { LaunchMeta } from "../LaunchMeta.tsx";
import { LaunchRecovery } from "../LaunchRecovery.tsx";
import { useMarkNotificationGroupsRead } from "../Notifications.tsx";
import { RequestsPanel } from "../Requests.tsx";
import { SessionActionButton } from "../SessionAction.tsx";
import { SessionToolbarActions } from "../SessionToolbarActions.tsx";
import { UsagePanel } from "../Usage.tsx";
import { Alert, Button, EmptyState, ErrorText, Panel, QueryState, TextLink, linkClass } from "../ui.tsx";
import { ActivitySection } from "./ActivitySection.tsx";
import { PastSessionBanner } from "./PastSessionBanner.tsx";
import { usePhoneActionBar } from "./PhoneActionBar.tsx";
import { SessionComposer } from "./SessionComposer.tsx";
import { useSessionDraft } from "./SessionDraft.tsx";
import { SESSION_ACTIONS_ANCHOR, SessionHeader } from "./SessionHeader.tsx";

export function SessionRoute() {
  const task = useOutletContext<TaskDetail>();
  const { notifications } = usePlatform();
  const markViewed = useMarkNotificationGroupsRead();
  useEffect(() => {
    void notifications?.closeShown(`task-${task.id}`);
    markViewed([`task:${task.id}`]);
  }, [notifications, markViewed, task.id]);
  const attempt = parseAttempt(useParams().attempt);
  if (attempt === null) return <Alert tone="bad">That session attempt is invalid.</Alert>;
  return <SessionView task={task} attempt={attempt} />;
}

export function SessionView({ task, attempt }: { task: TaskDetail; attempt?: number | undefined }) {
  const harnesses = useHarnesses();
  const resolveAction = useSessionAction();
  const draft = useSessionDraft();
  const activeSession = task.sessions.find((session) => session.current);
  const current = attempt === undefined || activeSession?.attempt === attempt;
  const summary = current ? activeSession : task.sessions.find((session) => session.attempt === attempt);
  const past = useTaskSession(task.id, current ? undefined : attempt);
  const snapshot = current ? undefined : past.data;
  const recovering = current && task.state === "launching";
  const seen = recovering ? task.launch?.session : undefined;
  const ref = current ? task.session ?? seen : snapshot?.session ?? summary?.session;
  const connectionId = ref?.connectionId ?? task.connectionId;
  const kind = current ? task.harness : harnesses.connection(connectionId)?.harness ?? task.harness;
  const harness = harnesses.kindName(kind);
  const connectionActive = current ? !isRetired(task) : !harnesses.retired(connectionId);
  const recorded = current ? task.session !== null : snapshot !== undefined;
  const state = current ? summary?.state || task.state : snapshot?.state || summary?.state;
  const links = current ? task.links ?? seen?.links : snapshot?.links ?? summary?.links;
  const action = resolveAction({ ...(links === undefined ? {} : { links }), connectionActive });
  const destination = action?.kind === "open" ? action.href : action?.kind === "resume" ? `#${SESSION_ACTIONS_ANCHOR}` : null;
  const waiting = current && task.state === "needs_input";
  const caps = useConnectionCapabilities(connectionId, recorded && connectionActive);
  const operations = caps.data?.operations;
  const scope = task.workspaceId ? { workspaceId: task.workspaceId } : undefined;
  const launchCaps = useConnectionCapabilities(task.connectionId, current, scope);
  const requests = useSessionRequests({ taskId: waiting && recorded && connectionActive ? task.id : null, connectionId, needsInput: waiting });
  const active = liveActivity(current, state ?? "");
  const activityOffered = recorded && connectionActive && operations?.activity?.available === true;
  const activity = useTaskActivity(task.id, current ? undefined : attempt, active, activityOffered, current ? summary?.attempt : undefined);
  const slots = harnessSessionSlots(kind);
  const slotProps = summary === undefined ? undefined : { task, session: summary, activity: activityOffered ? activity.data : undefined, current };
  const { HeaderExtra, Sections } = slots ?? {};
  const result = draft.result?.attempt === task.launchAttempt ? draft.result : null;
  const unlisted = harnesses.descriptor(kind)?.unlistedRequests;
  const delivery = composerDelivery(state ?? "", operations, {
    ready: requests.ready,
    count: requests.requests.length,
    ...(unlisted ? { unlisted } : {}),
  }, harness);
  const reasons = !connectionActive ? [retiredHint(harness)]
    : caps.isError && operations === undefined ? ["Message delivery is unavailable."] : delivery.reasons;

  const stoppable = current && canStop({ ...task, connectionActive: true });
  const { stop, block: stopBlock } = useStopTask(task, stoppable);
  const stopReason = !connectionActive ? retiredHint(harness)
    : stopBlock?.reason ?? (operations?.stop?.available === true ? undefined : operations?.stop?.reason || "Stop is unavailable.");
  const { actions, run } = useTaskActions(task, current && recorded && connectionActive);
  const sessionActions = splitToolbarActions(actions);
  const { barProps, spacer } = usePhoneActionBar();
  const hasPrimaryActions = action !== null || stoppable || waiting;
  const hasActions = hasPrimaryActions || sessionActions.buttons.length > 0 || sessionActions.forms.length > 0;
  const requestsAnchor = useRef<HTMLDivElement>(null);
  const location = useLocation();
  useEffect(() => {
    if (!waiting || location.hash !== `#${REQUESTS_ANCHOR}`) return;
    requestsAnchor.current?.scrollIntoView({ block: "start" });
    requestsAnchor.current?.focus({ preventScroll: true });
  }, [waiting, location]);

  const header = (
    <SessionHeader
      task={task}
      harness={harness}
      attempt={summary?.attempt ?? attempt}
      state={recorded || recovering ? state : undefined}
      reason={snapshot?.stateReason ?? summary?.stateReason}
      sessionId={ref?.externalSessionId}
      extra={HeaderExtra && slotProps ? <HeaderExtra {...slotProps} /> : null}
      actions={hasActions ? (
        <div {...barProps}>
          {hasPrimaryActions ? (
            <div className="flex basis-full flex-wrap items-center gap-2 md:contents">
              {waiting ? (
                <TextLink to={requestsPath(task.id)} className="inline-flex min-h-control items-center gap-1 text-small md:hidden">
                  Answer <IconArrowRight size={12} />
                </TextLink>
              ) : null}
              <SessionActionButton action={action} name={harness} connectionId={connectionId} variant={recovering ? "primary" : "secondary"} className="max-md:flex-1" />
              {stoppable ? (
                <BlockedReason reason={stopReason}>
                  <Button busy={stop.isPending} disabled={stopReason !== undefined} onClick={() => stop.mutate()}>Stop</Button>
                </BlockedReason>
              ) : null}
            </div>
          ) : null}
          <SessionToolbarActions key={`buttons:${task.launchAttempt}:${task.externalSessionId}`} actions={sessionActions.buttons} run={run} />
          <DeclaredActions key={`forms:${task.launchAttempt}:${task.externalSessionId}`} actions={sessionActions.forms} run={run} connectionId={connectionId} className="max-md:basis-full" />
        </div>
      ) : null}
    />
  );
  const reported = current ? task.reported : snapshot?.reported;
  const facts = current ? task.facts : snapshot?.facts;
  const parts = current ? launchParts(task.executionConfig, launchCaps.data) : [];
  const configure = current && recorded && action === null && connectionActive && harnesses.descriptor(kind)?.webAddress === true;
  const metadata = (current && !recorded) || parts.length > 0 || reported !== undefined || (facts?.length ?? 0) > 0 || configure;
  const activityError = activity.error;
  const offline = activity.fetchStatus === "paused" || (activityError instanceof ApiError && activityError.status === 503);

  return (
    <section aria-label={`${harness} session`} className="flex min-w-0 flex-col gap-3.5">
      {header}
      {current || attempt === undefined ? null : <PastSessionBanner taskId={task.id} attempt={attempt} />}
      <QueryState isPending={!current && past.isPending} error={current ? null : past.error} hasData={current || snapshot !== undefined}>
        {recovering ? <LaunchRecovery task={task} /> : null}
        {current && stopBlock !== null && connectionActive ? <LaunchBlockAlert block={stopBlock} tone="plain" /> : null}
        <ErrorText error={current ? stop.error : null} />
        {current && !recovering && task.lastError !== null ? (
          <Alert tone="bad"><span className="font-medium">Last error:</span> {task.lastError}</Alert>
        ) : null}
        <div className="grid min-w-0 gap-3.5 lg:grid-cols-[minmax(0,1fr)_340px]">
          <div className="flex min-w-0 flex-col gap-3.5">
            {!recorded && !recovering ? <Panel><EmptyState>No session yet. Start the task to launch one.</EmptyState></Panel> : null}
            {current && recorded ? (
              <div
                id={waiting ? REQUESTS_ANCHOR : undefined}
                ref={requestsAnchor}
                tabIndex={waiting ? -1 : undefined}
                className="flex scroll-mt-[calc(4.25rem+var(--inset-top))] flex-col gap-3.5 focus:outline-none lg:scroll-mt-[calc(1rem+var(--edge-band))]"
              >
                {waiting ? (
                  <>
                    <RequestsPanel key={task.launchAttempt} taskId={task.id} requests={requests.requests} harness={harness} stateRevision={task.stateRevision} />
                    <ErrorText error={requests.error} />
                  </>
                ) : null}
                <SessionComposer
                  harness={harness}
                  buttons={connectionActive && requests.error === null ? delivery.buttons : []}
                  reasons={requests.error === null ? reasons : ["Pending requests could not be checked."]}
                  draft={draft.text}
                  onDraft={draft.setText}
                  onSend={(mode) => draft.send(task.launchAttempt, sessionMessage(draft.text, mode, task.state, task.stateRevision))}
                  pending={draft.pending}
                  result={result}
                  destination={action === null ? null : (
                    <a href={`#${SESSION_ACTIONS_ANCHOR}`} className={`${linkClass("rest")} inline-flex items-center gap-1`}>
                      Session actions <IconArrowRight size={12} />
                    </a>
                  )}
                />
              </div>
            ) : null}
            <LastWordPanel reply={current ? task.finalReply : snapshot?.reply} harness={harness} href={destination} />
            {activityOffered ? <ActivitySection activity={activity.data} loading={activity.isPending} offline={offline} error={activityError} harness={harness} href={destination} showModel={!slots?.activityModelInHeader} /> : null}
            {Sections && slotProps ? <Sections {...slotProps} /> : null}
          </div>
          <div className="flex min-w-0 flex-col gap-3.5">
            <UsagePanel usage={current ? task.usage : snapshot?.usage} live={active} />
            {metadata ? (
              <Panel title={recorded ? "Session details" : "Launch details"}>
                <div className="flex flex-col gap-3">
                  <LaunchMeta parts={parts} reported={reported} harness={harness} />
                  {!recorded && parts.length === 0 && reported === undefined ? <EmptyState>Harness defaults</EmptyState> : null}
                  <HarnessFacts facts={facts} />
                  {configure ? <TextLink to={SETTINGS_PATH.connections} className="text-small">Configure browser address <IconArrowRight size={12} /></TextLink> : null}
                </div>
              </Panel>
            ) : null}
          </div>
        </div>
      </QueryState>
      {spacer}
    </section>
  );
}
