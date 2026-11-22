// Settings > General > Info: the running Stage (GET /api/health) in every host. The desktop app adds how its server
// runs, its socket, its own version, Open Stage at login and Copy diagnostics (components/Host.tsx).
import { useQuery } from "@tanstack/react-query";
import { keys } from "../api/queries.ts";
import { dateTime } from "../lib/format.ts";
import { runningAs, versionLine } from "../lib/settings.ts";
import { useApi } from "../platform/context.tsx";
import { DesktopControls, useDesktopInfo } from "./Host.tsx";
import { CopyButton, Detail, Details, Panel, QueryState } from "./ui.tsx";

export function InfoPanel() {
  const api = useApi();
  const health = useQuery({ queryKey: keys.health, queryFn: api.health, refetchInterval: false });
  const desktop = useDesktopInfo();
  const h = health.data;
  return (
    <Panel title="Info" {...(desktop?.diagnostics == null ? {} : { actions: <CopyButton text={desktop.diagnostics} label="Copy diagnostics" /> })}>
      <div className="flex flex-col gap-3">
        <QueryState isPending={health.isPending} error={health.error} hasData={h !== undefined}>
          {h === undefined ? null : (
            <Details>
              <Detail label="Version">{versionLine(h)}</Detail>
              <Detail label="Running as">{runningAs(desktop?.runs ?? null)}</Detail>
              <Detail label="Started">{dateTime(h.startedAt)}</Detail>
              <Detail label="Identity">{h.identity}</Detail>
              {desktop === null ? null : <Detail label={desktop.where.label}>{desktop.where.value}</Detail>}
              {desktop === null ? null : <Detail label="Desktop app">{desktop.app}</Detail>}
            </Details>
          )}
        </QueryState>
        {desktop === null ? null : (
          <div className="flex flex-col gap-3 border-t border-line pt-3">
            <DesktopControls />
          </div>
        )}
      </div>
    </Panel>
  );
}
