import { Fragment } from "react";
import type { TaskDetail } from "../api/types.ts";
import { isActive } from "../lib/tasks.ts";
import { usageRows } from "../lib/usage.ts";
import { Detail, Details, Panel } from "./ui.tsx";

/** What the task's current session consumed, when its harness reports it; hidden otherwise. */
export function UsagePanel({ task }: { task: TaskDetail }) {
  const usage = task.usage;
  if (usage === undefined) return null;
  return (
    <Panel title="Usage" meta={isActive(task) ? "so far" : undefined}>
      <Details>
        {usageRows(usage).map((row) => (
          <Detail key={row.label} label={row.label}>
            <span className="tabular" title={row.exact}>
              {row.value}
            </span>
            {row.detail === undefined ? null : (
              // Each part stays whole, so a narrow column breaks at the separator.
              <span className="block text-meta text-muted">
                {row.detail.map((part, i) => (
                  <Fragment key={part}>
                    {i > 0 ? " · " : null}
                    <span className="whitespace-nowrap">{part}</span>
                  </Fragment>
                ))}
              </span>
            )}
          </Detail>
        ))}
      </Details>
    </Panel>
  );
}
