// Settings > Connections > Manage: what the connection can do, as its harness declares it (its capabilities'
// operations, GET /api/connections/{id}/capabilities). One ruled line per operation in Stage's words
// (lib/capabilities.ts OPERATION_LABELS), with the harness's reason when it is unavailable. Never by harness.
import { useScopeMachineId } from "../api/harnessScope.tsx";
import { useConnectionCapabilities, useConnections } from "../api/queries.ts";
import type { Capabilities, HarnessDescriptor } from "../api/types.ts";
import { capabilityLines } from "../lib/capabilities.ts";
import { activeOn } from "../lib/harnesses.ts";
import { EmptyState, Meta, Panel, QueryState, Row, RowList, cx } from "./ui.tsx";

/** The Capabilities panel for the scope machine's active connection of the harness; nothing until it is set up. */
export function ConnectionCapabilities({ descriptor }: { descriptor: HarnessDescriptor }) {
  const connections = useConnections();
  const connection = activeOn(connections.data?.connections ?? [], descriptor.kind, useScopeMachineId());
  if (connection === undefined || !connection.configured) return null;
  return <CapabilitiesPanel connectionId={connection.id} name={descriptor.name} />;
}

function CapabilitiesPanel({ connectionId, name }: { connectionId: number; name: string }) {
  const caps = useConnectionCapabilities(connectionId, true);
  return (
    <Panel title="Capabilities" meta={`declared by ${name}`} flush>
      <QueryState isPending={caps.isPending} error={caps.error} hasData={caps.data !== undefined}>
        {caps.data === undefined ? null : <CapabilityList operations={caps.data.operations} name={name} />}
      </QueryState>
    </Panel>
  );
}

/** The ruled lines: label, then a mono available / unavailable and the harness's reason under an unavailable one. */
export function CapabilityList({ operations, name }: { operations: Capabilities["operations"]; name: string }) {
  const lines = capabilityLines(operations);
  if (lines.length === 0) return <EmptyState inset>{name} declares no operations.</EmptyState>;
  return (
    <RowList label={`${name} capabilities`}>
      {lines.map((l) => (
        <Row key={l.op} className="grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3 gap-y-0.5">
          <span className={cx("text-ui", l.available ? "text-fg" : "text-muted")}>{l.label}</span>
          <Meta>{l.available ? "available" : "unavailable"}</Meta>
          {l.reason === null ? null : (
            <Meta small className="col-span-2 break-words">
              {l.reason}
            </Meta>
          )}
        </Row>
      ))}
    </RowList>
  );
}
