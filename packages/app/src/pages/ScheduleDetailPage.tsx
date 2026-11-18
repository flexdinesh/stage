import { harnessName } from "../lib/tasks.ts";
import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { useApi } from "../platform/context.tsx";
import { useInvalidateWork, useSchedule, useSchedulePreview } from "../api/queries.ts";
import type { ScheduleDetail, ScheduleRun } from "../api/types.ts";
import { CopyButton } from "../components/CopyButton.tsx";
import { RefText } from "../components/StageLinks.tsx";
import { ImageStrip } from "../components/Images.tsx";
import { NextRun, ScheduleChip } from "../components/ScheduleList.tsx";
import { TaskStateBadge } from "../components/StateBadge.tsx";
import { TaskFormModal } from "../components/TaskForm.tsx";
import { Alert, Button, Chip, Detail, Details, ErrorText, Muted, PageHeader, Panel, QueryState, cx } from "../components/ui.tsx";
import { dateTime, executionModes, modelText, relativeTime, workspaceText } from "../lib/format.ts";
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

const linkClass = "text-fg underline-offset-2 hover:underline";

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
        meta={
          <>
            <Link to={`/projects/${s.projectId}`} className={linkClass}>
              {s.projectTitle}
            </Link>
            {` / schedule ${s.id} · ${cronSummary(s.cron)} · ${s.tz}`}
          </>
        }
      />
      <div className="-mt-1 mb-3 flex flex-wrap gap-1.5">
        <ScheduleChip paused={s.paused} />
        <Chip>{s.connectionId===1 ? modelText(s.model, s.modelOptions) : s.executionConfig?.model || "OpenCode default"}</Chip>
        {modes.permission === "" ? null : <Chip>{modes.permission}</Chip>}
        {modes.mode === "plan" ? <Chip>plan mode</Chip> : null}
        <Chip>{workspaceText(s.workspace)}</Chip>
        {s.tags.map((t) => (
          <Chip key={t}>#{t}</Chip>
        ))}
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
            <Link to={`/tasks/${run.data.id}`} className={linkClass}>
              task #{run.data.id}
            </Link>
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
              <span className="text-bad">unavailable</span>
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
          {s.lastRunAt === null ? "never" : <span title={dateTime(s.lastRunAt)}>{relativeTime(s.lastRunAt)}</span>}
        </Detail>
      </Details>
    </Panel>
  );
}

function ConfigPanel({ schedule: s }: { schedule: ScheduleDetail }) {
  return (
    <Panel title="Config">
      <Details>
        <Detail label="Project">
          <Link to={`/projects/${s.projectId}`} className={linkClass}>
            {s.projectTitle}
          </Link>
        </Detail>
        <Detail label="Harness">{harnessName(s.connectionId)}</Detail>
        {s.connectionId !== 1 ? <><Detail label="Model">{modelText(s.executionConfig?.model || "Harness default", s.executionConfig?.options ?? [])}</Detail><Detail label="Agent">{s.executionConfig?.mode || "Harness default"}</Detail></> : <><Detail label="Provider">{s.providerInstanceId ?? "T3 default"}</Detail>
        <Detail label="Model">{modelText(s.model, s.modelOptions)}</Detail>
        <Detail label="Runtime">{s.executionConfig ? s.executionConfig.permission || "Harness default" : s.runtimeMode}</Detail>
        <Detail label="Interaction">{s.executionConfig ? s.executionConfig.mode || "Harness default" : s.interactionMode}</Detail></>}
        <Detail label="Workspace">{workspaceText(s.workspace)}</Detail>
        <Detail label="Tags">{s.tags.length === 0 ? "-" : s.tags.map((t) => `#${t}`).join(" ")}</Detail>
        <Detail label="Created">
          <span title={dateTime(s.createdAt)}>{relativeTime(s.createdAt)}</span>
        </Detail>
      </Details>
    </Panel>
  );
}

function RunsPanel({ schedule: s }: { schedule: ScheduleDetail }) {
  return (
    <Panel title="Runs" meta={<span className="tabular">{s.runs.length}</span>} flush>
      {s.runs.length === 0 ? (
        <div className="p-3">
          <Muted>
            {s.paused
              ? "No runs yet. Resume the schedule or use Run now."
              : `No runs yet. The first one starts ${s.nextRunAt === null ? "on the next fire" : wallTime(s.nextRunAt, s.tz, { zone: true })}.`}
          </Muted>
        </div>
      ) : (
        <ul>
          {s.runs.map((r) => (
            <li key={r.id} className="border-b border-line last:border-b-0">
              <RunRow run={r} tz={s.tz} />
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

function OutcomeChip({ run }: { run: ScheduleRun }) {
  if (run.task !== null && (run.outcome === "started" || run.outcome === "manual")) return <TaskStateBadge state={run.task.state} />;
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
    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-2.5 gap-y-1 border-l-2 border-l-transparent px-3 py-2.5 md:grid-cols-[118px_minmax(0,1fr)] md:gap-x-3.5 md:py-2">
      <div className="order-2 justify-self-end md:order-1 md:justify-self-start">
        <OutcomeChip run={r} />
      </div>
      <div className="order-1 min-w-0 md:order-2">
        {r.taskId === null ? (
          <span className={cx("text-body", r.outcome === "missed" ? "text-fg" : "text-muted")}>{OUTCOME_TEXT[r.outcome]}</span>
        ) : (
          <>
            <span className="mr-1.5 font-mono text-meta whitespace-nowrap text-muted tabular">#{r.taskId}</span>
            <Link to={`/tasks/${r.taskId}`} className="text-body font-medium break-words hover:underline">
              {r.task?.title ?? `Task #${r.taskId}`}
            </Link>
          </>
        )}
        <div className="mt-0.5 flex flex-wrap gap-x-2.5 gap-y-0.5 font-mono text-meta text-muted">
          <span title={dateTime(r.fireAt)} className="tabular">
            {r.outcome === "manual" ? "ran" : "fire"} {wallTime(r.fireAt, tz)}
          </span>
          <span title={dateTime(r.at)} className="tabular">
            {relativeTime(r.at)}
          </span>
          {r.outcome === "manual" && r.taskId !== null ? <span>run now</span> : null}
          {r.detail === "" ? null : <span>{r.detail}</span>}
          {r.task?.lastError == null ? null : (
            <span className="truncate text-bad" title={r.task.lastError}>
              {r.task.lastError}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}
