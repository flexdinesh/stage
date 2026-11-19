import { useSchedules } from "../api/queries.ts";
import { IconPlus } from "../components/icons.tsx";
import { useNewTask } from "../components/NewTask.tsx";
import { useScope } from "../components/Scope.tsx";
import { ScheduleRows } from "../components/ScheduleList.tsx";
import { Button, PageHeader, Panel, QueryState } from "../components/ui.tsx";
import { relativeTime } from "../lib/format.ts";

export function SchedulesPage() {
  const scope = useScope();
  const projectId = scope.projectId ?? undefined;
  const schedules = useSchedules(scope.query);
  const newTask = useNewTask();
  const list = schedules.data?.schedules ?? [];
  const active = list.filter((s) => !s.paused);
  // Ordered by nextRunAt; the first unpaused one fires next.
  const next = active[0]?.nextRunAt ?? null;

  const meta =
    schedules.data === undefined
      ? undefined
      : [
          `${list.length} schedule${list.length === 1 ? "" : "s"}`,
          list.length === active.length ? null : `${list.length - active.length} paused`,
          next === null ? null : `next run ${relativeTime(next)}`,
        ]
          .filter((s) => s !== null)
          .join(" · ");

  return (
    <>
      <PageHeader
        title={scope.project === null ? "Schedules" : <><span className="text-muted">{scope.project.title} / </span>Schedules</>}
        {...(meta === undefined ? {} : { meta })}
        actions={
          <>
            <Button variant="primary" onClick={() => newTask(projectId, "repeat")}>
              <IconPlus /> New schedule
            </Button>
          </>
        }
      />
      <Panel flush title="Schedules" {...(schedules.data === undefined ? {} : { meta: <span className="tabular">{list.length}</span> })}>
        <QueryState isPending={schedules.isPending} error={schedules.error}>
          <ScheduleRows
            schedules={list}
            emptyText={
              scope.project === null
                ? "No schedules yet. A schedule starts a fresh task from the same prompt on a cron, e.g. every weekday at 09:00. Create one with New schedule."
                : "No schedules in this project."
            }
            showProject={scope.project === null}
          />
        </QueryState>
      </Panel>
    </>
  );
}
