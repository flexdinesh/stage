// Claude Code's registry entry: its setup panel, Settings row and status reader (the generic status, read as its own).
import { useDisconnectHarness, useHarnessStatus, keys } from "../../api/queries.ts";
import { connectionStatusChip } from "../../lib/connections.ts";
import { ConnectionRow } from "../../components/ConnectionRow.tsx";
import { GENERIC_STATUS, statusSlots } from "../status.ts";
import type { ConnectionRowProps, HarnessSlots } from "../types.ts";
import { CLAUDE, CLAUDE_DISCONNECT_PROMPT, claudeRowDetail, claudeStatusView } from "./claude.ts";
import { ClaudeSetup } from "./Setup.tsx";
import { ClaudeSessionHeader } from "./SessionHeader.tsx";

export const CLAUDE_SLOTS: HarnessSlots = {
  session: { HeaderExtra: ClaudeSessionHeader, activityModelInHeader: true },
  Setup: ClaudeSetup,
  Row: ClaudeRow,
  // The generic status (same query and cache as its Settings row), read as Claude Code's.
  ...statusSlots({ query: GENERIC_STATUS.query, presentation: GENERIC_STATUS.presentation, view: (read, _kind, name) => claudeStatusView(read, name), rowDetail: claudeRowDetail }),
  handledSetupActions: (id) => id === "connect" || id === "check",
  statusKey: (kind) => [...keys.harnessStatus, kind],
};

function ClaudeRow({ descriptor, machine, onManage, isDefault, overflow }: ConnectionRowProps) {
  const status = useHarnessStatus(CLAUDE);
  const disconnect = useDisconnectHarness(CLAUDE);
  const s = status.data;
  return (
    <ConnectionRow
      name={descriptor.name}
      machine={machine}
      detail={s === undefined ? (status.error?.message ?? "") : (s.reason || s.lastError || claudeRowDetail(s))}
      chip={s === undefined ? { tone: "idle", label: "checking" } : connectionStatusChip(s)}
      onManage={onManage}
      isDefault={isDefault}
      overflow={overflow}
      disconnect={
        s === undefined || !s.configured
          ? null
          : {
              prompt: CLAUDE_DISCONNECT_PROMPT,
              busy: disconnect.isPending,
              error: disconnect.error,
              run: (done) => disconnect.mutate(undefined, { onSuccess: done }),
              reset: disconnect.reset,
            }
      }
    />
  );
}
