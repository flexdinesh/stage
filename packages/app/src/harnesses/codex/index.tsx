// Codex's registry entry: its setup panel, Settings row and status reader.
import { connectionStatusChip } from "../../lib/connections.ts";
import { ConnectionRow } from "../../components/ConnectionRow.tsx";
import { statusRead } from "../../lib/connections.ts";
import { statusSlots } from "../status.ts";
import type { ConnectionRowProps, HarnessSlots } from "../types.ts";
import { CODEX_STATUS, CODEX_STATUS_KEY, useCodexStatus } from "./queries.ts";
import { CODEX_DISCONNECT_PROMPT, CodexSetup, useDisconnectCodex } from "./Setup.tsx";
import { codexRowDetail, codexSummary } from "./status.ts";

export const CODEX_SLOTS: HarnessSlots = {
  Setup: CodexSetup,
  Row: CodexRow,
  ...statusSlots(CODEX_STATUS),
  handledSetupActions: (id) => id === "connect" || id === "check",
  statusKey: () => CODEX_STATUS_KEY,
};

function CodexRow({ descriptor, machine, onManage, isDefault, overflow }: ConnectionRowProps) {
  const read = statusRead(useCodexStatus());
  const summary = codexSummary(read);
  const disconnect = useDisconnectCodex();
  const s = read.data;
  return (
    <ConnectionRow
      name={descriptor.name}
      machine={machine}
      detail={s === undefined ? (read.error ?? "") : (s.reason || codexRowDetail(s))}
      chip={s === undefined ? { tone: summary.tone, label: summary.state } : connectionStatusChip(s)}
      onManage={onManage}
      isDefault={isDefault}
      overflow={overflow}
      disconnect={
        s === undefined || !s.configured
          ? null
          : {
              prompt: CODEX_DISCONNECT_PROMPT,
              busy: disconnect.isPending,
              error: disconnect.error,
              run: (done) => disconnect.mutate(undefined, { onSuccess: done }),
              reset: disconnect.reset,
            }
      }
    />
  );
}
