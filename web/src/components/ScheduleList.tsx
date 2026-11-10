import { useMutation } from "@tanstack/react-query";
import { Link } from "react-router";
import { api } from "../api/client.ts";
import { useInvalidateWork } from "../api/queries.ts";
import type { Schedule } from "../api/types.ts";
import { dateTime, relativeTime } from "../lib/format.ts";
import { cronSummary, wallTime } from "../lib/schedules.ts";
import { IconRepeat } from "./icons.tsx";
import { TaskStateBadge } from "./StateBadge.tsx";
import { Button, Chip, ErrorText, cx } from "./ui.tsx";

/** "paused" (idle) or a plain "repeat" chip: a schedule has no state of its own, its runs do. */
export function ScheduleChip({ paused }: { paused: boolean }) {
  return paused ? (
    <Chip tone="idle" dot>
      paused
    </Chip>
  ) : (
    <Chip tone="plain">
      <IconRepeat />
      repeat
    </Chip>
  );
}

/** "next in 3h" with the exact wall time in the schedule's zone on hover; "paused" while paused. */
export function NextRun({ schedule }: { schedule: Schedule }) {
  if (schedule.paused || schedule.nextRunAt === null) return <span>paused</span>;
  return (
    <span title={`Next run ${wallTime(schedule.nextRunAt, schedule.tz, { zone: true })} (${dateTime(schedule.nextRunAt)} local)`} className="tabular">
      next {relativeTime(schedule.nextRunAt)}
    </span>
  );
}

/** Hairline-separated schedule rows (for `Panel flush`). */
export function ScheduleRows({
  schedules,
  emptyText = "No schedules.",
  showProject = false,
  actions = "full",
}: {
  schedules: Schedule[];
  emptyText?: string;
  showProject?: boolean;
  actions?: "full" | "run";
}) {
  if (schedules.length === 0) return <p className="px-3 py-2.5 text-small text-muted">{emptyText}</p>;
  return (
    <ul>
      {schedules.map((s) => (
        <li key={s.id} className="border-b border-line last:border-b-0">
          <ScheduleRow schedule={s} showProject={showProject} actions={actions} />
        </li>
      ))}
    </ul>
  );
}

/** Same grid as TaskRow: chip column, optional project column, title + mono meta, last-run chip, actions. */
export function ScheduleRow({
  schedule: s,
  showProject = false,
  actions = "full",
}: {
  schedule: Schedule;
  showProject?: boolean;
  actions?: "full" | "run";
}) {
  const invalidate = useInvalidateWork();
  const run = useMutation({ mutationFn: () => api.runSchedule(s.id), onSettled: invalidate });
  const pause = useMutation({ mutationFn: () => api.updateSchedule(s.id, { paused: !s.paused }), onSettled: invalidate });
  const underTitle = showProject ? "md:col-span-3 md:col-start-3" : "md:col-span-3 md:col-start-2";
  const error = run.error ?? pause.error;

  return (
    <div
      className={cx(
        "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-2.5 gap-y-1.5 border-l-2 border-l-transparent px-3 py-2.5 md:gap-x-3.5 md:py-2",
        showProject ? "md:grid-cols-[118px_150px_minmax(0,1fr)_auto_auto]" : "md:grid-cols-[118px_minmax(0,1fr)_auto_auto]",
      )}
    >
      <div className="order-2 justify-self-end md:order-1 md:justify-self-start">
        <ScheduleChip paused={s.paused} />
      </div>

      {showProject ? (
        <div className="hidden min-w-0 md:order-2 md:block">
          <Link
            to={`/projects/${s.projectId}`}
            title={s.projectTitle}
            className="block truncate font-mono text-detail font-medium text-fg hover:underline"
          >
            {s.projectTitle}
          </Link>
        </div>
      ) : null}

      <div className="order-1 min-w-0 md:order-2">
        {showProject ? (
          <Link
            to={`/projects/${s.projectId}`}
            className="mb-0.5 block font-mono text-detail font-medium break-words text-fg hover:underline md:hidden"
          >
            {s.projectTitle}
          </Link>
        ) : null}
        <Link to={`/schedules/${s.id}`} className="text-body font-medium break-words hover:underline">
          {s.title}
        </Link>
        <div className="mt-0.5 flex flex-wrap gap-x-2.5 gap-y-0.5 font-mono text-meta text-muted">
          {showProject ? null : (
            <Link to={`/projects/${s.projectId}`} className="hover:text-fg hover:underline">
              {s.projectTitle}
            </Link>
          )}
          <span>{cronSummary(s.cron)}</span>
          <span>{s.tz}</span>
          <NextRun schedule={s} />
          {s.lastTaskId === null ? null : (
            <Link to={`/tasks/${s.lastTaskId}`} className="tabular hover:text-fg hover:underline md:hidden">
              last #{s.lastTaskId}
              {s.lastTaskState === null ? null : ` ${s.lastTaskState.replace("_", " ")}`}
            </Link>
          )}
        </div>
      </div>

      <div className="hidden md:order-3 md:block">
        {s.lastTaskId === null || s.lastTaskState === null ? null : (
          <Link
            to={`/tasks/${s.lastTaskId}`}
            title={`Last run: task #${s.lastTaskId}`}
            className="inline-flex items-center gap-1.5 font-mono text-meta text-muted tabular hover:text-fg"
          >
            #{s.lastTaskId}
            <TaskStateBadge state={s.lastTaskState} />
          </Link>
        )}
      </div>

      <div className="order-4 col-span-2 flex flex-wrap items-center gap-1.5 md:col-span-1 md:justify-end">
        <Button small busy={run.isPending} onClick={() => run.mutate()} title="Start a run now; the regular schedule is unchanged">
          Run now
        </Button>
        {actions === "full" ? (
          <Button small variant="ghost" busy={pause.isPending} onClick={() => pause.mutate()}>
            {s.paused ? "Resume" : "Pause"}
          </Button>
        ) : null}
      </div>

      {error === null ? null : (
        <div className={cx("order-6 col-span-2", underTitle)}>
          <ErrorText error={error} />
        </div>
      )}
    </div>
  );
}
