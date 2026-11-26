// Codex's registry entry: its setup panel, Settings row and status reader.
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
  statusKey: () => CODEX_STATUS_KEY,
  // Sessions resume through a copied command; there is no address to set.
  webAddress: false,
};

function CodexRow({ descriptor, machine, onManage, isDefault }: ConnectionRowProps) {
  const read = statusRead(useCodexStatus());
  const summary = codexSummary(read);
  const disconnect = useDisconnectCodex();
  const s = read.data;
  return (
    <ConnectionRow
      name={descriptor.name}
      machine={machine}
      detail={s === undefined ? (read.error ?? "") : codexRowDetail(s)}
      chip={{ tone: summary.tone, label: summary.state }}
      onManage={onManage}
      isDefault={isDefault}
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
