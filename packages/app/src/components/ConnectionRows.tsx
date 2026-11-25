// Settings > Connections: a machine's harness connections as one-line rows (name, where, state, Manage,
// Disconnect), its Add connection menu, and the Manage drawer holding the harness's setup panel. Rows never expand;
// Disconnect confirms inline under its row. On phone the whole row opens Manage (Disconnect lives in the drawer).
// A disconnected connection stays a row (Reconnect, Forget) until it is forgotten or connected again.
// A machine whose setup cannot run (offline, another API version) lists its connections on record, read-only.
// Logic: lib/connectionRows.ts.
import { useMemo, useState, type ReactNode } from "react";
import { HarnessScopeProvider, LOCAL_SCOPE, type HarnessScope } from "../api/harnessScope.tsx";
import { useForgetConnection, useHarnesses, useOpenCodeStatus, usePreferences, useScopedHarnessDescriptors, useT3Status } from "../api/queries.ts";
import type { HarnessDescriptor, Machine } from "../api/types.ts";
import { addConnectionOptions, addConnectionReason, connectShortcuts, connectionRows, machineLive, type ConnectionRowModel } from "../lib/connectionRows.ts";
import { openCodeRowDetail, openCodeSummary, statusRead, t3RowDetail, t3Summary } from "../lib/connections.ts";
import { shortDate } from "../lib/format.ts";
import { forgetPrompt, usageText } from "../lib/harnesses.ts";
import { machineScopeId } from "../lib/machines.ts";
import { disconnectAvailability, t3DisconnectPrompt } from "../lib/onboarding.ts";
import { ConnectionSetup, useDisconnectHarness, useHarnessStatus } from "./HarnessConnections.tsx";
import { IconStep } from "./icons.tsx";
import { OPENCODE_DISCONNECT_PROMPT, useDisconnectOpenCode } from "./OpenCodeSetup.tsx";
import { useDisconnectT3 } from "./T3Setup.tsx";
import { Button, Chip, ConfirmInline, Drawer, Hint, Menu, MenuItem, Row, RowButton, RowList, useMenu, type ChipTone } from "./ui.tsx";

/** The harness scope of a machine: the local routes for the main machine, its worker's for a remote one. */
export function MachineScope({ machine, children }: { machine: Pick<Machine, "id" | "kind" | "label">; children: ReactNode }) {
  const { id, kind, label } = machine;
  const scope = useMemo<HarnessScope>(() => {
    const machineId = machineScopeId({ id, kind });
    return machineId === null ? LOCAL_SCOPE : { machineId, label };
  }, [id, kind, label]);
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

/** The Manage (or Add) drawer's subject: a harness on the machine; `disconnected` when its row is a disconnected one. */
export type ManageTarget = { descriptor: HarnessDescriptor; disconnected?: ConnectionRowModel };

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
            detail={o.detail}
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
  const defaultId = usePreferences().data?.defaultConnectionId ?? null;
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
        const isDefault = r.id === defaultId;
        if (!connections.live || descriptor === undefined) return <RecordedRow key={r.id} machine={machine} row={r} isDefault={isDefault} />;
        if (r.disconnected) return <DisconnectedRow key={r.id} machine={machine} row={r} isDefault={isDefault} onManage={() => onManage({ descriptor, disconnected: r })} />;
        const manage = () => onManage({ descriptor });
        switch (r.harness) {
          case "t3":
            return <T3Row key={r.id} name={descriptor.name} machine={machine} onManage={manage} isDefault={isDefault} />;
          case "opencode":
            return <OpenCodeRow key={r.id} name={descriptor.name} machine={machine} onManage={manage} isDefault={isDefault} />;
          default:
            return <GenericRow key={r.id} descriptor={descriptor} machine={machine} onManage={manage} isDefault={isDefault} />;
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
          {target.disconnected === undefined ? null : <ForgetDisconnected row={target.disconnected} name={target.descriptor.name} onForgotten={onClose} />}
        </div>
      </MachineScope>
    </Drawer>
  );
}

// ---- Rows ----

/** The row's confirmed secondary action: Disconnect, or Forget on a disconnected row. */
interface Disconnect {
  /** Button and confirm label; "Disconnect" when omitted. */
  label?: string;
  prompt: string;
  busy: boolean;
  error: unknown;
  /** Disconnects; `done` closes the confirm once it succeeded. */
  run: (done: () => void) => void;
  reset: () => void;
}

/** One connection: name (and the default chip) and where, state chip, Manage and Disconnect (laptop) or a whole-row button (phone). */
function ConnectionRow({
  name,
  machine,
  detail,
  chip,
  onManage,
  manageLabel = "Manage",
  disconnect,
  isDefault,
}: {
  name: string;
  machine: Machine;
  detail: string;
  chip: { tone: ChipTone; label: string };
  onManage?: () => void;
  /** The primary button's label: Manage, or Reconnect on a disconnected row. */
  manageLabel?: string;
  /** null: the harness cannot disconnect from here. */
  disconnect: Disconnect | null;
  /** New tasks start on this connection (Preferences.defaultConnectionId). */
  isDefault: boolean;
}) {
  const [confirm, setConfirm] = useState(false);
  const action = disconnect?.label ?? "Disconnect";
  const state = (
    <Chip tone={chip.tone} dot className="shrink-0">
      {chip.label}
    </Chip>
  );
  const title = (
    <span className="flex min-w-0 flex-1 items-baseline gap-x-2">
      <span className="shrink-0 text-body font-medium text-fg">{name}</span>
      {isDefault ? (
        <Chip className="shrink-0 self-center" title="New tasks start on this connection">
          default
        </Chip>
      ) : null}
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
          <RowButton className="gap-3 pl-[30px] md:hidden" onClick={onManage} aria-label={`${manageLabel} ${name} on ${machine.label}`}>
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
              <Button small onClick={onManage} aria-label={`${manageLabel} ${name} on ${machine.label}`}>
                {manageLabel}
              </Button>
              {disconnect === null ? null : (
                <Button
                  small
                  variant="ghost"
                  disabled={confirm}
                  aria-label={`${action} ${name} on ${machine.label}`}
                  onClick={() => {
                    disconnect.reset();
                    setConfirm(true);
                  }}
                >
                  {action}
                </Button>
              )}
            </div>
          </div>
        </>
      )}
      {confirm && disconnect !== null ? (
        <div className="pr-3 pb-2.5 pl-[30px] max-md:hidden">
          <ConfirmInline danger confirmLabel={action} busy={disconnect.busy} error={disconnect.error} onConfirm={() => disconnect.run(() => setConfirm(false))} onCancel={() => setConfirm(false)}>
            {disconnect.prompt}
          </ConfirmInline>
        </div>
      ) : null}
    </Row>
  );
}

function T3Row({ name, machine, onManage, isDefault }: { name: string; machine: Machine; onManage: () => void; isDefault: boolean }) {
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
      isDefault={isDefault}
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

function OpenCodeRow({ name, machine, onManage, isDefault }: { name: string; machine: Machine; onManage: () => void; isDefault: boolean }) {
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
      isDefault={isDefault}
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

function GenericRow({ descriptor, machine, onManage, isDefault }: { descriptor: HarnessDescriptor; machine: Machine; onManage: () => void; isDefault: boolean }) {
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
      isDefault={isDefault}
      disconnect={
        s === undefined || !descriptor.setup.disconnect || !s.configured
          ? null
          : {
              prompt: `Disconnect ${descriptor.name}? Tasks and history stay. You can connect again any time. It stays listed as disconnected until you forget it.`,
              busy: disconnect.isPending,
              error: disconnect.error,
              run: (done) => disconnect.mutate(undefined, { onSuccess: done }),
              reset: disconnect.reset,
            }
      }
    />
  );
}

/** "controlbox · 400 tasks · 27 bindings": the instance and what stays on a disconnected connection. */
function disconnectedDetail(row: ConnectionRowModel, c: Parameters<typeof usageText>[0] | undefined): string {
  const usage = c === undefined ? null : usageText(c);
  return [row.label === "" ? null : row.label, usage].filter((p) => p !== null).join(" · ");
}

/** A disconnected connection: its history stays; Reconnect opens its setup, Forget lets go of it. */
function DisconnectedRow({ machine, row, isDefault, onManage }: { machine: Machine; row: ConnectionRowModel; isDefault: boolean; onManage: () => void }) {
  const harnesses = useHarnesses();
  const forget = useForgetConnection();
  const c = harnesses.connection(row.id);
  return (
    <ConnectionRow
      name={harnesses.kindName(row.harness)}
      machine={machine}
      detail={disconnectedDetail(row, c)}
      chip={{ tone: "idle", label: "disconnected" }}
      onManage={onManage}
      manageLabel="Reconnect"
      isDefault={isDefault}
      disconnect={{
        label: "Forget",
        prompt: forgetPrompt({ label: row.label }),
        busy: forget.isPending,
        error: forget.error,
        run: (done) => forget.mutate(row.id, { onSuccess: done }),
        reset: forget.reset,
      }}
    />
  );
}

/** The Reconnect drawer's Forget (phone has no row actions): lets go of the disconnected connection after a confirm. */
function ForgetDisconnected({ row, name, onForgotten }: { row: ConnectionRowModel; name: string; onForgotten: () => void }) {
  const forget = useForgetConnection();
  const [confirm, setConfirm] = useState(false);
  return (
    <div className="flex flex-col gap-2 border-t border-line pt-3.5">
      {confirm ? (
        <ConfirmInline danger confirmLabel="Forget" busy={forget.isPending} error={forget.error} onConfirm={() => forget.mutate(row.id, { onSuccess: onForgotten })} onCancel={() => setConfirm(false)}>
          {forgetPrompt({ label: row.label })}
        </ConfirmInline>
      ) : (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <Hint className="min-w-0 flex-1">Not using this {name} any more? Forget it; its tasks keep their history.</Hint>
          <Button
            small
            variant="ghost"
            onClick={() => {
              forget.reset();
              setConfirm(true);
            }}
          >
            Forget
          </Button>
        </div>
      )}
    </div>
  );
}

/** A connection on a machine whose setup cannot run now: as Stage last recorded it, no actions. */
function RecordedRow({ machine, row, isDefault }: { machine: Machine; row: ConnectionRowModel; isDefault: boolean }) {
  const { kindName } = useHarnesses();
  return (
    <ConnectionRow
      name={kindName(row.harness)}
      machine={machine}
      detail={row.label === "" ? `connection #${row.id}` : `${row.label} · on record`}
      chip={{ tone: "idle", label: machine.status === "needs_update" ? "needs update" : "offline" }}
      disconnect={null}
      isDefault={isDefault}
    />
  );
}
