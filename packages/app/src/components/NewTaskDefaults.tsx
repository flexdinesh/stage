// Settings > Connections > Manage: a connection's "New tasks" panel. Set as default makes this connection the one new
// tasks start on (one across all machines); the model pin is what new tasks here start with (model and options only:
// mode and permission start at the harness default). Harness-specific new-task settings belong here too.
// API: PUT /api/preferences/default-connection, PUT /api/connections/{id}/new-task-config.
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useScopeMachineId } from "../api/harnessScope.tsx";
import { useConnections, useHarnesses, useMachines, usePreferences, useSetDefaultConnection, useSetNewTaskConfig } from "../api/queries.ts";
import type { ExecutionConfig, Harness, HarnessConnection } from "../api/types.ts";
import { activeOn } from "../lib/harnesses.ts";
import { useApi } from "../platform/context.tsx";
import { HarnessFields, harnessDefaults } from "./HarnessFields.tsx";
import { Button, Chip, ErrorText, Hint, Muted, Panel, SubHeading } from "./ui.tsx";

/** The New tasks panel for the scope machine's active connection of `harness`; nothing until it is set up. */
export function NewTaskDefaults({ harness }: { harness: Harness }) {
  const connections = useConnections();
  const connection = activeOn(connections.data?.connections ?? [], harness, useScopeMachineId());
  if (connection === undefined || !connection.configured) return null;
  return (
    <Panel title="New tasks">
      <div className="flex flex-col gap-3">
        <DefaultConnection connection={connection} />
        {/* Re-read the pin after a save or a change elsewhere. */}
        <ModelPin key={JSON.stringify(connection.newTaskConfig)} connection={connection} />
      </div>
    </Panel>
  );
}

function DefaultConnection({ connection }: { connection: HarnessConnection }) {
  const prefs = usePreferences();
  const harnesses = useHarnesses();
  const { machines } = useMachines();
  const save = useSetDefaultConnection();
  const current = prefs.data?.defaultConnectionId ?? null;
  const other = current === null || current === connection.id ? undefined : harnesses.connection(current);
  const otherName = other === undefined ? null : `${harnesses.kindName(other.harness)} on ${machines.find((m) => m.id === other.machineId)?.label ?? "another machine"}`;
  if (prefs.data === undefined) return <ErrorText error={prefs.error} />;
  return (
    <div className="flex flex-col gap-1.5" aria-busy={save.isPending}>
      {current === connection.id ? (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <Chip>default</Chip>
          <Hint className="min-w-0 flex-1 basis-56">New tasks open here. A project not on this connection opens on its first available harness.</Hint>
          <Button small busy={save.isPending} onClick={() => save.mutate(null)}>
            Clear default
          </Button>
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <Button busy={save.isPending} onClick={() => save.mutate(connection.id)}>
            Set as default
          </Button>
          <Hint className="min-w-0 flex-1 basis-56">
            {otherName === null ? "No default: a new task opens on its project's only harness." : `Default now: ${otherName}.`}
          </Hint>
        </div>
      )}
      <ErrorText error={save.error} />
    </div>
  );
}

const pinnable = (c: ExecutionConfig) => c.model !== "" || c.options.length > 0;
const sameConfig = (a: ExecutionConfig, b: ExecutionConfig) => JSON.stringify(a) === JSON.stringify(b);

function ModelPin({ connection }: { connection: HarnessConnection }) {
  const api = useApi();
  const save = useSetNewTaskConfig();
  const pinned = connection.newTaskConfig;
  const start: ExecutionConfig = pinned === null ? harnessDefaults : { ...harnessDefaults, model: pinned.model, options: pinned.options ?? [] };
  const [config, setConfig] = useState<ExecutionConfig>(start);
  // What the connection offers for any project (no project: its harness-wide models and options).
  const caps = useQuery({
    queryKey: ["capabilities", connection.id, "", false],
    queryFn: () => api.capabilities(connection.id),
    staleTime: 60_000,
    retry: false,
  });
  const pin = (next: ExecutionConfig | null) => save.mutate({ id: connection.id, config: next });
  const busy = save.isPending;
  return (
    <section className="flex flex-col gap-3 border-t border-line pt-3" aria-label="Model" aria-busy={busy}>
      <SubHeading title="Model" meta={pinned === null ? "harness default" : "pinned"} />
      {pinned === null ? <Muted>Not pinned. New tasks here start at the harness default. Mode and permission always do.</Muted> : null}
      <HarnessFields modelOnly config={config} caps={caps.data} onChange={setConfig} disabled={busy || caps.data === undefined} />
      <ErrorText error={caps.error} />
      <div className="flex flex-wrap gap-2">
        {pinned === null ? (
          <Button variant="primary" busy={busy} disabled={!pinnable(config)} onClick={() => pin(config)}>
            Pin
          </Button>
        ) : (
          <>
            <Button variant="primary" busy={busy && save.variables?.config !== null} disabled={busy || !pinnable(config) || sameConfig(config, start)} onClick={() => pin(config)}>
              Save pinned
            </Button>
            <Button busy={busy && save.variables?.config === null} disabled={busy} onClick={() => pin(null)}>
              Unpin
            </Button>
          </>
        )}
      </div>
      <ErrorText error={save.error} />
    </section>
  );
}
