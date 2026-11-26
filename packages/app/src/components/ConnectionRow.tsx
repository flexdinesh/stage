// One Settings > Connections row (name and where, state chip, Manage and a confirmed Disconnect; on phone the whole
// row opens Manage) and the harness scope of a machine. A harness's own row (src/harnesses/) fills it in.
import { useId, useMemo, useState, type ReactNode } from "react";
import { HarnessScopeProvider, LOCAL_SCOPE, type HarnessScope } from "../api/harnessScope.tsx";
import type { Machine } from "../api/types.ts";
import { machineScopeId } from "../lib/machines.ts";
import { IconStep } from "./icons.tsx";
import { Button, Chip, ConfirmInline, Row, RowButton, type ChipTone } from "./ui.tsx";

/** The harness scope of a machine: the local routes for the main machine, its worker's for a remote one. */
export function MachineScope({ machine, children }: { machine: Pick<Machine, "id" | "kind" | "label">; children: ReactNode }) {
  const { id, kind, label } = machine;
  const scope = useMemo<HarnessScope>(() => {
    const machineId = machineScopeId({ id, kind });
    return machineId === null ? LOCAL_SCOPE : { machineId, label };
  }, [id, kind, label]);
  return <HarnessScopeProvider scope={scope}>{children}</HarnessScopeProvider>;
}

/** The row's confirmed secondary action: Disconnect, or Forget on a disconnected row. */
export interface Disconnect {
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
export function ConnectionRow({
  name,
  machine,
  detail,
  chip,
  onManage,
  manageLabel = "Manage",
  disconnect,
  isDefault,
  overflow,
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
  overflow?: ReactNode;
}) {
  const [confirm, setConfirm] = useState(false);
  const statusDescription = useId();
  const action = disconnect?.label ?? "Disconnect";
  const state = (
    <Chip tone={chip.tone} dot className="shrink-0">
      {chip.label}
    </Chip>
  );
  const title = (
    <span className="flex min-w-0 items-center gap-x-2">
      <span className="min-w-0 text-body font-medium text-fg wrap-anywhere">{name}</span>
      {isDefault ? <Chip className="shrink-0" title="New tasks start on this connection">default</Chip> : null}
    </span>
  );
  const reason = detail ? <span className="min-w-0 font-mono text-meta wrap-anywhere text-muted">{detail}</span> : null;
  const manageName = `${manageLabel} ${name} on ${machine.label}`;
  return (
    <Row bare>
      <div className="flex items-start gap-1 pr-3 md:items-center md:gap-0">
        <div className="flex min-w-0 flex-1 items-start gap-1 md:hidden">
          {onManage === undefined ? (
            <div className="flex min-h-11 min-w-0 flex-1 flex-col gap-1 py-2 pr-3 pl-[30px]">
              <span className="flex flex-wrap items-center justify-between gap-2">{title}{state}</span>
              {reason}
            </div>
          ) : (
            <RowButton className="min-w-0 flex-1 items-start gap-2 pl-[30px]" onClick={onManage} data-focus-return={`connection:${machine.id}:${name}`} aria-label={manageName} aria-describedby={detail ? `${statusDescription}-state ${statusDescription}-reason` : `${statusDescription}-state`}>
              <span className="flex min-w-0 flex-1 flex-col gap-1 text-left">
                <span className="flex flex-wrap items-center justify-between gap-2">{title}<span id={`${statusDescription}-state`}>{state}</span></span>
                {reason === null ? null : <span id={`${statusDescription}-reason`}>{reason}</span>}
              </span>
              <span className="mt-0.5 text-muted"><IconStep /></span>
            </RowButton>
          )}
        </div>
        <div className="grid min-h-control min-w-0 flex-1 grid-cols-[minmax(6rem,1fr)_auto_minmax(0,2fr)_auto] items-center gap-x-3 py-1.5 pl-[30px] max-md:hidden">
          {title}
          {state}
          {reason ?? <span />}
          <div className="flex items-center gap-1.5">
            {onManage === undefined ? null : <Button small onClick={onManage} data-focus-return={`connection:${machine.id}:${name}`} aria-label={manageName}>{manageLabel}</Button>}
            {disconnect === null ? null : (
              <Button small variant="ghost" aria-expanded={confirm} aria-label={`${action} ${name} on ${machine.label}`}
                onClick={() => {
                  if (disconnect.busy) return;
                  disconnect.reset();
                  setConfirm(true);
                }}>
                {action}
              </Button>
            )}
          </div>
        </div>
        {overflow === undefined ? null : <div className="shrink-0 max-md:pt-1 md:pl-1.5">{overflow}</div>}
      </div>
      {confirm && disconnect !== null ? (
        <div className="pr-3 pb-2.5 pl-[30px] max-md:hidden">
          <ConfirmInline danger confirmLabel={action} busy={disconnect.busy} error={disconnect.error} onConfirm={() => {
            if (!disconnect.busy) disconnect.run(() => setConfirm(false));
          }} onCancel={() => setConfirm(false)}>
            {disconnect.prompt}
          </ConfirmInline>
        </div>
      ) : null}
    </Row>
  );
}
