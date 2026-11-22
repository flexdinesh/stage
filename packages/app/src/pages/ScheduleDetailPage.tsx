import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { useApi } from "../platform/context.tsx";
import { useHarnesses, useInvalidateWork, useSchedule, useSchedulePreview } from "../api/queries.ts";
import type { ScheduleDetail, ScheduleRun } from "../api/types.ts";
import { RefText } from "../components/StageLinks.tsx";
import { ImageStrip } from "../components/Images.tsx";
import { NextRun, ScheduleChip } from "../components/ScheduleList.tsx";
import { NodeChip } from "../components/trees/TreeNode.tsx";
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
  IdTag,
  InlineError,
  PageHeader,
  Panel,
  QueryState,
  RelTime,
  Row,
  RowList,
  TextLink,
  cx,
  needEdgeClass,
} from "../components/ui.tsx";
import { configModelText, dateTime, executionModes, workspaceText } from "../lib/format.ts";
import { useIdParam } from "../lib/params.ts";
import { cronSummary, wallTime } from "../lib/schedules.ts";
import { NotFoundPage } from "./NotFoundPage.tsx";

export function ScheduleDetailPage() {
  const id = useIdParam();
  return id === null ? <NotFoundPage /> : <ScheduleDetailView id={id} />;
}

function ScheduleDetailView({ id }: { id: number }) {
  const schedule = useSchedule(id);
  return (
    <QueryState isPending={schedule.isPending} error={schedule.error}>
      {schedule.data === undefined ? null : <ScheduleBody schedule={schedule.data} />}
    </QueryState>
  );
}

function ScheduleBody({ schedule: s }: { schedule: ScheduleDetail }) {
  const api = useApi();
  const navigate = useNavigate();
  const invalidate = useInvalidateWork();
  const [editing, setEditing] = useState(false);
  const [armed, setArmed] = useState(false);
  const run = useMutation({ mutationFn: () => api.runSchedule(s.id), onSettled: invalidate });
  const pause = useMutation({ mutationFn: () => api.updateSchedule(s.id, { paused: !s.paused }), onSettled: invalidate });
  const remove = useMutation({
    mutationFn: () => api.deleteSchedule(s.id),
    onSuccess: async () => {
      await navigate("/schedules");
      await invalidate();
    },
  });
  const actionError = run.error ?? pause.error ?? remove.error;

  const modes = executionModes(s);
  return (
    <div>
      <PageHeader
        title={s.title}
        trail={[{ label: s.projectTitle, to: `/projects/${s.projectId}` }, { label: `schedule ${s.id}` }]}
        meta={`${cronSummary(s.cron)} · ${s.tz}`}
      />
      <div className="-mt-1 mb-3 flex flex-wrap gap-1.5">
        <ScheduleChip paused={s.paused} />
        <Chip>{configModelText(s.executionConfig)}</Chip>
        {modes.permission === "" ? null : <Chip>{modes.permission}</Chip>}
        {modes.mode === "plan" ? <Chip>plan mode</Chip> : null}
        <Chip>{workspaceText(s.workspace)}</Chip>
      </div>

      <div className="flex flex-col gap-3.5">
        {s.paused ? (
          <Alert tone="plain">Paused. No runs start until you resume; times missed while paused are skipped.</Alert>
        ) : null}

        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="primary"
            busy={run.isPending}
            onClick={() => run.mutate()}
            title="Start a run now; the regular schedule is unchanged"
          >
            Run now
          </Button>
          <Button busy={pause.isPending} onClick={() => pause.mutate()}>
            {s.paused ? "Resume" : "Pause"}
          </Button>
          <Button onClick={() => setEditing(true)}>Edit</Button>
          <Button
            variant="danger"
            className="ml-auto"
            busy={remove.isPending}
            onClick={() => (armed ? remove.mutate() : setArmed(true))}
            onBlur={() => setArmed(false)}
          >
            {armed ? "Confirm delete · started tasks are kept" : "Delete"}
          </Button>
        </div>

        <ErrorText error={actionError} />
        {run.data === undefined ? null : (
          <Alert tone="plain">
            Started{" "}
            <TextLink to={`/tasks/${run.data.id}`}>task #{run.data.id}</TextLink>
            .
          </Alert>
        )}

        <div className="grid grid-cols-1 gap-3.5 lg:grid-cols-[minmax(0,1fr)_340px]">
          <div className="flex min-w-0 flex-col gap-3.5">
            <RunsPanel schedule={s} />
          </div>
          <div className="flex min-w-0 flex-col gap-3.5">
            <WhenPanel schedule={s} />
            <ConfigPanel schedule={s} />
            <Panel title="Prompt" actions={<CopyButton text={s.prompt} label="Copy" />}>
              <div className="flex flex-col gap-3">
                <pre className="max-h-80 overflow-auto font-mono text-small leading-relaxed whitespace-pre-wrap"><RefText text={s.prompt} /></pre>
                <ImageStrip attachments={s.attachments} label="Prompt images" />
              </div>
            </Panel>
            {s.notes === "" ? null : (
              <Panel title="Notes">
                <p className="text-small whitespace-pre-wrap">{s.notes}</p>
              </Panel>
            )}
          </div>
        </div>
      </div>

      {editing ? <TaskFormModal schedule={s} onClose={() => setEditing(false)} /> : null}
    </div>
  );
}

function WhenPanel({ schedule: s }: { schedule: ScheduleDetail }) {
  const preview = useSchedulePreview(s.paused ? null : { cron: s.cron, tz: s.tz });
  const next = preview.data?.next ?? [];
  return (
    <Panel title="When" meta={<NextRun schedule={s} />}>
      <Details>
        <Detail label="Repeat">{cronSummary(s.cron)}</Detail>
        <Detail label="Cron">{s.cron}</Detail>
        <Detail label="Zone">{s.tz}</Detail>
        <Detail label="Next">
          {s.paused ? (
            "paused"
          ) : next.length === 0 ? (
            preview.isError ? (
              <InlineError>unavailable</InlineError>
            ) : (
              "-"
            )
          ) : (
            <ol className="flex flex-col tabular">
              {next.map((iso) => (
                <li key={iso} title={dateTime(iso)}>
                  {wallTime(iso, s.tz, { zone: true })}
                </li>
              ))}
            </ol>
          )}
        </Detail>
        <Detail label="Last run">
          {s.lastRunAt === null ? "never" : <RelTime at={s.lastRunAt} />}
        </Detail>
      </Details>
    </Panel>
  );
}

function ConfigPanel({ schedule: s }: { schedule: ScheduleDetail }) {
  const harnesses = useHarnesses();
  return (
    <Panel title="Config">
      <Details>
        <Detail label="Project">
          <TextLink to={`/projects/${s.projectId}`}>{s.projectTitle}</TextLink>
        </Detail>
        <Detail label="Harness">{harnesses.name(s.connectionId)}{harnesses.retired(s.connectionId) ? <span className="text-muted"> (replaced)</span> : null}</Detail>
        <Detail label="Model">{configModelText(s.executionConfig)}</Detail>
        <Detail label="Permission">{executionModes(s).permission || "Harness default"}</Detail>
        <Detail label="Mode">{executionModes(s).mode || "Harness default"}</Detail>
        <Detail label="Workspace">{workspaceText(s.workspace)}</Detail>
        <Detail label="Created">
          <RelTime at={s.createdAt} />
        </Detail>
      </Details>
    </Panel>
  );
}

function RunsPanel({ schedule: s }: { schedule: ScheduleDetail }) {
  return (
    <Panel title="Runs" meta={<span className="tabular">{s.runs.length}</span>} flush>
      {s.runs.length === 0 ? (
        <EmptyState inset>
          {s.paused
            ? "No runs yet. Resume the schedule or use Run now."
            : `No runs yet. The first one starts ${s.nextRunAt === null ? "on the next fire" : wallTime(s.nextRunAt, s.tz, { zone: true })}.`}
        </EmptyState>
      ) : (
        <RowList>
          {s.runs.map((r) => (
            <Row key={r.id} bare>
              <RunRow run={r} tz={s.tz} />
            </Row>
          ))}
        </RowList>
      )}
    </Panel>
  );
}

function OutcomeChip({ run }: { run: ScheduleRun }) {
  if (run.task !== null && (run.outcome === "started" || run.outcome === "manual")) return <NodeChip task={run.task} />;
  switch (run.outcome) {
    case "skipped":
      return (
        <Chip tone="idle" dot>
          skipped
        </Chip>
      );
    case "missed":
      return (
        <Chip tone="bad" dot>
          missed
        </Chip>
      );
    case "manual":
      return <Chip>run now</Chip>;
    case "started":
      return <Chip>started</Chip>;
  }
}

const OUTCOME_TEXT: Record<ScheduleRun["outcome"], string> = {
  started: "Started on schedule",
  manual: "Run now",
  skipped: "Skipped: previous run still active",
  missed: "Missed: server was down",
};

function RunRow({ run: r, tz }: { run: ScheduleRun; tz: string }) {
  return (
    <div className={cx("grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-2.5 gap-y-1 px-3 py-row md:grid-cols-[118px_minmax(0,1fr)] md:gap-x-3.5", needEdgeClass(false))}>
      <div className="order-2 justify-self-end md:order-1 md:justify-self-start">
        <OutcomeChip run={r} />
      </div>
      <div className="order-1 min-w-0 md:order-2">
        {r.taskId === null ? (
          <span className={cx("text-body", r.outcome === "missed" ? "text-fg" : "text-muted")}>{OUTCOME_TEXT[r.outcome]}</span>
        ) : (
          <>
            <IdTag id={r.taskId} className="mr-1.5" />
            <TextLink to={`/tasks/${r.taskId}`} className="text-body font-medium break-words">
              {r.task?.title ?? `Task #${r.taskId}`}
            </TextLink>
          </>
        )}
        <div className="mt-0.5 flex flex-wrap gap-x-2.5 gap-y-0.5 font-mono text-meta text-muted">
          <span title={dateTime(r.fireAt)} className="tabular">
            {r.outcome === "manual" ? "ran" : "fire"} {wallTime(r.fireAt, tz)}
          </span>
          <RelTime at={r.at} className="tabular" />
          {r.outcome === "manual" && r.taskId !== null ? <span>run now</span> : null}
          {r.detail === "" ? null : <span>{r.detail}</span>}
          {r.task?.lastError == null ? null : (
            <InlineError truncate className="min-w-0">
              {r.task.lastError}
            </InlineError>
          )}
        </div>
      </div>
    </div>
  );
}
