// OpenCode's registry entry: its setup panel, Settings row and status reader.
import { connectionStatusChip } from "../../lib/connections.ts";
import { ConnectionRow } from "../../components/ConnectionRow.tsx";
import { statusRead } from "../../lib/connections.ts";
import { statusSlots } from "../status.ts";
import type { ConnectionRowProps, HarnessSlots } from "../types.ts";
import { OPENCODE_STATUS, OPENCODE_STATUS_KEY, useOpenCodeStatus } from "./queries.ts";
import { OPENCODE_DISCONNECT_PROMPT, OpenCodeSetup, useDisconnectOpenCode } from "./Setup.tsx";
import { openCodeRowDetail, openCodeSummary } from "./status.ts";

export const OPENCODE_SLOTS: HarnessSlots = {
  Setup: OpenCodeSetup,
  Row: OpenCodeRow,
  ...statusSlots(OPENCODE_STATUS),
  handledSetupActions: (id) => id === "connect-local" || id === "pair" || id === "check",
  statusKey: () => OPENCODE_STATUS_KEY,
};

function OpenCodeRow({ descriptor, machine, onManage, isDefault, overflow }: ConnectionRowProps) {
  const query = useOpenCodeStatus();
  const read = statusRead(query);
  const summary = openCodeSummary(read);
  const disconnect = useDisconnectOpenCode();
  const s = read.data;
  return (
    <ConnectionRow
      name={descriptor.name}
      machine={machine}
      detail={s === undefined ? (read.error ?? "") : (s.presentation?.reason || openCodeRowDetail(s))}
      chip={s?.presentation === undefined ? { tone: summary.tone, label: summary.state } : connectionStatusChip(s.presentation)}
      onManage={onManage}
      isDefault={isDefault}
      overflow={overflow}
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
