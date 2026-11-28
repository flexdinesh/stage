import { Fragment } from "react";
import type { Usage } from "../api/types.ts";
import { usageRows } from "../lib/usage.ts";
import { Detail, Details, Panel } from "./ui.tsx";

/** What this session consumed, when its harness reports it. */
export function UsagePanel({ usage, live }: { usage: Usage | null | undefined; live: boolean }) {
  if (usage == null) return null;
  return (
    <Panel title="Usage" meta={live ? "so far" : undefined}>
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
