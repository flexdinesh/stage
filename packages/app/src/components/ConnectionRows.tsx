// Settings > Connections: a machine's harness connections as one-line rows (name, where, state, Manage,
// Disconnect), its Add connection menu, and the Manage drawer holding the harness's setup panel. Rows never expand;
// Disconnect confirms inline under its row. On phone the whole row opens Manage (Disconnect lives in the drawer).
// A disconnected connection stays a row (Reconnect, Forget) until it is forgotten or connected again.
// A machine whose setup cannot run lists its connections on record, read-only.
// Logic: lib/connectionRows.ts.
import { useState } from "react";
import { useForgetConnection, useHarnesses, usePreferences, useScopedHarnessDescriptors } from "../api/queries.ts";
import type { HarnessDescriptor, Machine } from "../api/types.ts";
import { harnessSlots } from "../harnesses/index.ts";
import { addConnectionOptions, addConnectionReason, connectShortcuts, connectionRows, machineLive, type ConnectionRowModel } from "../lib/connectionRows.ts";
import { forgetPrompt, usageText } from "../lib/harnesses.ts";
import { machineSetupAlert, machineStatusLabel } from "../lib/machines.ts";
import { ConnectionCapabilities } from "./ConnectionCapabilities.tsx";
import { ConnectionRow, MachineScope } from "./ConnectionRow.tsx";
import { Button, ConfirmInline, Drawer, Hint, Menu, MenuItem, RowList, useMenu } from "./ui.tsx";

export { MachineScope } from "./ConnectionRow.tsx";

/** A machine's rows and Add connection options; call inside its MachineScope. */
export function useMachineConnections(machine: Machine) {
  const harnesses = useHarnesses();
  const live = machineLive(machine);
  const descriptors = useScopedHarnessDescriptors(live);
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
        const { Row } = harnessSlots(r.harness);
        return <Row key={r.id} descriptor={descriptor} machine={machine} onManage={() => onManage({ descriptor })} isDefault={isDefault} />;
      })}
    </RowList>
  );
}

/** The Manage drawer: the harness's own setup panel(s) on the machine. */
export function ManageDrawer({ machine, target, onClose }: { machine: Machine; target: ManageTarget; onClose: () => void }) {
  const { Setup } = harnessSlots(target.descriptor.kind);
  const alert = machineSetupAlert(machine);
  return (
    <Drawer title={`${target.descriptor.name} on ${machine.label}`} onClose={onClose}>
      {alert === null ? (
        <MachineScope machine={machine}>
          <div className="flex flex-col gap-3.5">
            <Setup descriptor={target.descriptor} />
            {target.disconnected === undefined ? <ConnectionCapabilities descriptor={target.descriptor} /> : null}
            {target.disconnected === undefined ? null : <ForgetDisconnected row={target.disconnected} name={target.descriptor.name} onForgotten={onClose} />}
          </div>
        </MachineScope>
      ) : (
        <Hint className="wrap-anywhere">{alert}</Hint>
      )}
    </Drawer>
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
      chip={{ tone: "idle", label: machineLive(machine) ? "on record" : machineStatusLabel(machine.status) }}
      disconnect={null}
      isDefault={isDefault}
    />
  );
}
