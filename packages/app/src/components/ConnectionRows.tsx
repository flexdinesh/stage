// Settings > Connections: a machine's harness connections as one-line rows (name, where, state, Manage,
// Disconnect), its Add connection menu, and the Manage drawer holding the harness's setup panel. Rows never expand;
// Disconnect confirms inline under its row. On phone the whole row opens Manage (Disconnect lives in the drawer).
// A machine whose setup cannot run (offline, another API version) lists its connections on record, read-only.
// Logic: lib/connectionRows.ts.
import { useMemo, useState, type ReactNode } from "react";
import { HarnessScopeProvider, LOCAL_SCOPE, type HarnessScope } from "../api/harnessScope.tsx";
import { useHarnesses, useOpenCodeStatus, useScopedHarnessDescriptors, useT3Status } from "../api/queries.ts";
import type { HarnessDescriptor, Machine } from "../api/types.ts";
import { addConnectionOptions, addConnectionReason, connectShortcuts, connectionRows, machineLive, type ConnectionRowModel } from "../lib/connectionRows.ts";
import { openCodeRowDetail, openCodeSummary, statusRead, t3RowDetail, t3Summary } from "../lib/connections.ts";
import { shortDate } from "../lib/format.ts";
import { disconnectAvailability, t3DisconnectPrompt } from "../lib/onboarding.ts";
import { ConnectionSetup, useDisconnectHarness, useHarnessStatus } from "./HarnessConnections.tsx";
import { IconStep } from "./icons.tsx";
import { OPENCODE_DISCONNECT_PROMPT, useDisconnectOpenCode } from "./OpenCodeSetup.tsx";
import { useDisconnectT3 } from "./T3Setup.tsx";
import { Button, Chip, ConfirmInline, Drawer, Hint, Menu, MenuItem, Row, RowButton, RowList, useMenu, type ChipTone } from "./ui.tsx";

/** The harness scope of a machine: the local routes for the main machine, its worker's for a remote one. */
export function MachineScope({ machine, children }: { machine: Pick<Machine, "id" | "kind" | "label">; children: ReactNode }) {
  const scope = useMemo<HarnessScope>(() => (machine.kind === "local" ? LOCAL_SCOPE : { machineId: machine.id, label: machine.label }), [machine.id, machine.kind, machine.label]);
  return <HarnessScopeProvider scope={scope}>{children}</HarnessScopeProvider>;
}

/** A machine's rows and Add connection options; call inside its MachineScope. */
export function useMachineConnections(machine: Machine) {
  const harnesses = useHarnesses();
  const live = machineLive(machine);
  const descriptors = useScopedHarnessDescriptors();
  const rows = connectionRows(machine, harnesses.connection);
  const kinds = live ? (descriptors.data?.harnesses ?? null) : [];
  const options = kinds === null ? null : addConnectionOptions(kinds, rows);
  const descriptor = (kind: string): HarnessDescriptor | undefined => kinds?.find((d) => d.kind === kind);
  return { live, rows, options, addReason: addConnectionReason(machine, options), descriptor };
}

/** The Manage (or Add) drawer's subject: a harness on the machine. */
export type ManageTarget = { descriptor: HarnessDescriptor };

/** Add connection: a menu of the machine's connectable harnesses; connected ones are listed disabled. */
export function AddConnection({ machine, connections, onPick }: { machine: Machine; connections: ReturnType<typeof useMachineConnections>; onPick: (t: ManageTarget) => void }) {
  const menu = useMenu();
  const reason = connections.addReason;
  return (
    <>
      <Button small disabled={reason !== null} title={reason ?? undefined} aria-label={`Add connection on ${machine.label}`} {...menu.triggerProps}>
        Add connection
      </Button>
      <Menu menu={menu} label={`Add connection on ${machine.label}`} align="right" className="w-64">
        {(connections.options ?? []).map((o) => (
          <MenuItem
            key={o.kind}
            reason={o.reason}
            onSelect={() => {
              const descriptor = connections.descriptor(o.kind);
              if (descriptor !== undefined) onPick({ descriptor });
            }}
          >
            {o.name}
          </MenuItem>
        ))}
      </Menu>
    </>
  );
}

/** The machine's connection rows; a live machine without any offers a Connect button per harness it can add. */
export function ConnectionRowList({ machine, connections, onManage }: { machine: Machine; connections: ReturnType<typeof useMachineConnections>; onManage: (t: ManageTarget) => void }) {
  if (connections.rows.length === 0) {
    if (!connections.live) return <Hint className="border-t border-line py-2 pr-3 pl-[30px]">No harness connections.</Hint>;
    return (
      <div className="flex min-h-11 flex-wrap items-center gap-x-3 gap-y-2 border-t border-line py-2 pr-3 pl-[30px] md:min-h-control">
        <Hint>No connections. Connect a harness to run tasks here.</Hint>
        {connectShortcuts(connections.options).map((o) => (
          <Button
            key={o.kind}
            small
            className="max-md:h-11"
            aria-label={`${o.label} on ${machine.label}`}
            onClick={() => {
              const descriptor = connections.descriptor(o.kind);
              if (descriptor !== undefined) onManage({ descriptor });
            }}
          >
            {o.label}
          </Button>
        ))}
      </div>
    );
  }
  return (
    <RowList label={`${machine.label} connections`} className="border-t border-line">
      {connections.rows.map((r) => {
        const descriptor = connections.descriptor(r.harness);
        if (!connections.live || descriptor === undefined) return <RecordedRow key={r.id} machine={machine} row={r} />;
        const manage = () => onManage({ descriptor });
        switch (r.harness) {
          case "t3":
            return <T3Row key={r.id} name={descriptor.name} machine={machine} onManage={manage} />;
          case "opencode":
            return <OpenCodeRow key={r.id} name={descriptor.name} machine={machine} onManage={manage} />;
          default:
            return <GenericRow key={r.id} descriptor={descriptor} machine={machine} onManage={manage} />;
        }
      })}
    </RowList>
  );
}

/** The Manage drawer: the harness's own setup panel(s) on the machine. */
export function ManageDrawer({ machine, target, onClose }: { machine: Machine; target: ManageTarget; onClose: () => void }) {
  return (
    <Drawer title={`${target.descriptor.name} on ${machine.label}`} onClose={onClose}>
      <MachineScope machine={machine}>
        <div className="flex flex-col gap-3.5">
          <ConnectionSetup descriptor={target.descriptor} />
        </div>
      </MachineScope>
    </Drawer>
  );
}

// ---- Rows ----

interface Disconnect {
  prompt: string;
  busy: boolean;
  error: unknown;
  /** Disconnects; `done` closes the confirm once it succeeded. */
  run: (done: () => void) => void;
  reset: () => void;
}

/** One connection: name and where, state chip, Manage and Disconnect (laptop) or a whole-row button (phone). */
function ConnectionRow({
  name,
  machine,
  detail,
  chip,
  onManage,
  disconnect,
}: {
  name: string;
  machine: Machine;
  detail: string;
  chip: { tone: ChipTone; label: string };
  onManage?: () => void;
  /** null: the harness cannot disconnect from here. */
  disconnect: Disconnect | null;
}) {
  const [confirm, setConfirm] = useState(false);
  const state = (
    <Chip tone={chip.tone} dot className="shrink-0">
      {chip.label}
    </Chip>
  );
  const title = (
    <span className="flex min-w-0 flex-1 items-baseline gap-x-2">
      <span className="shrink-0 text-body font-medium text-fg">{name}</span>
      <span className="min-w-0 truncate font-mono text-meta text-muted" title={detail}>
        {detail}
      </span>
    </span>
  );
  return (
    <Row bare>
      {onManage === undefined ? (
        <div className="flex min-h-11 items-center gap-3 py-1.5 pr-3 pl-[30px] md:min-h-control">
          {title}
          {state}
          <Hint className="max-md:hidden">editable when {machine.label} is back</Hint>
        </div>
      ) : (
        <>
          <RowButton className="gap-3 pl-[30px] md:hidden" onClick={onManage} aria-label={`Manage ${name} on ${machine.label}`}>
            {title}
            {state}
            <span className="text-muted">
              <IconStep />
            </span>
          </RowButton>
          <div className="flex items-center gap-3 py-1.5 pr-3 pl-[30px] max-md:hidden">
            {title}
            {state}
            <div className="flex shrink-0 items-center gap-1.5">
              <Button small onClick={onManage} aria-label={`Manage ${name} on ${machine.label}`}>
                Manage
              </Button>
              {disconnect === null ? null : (
                <Button
                  small
                  variant="ghost"
                  disabled={confirm}
                  aria-label={`Disconnect ${name} on ${machine.label}`}
                  onClick={() => {
                    disconnect.reset();
                    setConfirm(true);
                  }}
                >
                  Disconnect
                </Button>
              )}
            </div>
          </div>
        </>
      )}
      {confirm && disconnect !== null ? (
        <div className="pr-3 pb-2.5 pl-[30px] max-md:hidden">
          <ConfirmInline danger confirmLabel="Disconnect" busy={disconnect.busy} error={disconnect.error} onConfirm={() => disconnect.run(() => setConfirm(false))} onCancel={() => setConfirm(false)}>
            {disconnect.prompt}
          </ConfirmInline>
        </div>
      ) : null}
    </Row>
  );
}

function T3Row({ name, machine, onManage }: { name: string; machine: Machine; onManage: () => void }) {
  const query = useT3Status();
  const read = statusRead(query);
  const summary = t3Summary(read);
  const disconnect = useDisconnectT3();
  const s = read.data;
  return (
    <ConnectionRow
      name={name}
      machine={machine}
      detail={s === undefined ? (read.error ?? "") : t3RowDetail(s)}
      chip={{ tone: summary.tone, label: summary.state }}
      onManage={onManage}
      disconnect={
        s === undefined || disconnectAvailability(s) === "hidden"
          ? null
          : {
              prompt: t3DisconnectPrompt(s, shortDate),
              busy: disconnect.isPending,
              error: disconnect.error,
              run: (done) => disconnect.mutate(undefined, { onSuccess: done }),
              reset: disconnect.reset,
            }
      }
    />
  );
}

function OpenCodeRow({ name, machine, onManage }: { name: string; machine: Machine; onManage: () => void }) {
  const query = useOpenCodeStatus();
  const read = statusRead(query);
  const summary = openCodeSummary(read);
  const disconnect = useDisconnectOpenCode();
  const s = read.data;
  return (
    <ConnectionRow
      name={name}
      machine={machine}
      detail={s === undefined ? (read.error ?? "") : openCodeRowDetail(s)}
      chip={{ tone: summary.tone, label: summary.state }}
      onManage={onManage}
      disconnect={
        s === undefined || s.state === "unconfigured"
          ? null
          : {
              prompt: OPENCODE_DISCONNECT_PROMPT,
              busy: disconnect.isPending,
              error: disconnect.error,
              run: (done) => disconnect.mutate(undefined, { onSuccess: done }),
              reset: disconnect.reset,
            }
      }
    />
  );
}

function GenericRow({ descriptor, machine, onManage }: { descriptor: HarnessDescriptor; machine: Machine; onManage: () => void }) {
  const status = useHarnessStatus(descriptor.kind);
  const disconnect = useDisconnectHarness(descriptor.kind);
  const s = status.data;
  const tone: ChipTone = s === undefined ? "idle" : s.connected ? "ok" : s.configured ? "bad" : "idle";
  return (
    <ConnectionRow
      name={descriptor.name}
      machine={machine}
      detail={s === undefined ? (status.error?.message ?? "") : (s.reason ?? s.lastError ?? "")}
      chip={{ tone, label: s?.state ?? "checking" }}
      onManage={onManage}
      disconnect={
        s === undefined || !descriptor.setup.disconnect || !s.configured
          ? null
          : {
              prompt: `Disconnect ${descriptor.name}? Tasks and history stay. You can connect again any time.`,
              busy: disconnect.isPending,
              error: disconnect.error,
              run: (done) => disconnect.mutate(undefined, { onSuccess: done }),
              reset: disconnect.reset,
            }
      }
    />
  );
}

/** A connection on a machine whose setup cannot run now: as Stage last recorded it, no actions. */
function RecordedRow({ machine, row }: { machine: Machine; row: ConnectionRowModel }) {
  const { kindName } = useHarnesses();
  return (
    <ConnectionRow
      name={kindName(row.harness)}
      machine={machine}
      detail={row.label === "" ? `connection #${row.id}` : `${row.label} · on record`}
      chip={{ tone: "idle", label: machine.status === "needs_update" ? "needs update" : "offline" }}
      disconnect={null}
    />
  );
}
