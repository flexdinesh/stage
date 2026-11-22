// A remote machine's page (/settings/machines/:id): its harness connection
// panels, driven on the machine through its worker. Credentials stay on the
// machine. Offline or on another API version: why, and the connections Stage
// has on record, read-only. The local machine's setup stays in Settings.
import { useMemo } from "react";
import { Navigate } from "react-router";
import { HarnessScopeProvider, type HarnessScope } from "../api/harnessScope.tsx";
import { useHarnesses, useMachines } from "../api/queries.ts";
import type { Machine } from "../api/types.ts";
import { HarnessConnections } from "../components/HarnessConnections.tsx";
import { Alert, EmptyState, Hint, Marker, Meta, PageHeader, Panel, QueryState, Row, RowList } from "../components/ui.tsx";
import { dateTime } from "../lib/format.ts";
import { machineSetupAlert, machineSetupState, machineStatusLine, machineTone, serverVersion, versionDiffers } from "../lib/machines.ts";
import { useIdParam } from "../lib/params.ts";
import { NotFoundPage } from "./NotFoundPage.tsx";

export function MachinePage() {
  const id = useIdParam();
  const machines = useMachines();
  if (id === null) return <NotFoundPage />;
  const m = machines.machines.find((x) => x.id === id);
  return (
    <QueryState isPending={machines.isPending} error={machines.error}>
      {machines.data === undefined ? null : m === undefined ? (
        <NotFoundPage />
      ) : m.kind === "local" ? (
        <Navigate to="/settings" replace />
      ) : (
        <MachineView machine={m} server={serverVersion(machines.machines)} />
      )}
    </QueryState>
  );
}

function MachineView({ machine: m, server }: { machine: Machine; server: string | null }) {
  const scope = useMemo<HarnessScope>(() => ({ machineId: m.id, label: m.label }), [m.id, m.label]);
  const alert = machineSetupAlert(m, server);
  return (
    <div>
      <PageHeader
        title={m.label}
        trail={[{ label: "Settings", to: "/settings" }, { label: `machine ${m.id}` }]}
        meta={
          <>
            <span className="inline-flex items-center gap-1.5 align-middle">
              <Marker tone={machineTone(m)} />
              <span className="tabular" title={m.lastSeenAt === null ? undefined : `Last seen ${dateTime(m.lastSeenAt)}`}>
                {machineStatusLine(m)}
              </span>
            </span>
            {versionDiffers(m, server) ? ` · server ${server}` : null}
          </>
        }
      />
      {alert === null ? (
        <div className="flex flex-col gap-3.5">
          <HarnessScopeProvider scope={scope}>
            <div className="grid grid-cols-1 items-start gap-3.5 lg:grid-cols-2">
              <HarnessConnections />
            </div>
          </HarnessScopeProvider>
          <Footnote label={m.label} />
        </div>
      ) : (
        <div className="flex max-w-[720px] flex-col gap-3.5">
          <Alert tone={machineSetupState(m) === "needs_update" ? "need" : "plain"}>{alert}</Alert>
          <RecordedConnections machine={m} />
          <Footnote label={m.label} />
        </div>
      )}
    </div>
  );
}

function Footnote({ label }: { label: string }) {
  return (
    <Hint className="max-w-[72ch]">
      Harness credentials stay on {label}, in its worker&apos;s data dir. This server keeps each connection&apos;s identity, status and settings.
    </Hint>
  );
}

/** Offline: the machine's connections as Stage last recorded them. */
function RecordedConnections({ machine: m }: { machine: Machine }) {
  const { kindName } = useHarnesses();
  return (
    <Panel title="Connections on record" meta={`${m.connections.length}`} flush>
      {m.connections.length === 0 ? (
        <EmptyState inset>No harness connections yet. Connect one here once {m.label} is back online.</EmptyState>
      ) : (
        <RowList>
          {m.connections.map((c) => (
            <Row key={c.id} className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
              <span className="text-body font-medium text-fg">{kindName(c.harness)}</span>
              <Meta className="min-w-0 break-all">{c.label === "" ? `connection #${c.id}` : c.label}</Meta>
            </Row>
          ))}
        </RowList>
      )}
    </Panel>
  );
}
