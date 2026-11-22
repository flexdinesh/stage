import { useMutation } from "@tanstack/react-query";
import { Link } from "react-router";
import { useApi } from "../platform/context.tsx";
import { useInvalidateWork } from "../api/queries.ts";
import type { Schedule } from "../api/types.ts";
import { dateTime, relativeTime } from "../lib/format.ts";
import { cronSummary, lastRun, wallTime } from "../lib/schedules.ts";
import { IconRepeat } from "./icons.tsx";
import { TaskStateBadge, taskStateTone } from "./StateBadge.tsx";
import { Button, Chip, ErrorText, Skeleton, SkeletonBar, cx, toneClass } from "./ui.tsx";

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

function scheduleRowGrid(showProject: boolean): string {
  return cx(
    "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-2.5 gap-y-1.5 border-l-2 border-l-transparent px-3 py-row md:gap-x-3.5",
    showProject ? "md:grid-cols-[118px_150px_minmax(0,1fr)_auto_auto]" : "md:grid-cols-[118px_minmax(0,1fr)_auto_auto]",
  );
}

const SCHEDULE_TITLE_WIDTHS = ["w-2/5", "w-1/2", "w-1/3"];

/** Loading schedule rows in the schedule grid: chip, project, title over meta, Run now and Pause. */
export function ScheduleRowsSkeleton({ count = 3, showProject = false }: { count?: number; showProject?: boolean }) {
  return (
    <Skeleton label="Loading schedules">
      <ul>
        {Array.from({ length: count }, (_, i) => (
          <li key={i} className="border-b border-line last:border-b-0">
            <div className={scheduleRowGrid(showProject)}>
              <div className="order-2 justify-self-end md:order-1 md:justify-self-start">
                <SkeletonBar className="h-[22px] w-20" />
              </div>
              {showProject ? (
                <div className="hidden md:order-2 md:block">
                  <SkeletonBar className="h-3.5 w-20" />
                </div>
              ) : null}
              <div className="order-1 flex min-w-0 flex-col gap-1.5 py-0.5 md:order-2">
                {showProject ? <SkeletonBar className="h-3.5 w-20 md:hidden" /> : null}
                <SkeletonBar className={cx("h-4", SCHEDULE_TITLE_WIDTHS[i % SCHEDULE_TITLE_WIDTHS.length])} />
                <SkeletonBar className="h-3 w-72 max-w-full" />
              </div>
              <div className="order-4 col-span-2 flex gap-1.5 md:col-span-2 md:justify-end">
                <SkeletonBar className="h-control-sm w-20" />
                <SkeletonBar className="h-control-sm w-14" />
              </div>
            </div>
          </li>
        ))}
      </ul>
    </Skeleton>
  );
}

/** Same grid as TaskRow: chip column, optional project column, title + mono meta, last-run chip (failed or blocking runs only), actions. */
export function ScheduleRow({
  schedule: s,
  showProject = false,
  actions = "full",
}: {
  schedule: Schedule;
  showProject?: boolean;
  actions?: "full" | "run";
}) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const run = useMutation({ mutationFn: () => api.runSchedule(s.id), onSettled: invalidate });
  const pause = useMutation({ mutationFn: () => api.updateSchedule(s.id, { paused: !s.paused }), onSettled: invalidate });
  const underTitle = showProject ? "md:col-span-3 md:col-start-3" : "md:col-span-3 md:col-start-2";
  const error = run.error ?? pause.error;
  // Healthy last runs read as plain meta; failed or fire-blocking ones get the chip (lastRun).
  const view = s.lastTaskId === null || s.lastTaskState === null ? null : lastRun(s.lastTaskId, s.lastTaskState);
  const flag = view?.kind === "flag" && s.lastTaskState !== null ? { state: s.lastTaskState, skip: view.skip } : null;
  const quietWord = view?.kind === "quiet" ? view.word : null;

  return (
    <div
      className={scheduleRowGrid(showProject)}
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
          {flag !== null && flag.skip !== null && !s.paused ? (
            <span className={cx(toneClass(taskStateTone(flag.state)), "text-[var(--cf)]")}>{flag.skip}</span>
          ) : null}
          {s.lastTaskId === null ? null : flag === null ? (
            <Link to={`/tasks/${s.lastTaskId}`} title={`Last run: task #${s.lastTaskId}`} className="tabular hover:text-fg hover:underline">
              last #{s.lastTaskId}
              {quietWord === null ? null : ` ${quietWord}`}
            </Link>
          ) : (
            <Link to={`/tasks/${s.lastTaskId}`} className="tabular hover:text-fg hover:underline md:hidden">
              last #{s.lastTaskId}{" "}
              <span className={cx(toneClass(taskStateTone(flag.state)), "text-[var(--cf)]")}>{flag.state.replace("_", " ")}</span>
            </Link>
          )}
        </div>
      </div>

      <div className="hidden md:order-3 md:block">
        {s.lastTaskId === null || flag === null ? null : (
          <Link
            to={`/tasks/${s.lastTaskId}`}
            title={`Last run: task #${s.lastTaskId}`}
            className="inline-flex items-center gap-1.5 font-mono text-meta text-muted tabular hover:text-fg"
          >
            last #{s.lastTaskId}
            <TaskStateBadge state={flag.state} />
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
