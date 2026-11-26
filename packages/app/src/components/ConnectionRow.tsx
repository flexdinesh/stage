// One Settings > Connections row (name and where, state chip, Manage and a confirmed Disconnect; on phone the whole
// row opens Manage) and the harness scope of a machine. A harness's own row (src/harnesses/) fills it in.
import { useMemo, useState, type ReactNode } from "react";
import { HarnessScopeProvider, LOCAL_SCOPE, type HarnessScope } from "../api/harnessScope.tsx";
import type { Machine } from "../api/types.ts";
import { machineScopeId } from "../lib/machines.ts";
import { IconStep } from "./icons.tsx";
import { Button, Chip, ConfirmInline, Hint, Row, RowButton, type ChipTone } from "./ui.tsx";

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

