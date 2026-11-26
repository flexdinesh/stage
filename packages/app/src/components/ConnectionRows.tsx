// Settings > Connections: a machine's harness connections as one-line rows (name, where, state, Manage,
// Disconnect), its Add connection menu, and the Manage drawer holding the harness's setup panel. Rows never expand;
// Disconnect confirms inline under its row. On phone the whole row opens Manage (Disconnect lives in the drawer).
// A disconnected connection stays a row (Reconnect, Forget) until it is forgotten or connected again.
// A machine whose setup cannot run lists its connections on record, read-only.
// Logic: lib/connectionRows.ts.
import { useQuery } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { useForgetConnection, useHarnesses, usePreferences, useScopedHarnessDescriptors } from "../api/queries.ts";
import type { HarnessConnection, HarnessDescriptor, HarnessStatus, Machine, ReplacedConnection } from "../api/types.ts";
import { harnessSlots } from "../harnesses/index.ts";
import { addConnectionOptions, addConnectionReason, connectShortcuts, connectionRows, machineLive, type ConnectionRowModel } from "../lib/connectionRows.ts";
import { availableSetupActions } from "../lib/connectionSetup.ts";
import { forgetPrompt, usageText } from "../lib/harnesses.ts";
import { useApi } from "../platform/context.tsx";
import { machineSetupAlert, machineStatusLabel } from "../lib/machines.ts";
import { ConnectionDeclaredActions } from "./ConnectionDeclaredActions.tsx";
import { CheckConnection, ConfirmConnectionAction, HarnessFacts, ReplacedConnectionNotice } from "./HarnessPresentation.tsx";
import { IconMore } from "./icons.tsx";
import { ConnectionCapabilities } from "./ConnectionCapabilities.tsx";
import { ConnectionRow, MachineScope } from "./ConnectionRow.tsx";
import { Button, Drawer, Hint, IconButton, Menu, MenuItem, Panel, Row, RowList, useMenu } from "./ui.tsx";

export { MachineScope } from "./ConnectionRow.tsx";

/** A machine's rows and Add connection options; call inside its MachineScope. */
export function useMachineConnections(machine: Machine) {
  const harnesses = useHarnesses();
  const live = machineLive(machine);
  const descriptors = useScopedHarnessDescriptors(live);
  const rows = connectionRows(machine, harnesses.connection);
  const kinds = live ? (descriptors.data?.harnesses ?? null) : [];
  const options = kinds === null ? null : addConnectionOptions(kinds, rows);
  const descriptor = (kind: string): HarnessDescriptor | undefined => kinds?.find((d) => d.kind === kind) ?? harnesses.descriptor(kind);
  return { live, rows, options, addReason: addConnectionReason(machine, options), descriptor };
}

/** The Manage (or Add) drawer's subject: a harness on the machine; `disconnected` when its row is a disconnected one. */
export type ManageTarget = { descriptor: HarnessDescriptor; disconnected?: ConnectionRowModel; actionId?: string; connectionId?: number };

/** Add connection: a menu of the machine's connectable harnesses; connected ones are listed disabled. */
export function AddConnection({ machine, connections, onPick }: { machine: Machine; connections: ReturnType<typeof useMachineConnections>; onPick: (t: ManageTarget) => void }) {
  const menu = useMenu();
  const trigger = useRef<HTMLButtonElement>(null);
  const reason = connections.addReason;
  return (
    <>
      <Button ref={trigger} small disabled={reason !== null} title={reason ?? undefined} aria-label={`Add connection on ${machine.label}`} {...menu.triggerProps}>
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
              if (descriptor !== undefined) {
                trigger.current?.focus();
                onPick({ descriptor });
              }
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
        const target = descriptor === undefined ? undefined : { descriptor, connectionId: r.id };
        if (!connections.live || descriptor === undefined || !descriptor.available || !descriptor.connectable) {
          return <RecordedRow key={r.id} machine={machine} row={r} isDefault={isDefault} descriptor={descriptor}
            onManage={target === undefined ? undefined : () => onManage(target)} />;
        }
        if (r.disconnected) return <DisconnectedRow key={r.id} machine={machine} row={r} isDefault={isDefault}
          onManage={() => onManage({ descriptor, connectionId: r.id, disconnected: r })} />;
        const { Row: HarnessRow } = harnessSlots(r.harness);
        return <HarnessRow key={r.id} descriptor={descriptor} machine={machine} onManage={() => onManage({ descriptor, connectionId: r.id })}
          overflow={<ConnectionActionMenu descriptor={descriptor} machine={machine} onPick={(actionId) => onManage({ descriptor, connectionId: r.id, actionId })} />}
          isDefault={isDefault} />;
      })}
    </RowList>
  );
}

function ConnectionActionMenu({ descriptor, machine, onPick }: {
  descriptor: HarnessDescriptor;
  machine: Machine;
  onPick: (id: string) => void;
}) {
  const { Presentation } = harnessSlots(descriptor.kind);
  return (
    <Presentation kind={descriptor.kind} enabled>
      {(status) => <ConnectionActionItems descriptor={descriptor} machine={machine} status={status} onPick={onPick} />}
    </Presentation>
  );
}

function ConnectionActionItems({ descriptor, machine, status, onPick }: {
  descriptor: HarnessDescriptor;
  machine: Machine;
  status: HarnessStatus | undefined;
  onPick: (id: string) => void;
}) {
  const menu = useMenu();
  const trigger = useRef<HTMLButtonElement>(null);
  const actions = status === undefined ? [] : availableSetupActions(descriptor.setup.actions, status.actions);
  if (actions.length === 0) return null;
  const label = `${descriptor.name} actions on ${machine.label}`;
  return (
    <>
      <IconButton ref={trigger} small label={label} icon={IconMore} data-focus-return={`connection-actions:${machine.id}:${descriptor.name}`} aria-haspopup="menu" {...menu.triggerProps} />
      <Menu menu={menu} label={label} align="right" className="w-64">
        {actions.map((action) => (
          <MenuItem key={action.id} danger={action.destructive}
            reason={action.availability.available ? null : action.availability.reason || "Unavailable"}
            onSelect={() => {
              trigger.current?.focus();
              onPick(action.id);
            }}>
            {action.label}
          </MenuItem>
        ))}
      </Menu>
    </>
  );
}

/** The harness's setup, or a scoped action selected from the invoking row. */
export function ManageDrawer({ machine, target, onClose }: { machine: Machine; target: ManageTarget; onClose: () => void }) {
  return (
    <Drawer title={`${target.descriptor.name} on ${machine.label}`} onClose={onClose}>
      <MachineScope machine={machine}>
        <ManageBody machine={machine} target={target} onClose={onClose} />
      </MachineScope>
    </Drawer>
  );
}

function ManageBody({ machine, target, onClose }: { machine: Machine; target: ManageTarget; onClose: () => void }) {
  const [actionId, select] = useState(target.actionId);
  const [replaced, setReplaced] = useState<ReplacedConnection | null>(null);
  const descriptors = useScopedHarnessDescriptors(machineLive(machine));
  const descriptor = descriptors.data?.harnesses.find((entry) => entry.kind === target.descriptor.kind) ?? target.descriptor;
  const { Setup, Presentation, handledSetupActions } = harnessSlots(descriptor.kind);
  const live = machineLive(machine) && descriptor.available && descriptor.connectable;
  if (!live) {
    return <RecordedConnection machine={machine} descriptor={descriptor} id={target.connectionId}
      {...(machineLive(machine) && descriptors.error !== null ? { retry: () => descriptors.refetch({ throwOnError: true }) } : {})} />;
  }
  return (
    <Presentation kind={descriptor.kind} enabled>
      {(status) => {
        const changed = actionId !== undefined && status !== undefined && status.connectionId !== target.connectionId;
        const actions = status === undefined || changed ? [] : availableSetupActions(descriptor.setup.actions, status.actions)
          .filter((action) => actionId === undefined ? !handledSetupActions(action.id) : action.id === actionId);
        return (
          <div className="flex min-w-0 flex-col gap-3.5">
            <ReplacedConnectionNotice replaced={replaced} />
            {actionId === undefined ? <Setup descriptor={descriptor} /> : (
              <div><Button small onClick={() => select(undefined)}>Back to Manage</Button></div>
            )}
            {changed ? <Hint>This connection changed. Open its current row to run an action.</Hint> : null}
            {actions.length === 0 || status === undefined ? null : (
              <Panel title={actionId === undefined ? "Actions" : actions[0]?.label ?? "Action"}>
                <ConnectionDeclaredActions descriptor={descriptor} status={status} actions={actions} onReplaced={setReplaced} />
              </Panel>
            )}
            {actionId !== undefined || target.disconnected !== undefined ? null : <ConnectionCapabilities descriptor={descriptor} />}
            {actionId !== undefined || target.disconnected === undefined ? null : <ForgetDisconnected row={target.disconnected} name={descriptor.name} onForgotten={onClose} />}
          </div>
        );
      }}
    </Presentation>
  );
}

function RecordedConnection({ machine, descriptor, id, retry }: {
  machine: Machine;
  descriptor: HarnessDescriptor;
  id: number | undefined;
  retry?: () => Promise<unknown>;
}) {
  const api = useApi();
  const { Presentation } = harnessSlots(descriptor.kind);
  const saved = useQuery({
    queryKey: ["connection-facts", id],
    queryFn: () => api.connectionFacts(id ?? 0),
    enabled: id !== undefined,
    retry: false,
  });
  const reason = machineSetupAlert(machine) || descriptor.reason || "Connection setup is unavailable.";
  return (
    <Presentation kind={descriptor.kind} enabled={false}>
      {(status) => (
        <div className="flex flex-col gap-3">
          <Hint className="wrap-anywhere">{reason}</Hint>
          <HarnessFacts facts={saved.data?.facts ?? (status?.connectionId === id ? status?.facts : undefined)}
            offline={machine.status === "offline"} />
          {retry === undefined ? null : <CheckConnection run={retry} />}
        </div>
      )}
    </Presentation>
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

function ForgetDisconnected({ row, name, onForgotten }: { row: ConnectionRowModel; name: string; onForgotten: () => void }) {
  return (
    <div className="flex flex-col gap-2 border-t border-line pt-3.5">
      <Hint>Forget this {name} connection to remove it from Settings. Its tasks keep their history.</Hint>
      <ForgetConnection connection={row} onForgotten={onForgotten} />
    </div>
  );
}

function ForgetConnection({ connection, onForgotten }: {
  connection: Pick<HarnessConnection, "id" | "label">;
  onForgotten?: () => void;
}) {
  const forget = useForgetConnection();
  return <ConfirmConnectionAction accessibleLabel={`Forget ${connection.label || `connection #${connection.id}`}`} action={{
    label: "Forget",
    prompt: forgetPrompt(connection),
    busy: forget.isPending,
    error: forget.error,
    run: (done) => forget.mutate(connection.id, { onSuccess: () => { done(); onForgotten?.(); } }),
    reset: forget.reset,
  }} />;
}

export function PreviousConnections() {
  const harnesses = useHarnesses();
  const previous = harnesses.list.filter((connection) => !connection.active && connection.forgottenAt === null);
  if (previous.length === 0) return null;
  return (
    <details className="border border-line bg-surface">
      <summary className="cursor-pointer bg-panel px-3 py-2 text-ui font-medium">Previous connections ({previous.length})</summary>
      <RowList label="Previous connections" className="border-t border-line">
        {previous.map((connection) => (
          <Row key={connection.id}>
            <div className="flex flex-wrap items-start justify-between gap-2">
              <div className="min-w-0 flex-1">
                <p className="text-ui wrap-anywhere">{harnesses.kindName(connection.harness)} · {connection.label || `#${connection.id}`}</p>
                <Hint>{usageText(connection)}</Hint>
              </div>
              <ForgetConnection connection={connection} />
            </div>
          </Row>
        ))}
      </RowList>
    </details>
  );
}

/** A connection on a machine whose setup cannot run now: as Stage last recorded it, no actions. */
function RecordedRow({ machine, row, isDefault, descriptor, onManage }: { machine: Machine; row: ConnectionRowModel; isDefault: boolean; descriptor: HarnessDescriptor | undefined; onManage: (() => void) | undefined }) {
  const { kindName } = useHarnesses();
  return (
    <ConnectionRow
      name={kindName(row.harness)}
      machine={machine}
      detail={machineSetupAlert(machine) || descriptor?.reason || row.label || `Connection #${row.id}`}
      chip={{ tone: "idle", label: machineLive(machine) ? "Unavailable" : machineStatusLabel(machine.status) }}
      {...(onManage === undefined ? {} : { onManage })}
      disconnect={null}
      isDefault={isDefault}
    />
  );
}
