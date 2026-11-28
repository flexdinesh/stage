// Codex's registry entry: its setup panel, Settings row and status reader.
import { connectionStatusChip } from "../../lib/connections.ts";
import { ConnectionRow } from "../../components/ConnectionRow.tsx";
import { statusRead } from "../../lib/connections.ts";
import { Meta } from "../../components/ui.tsx";
import { statusSlots } from "../status.ts";
import type { ConnectionRowProps, HarnessSlots, SessionSlotProps } from "../types.ts";
import { codexThreadMetadata } from "./codex.ts";
import { CODEX_STATUS, CODEX_STATUS_KEY, useCodexStatus } from "./queries.ts";
import { CODEX_DISCONNECT_PROMPT, CodexSetup, useDisconnectCodex } from "./Setup.tsx";
import { codexRowDetail, codexSummary } from "./status.ts";

export const CODEX_SLOTS: HarnessSlots = {
  Setup: CodexSetup,
  Row: CodexRow,
  ...statusSlots(CODEX_STATUS),
  handledSetupActions: (id) => id === "connect" || id === "check",
  statusKey: () => CODEX_STATUS_KEY,
  session: { HeaderExtra: CodexThreadMeta, activityModelInHeader: true },
};

function CodexThreadMeta({ activity }: SessionSlotProps) {
  const parts = codexThreadMetadata(activity?.details);
  if (parts.length === 0) return null;
  return (
    <dl aria-label="Codex thread settings" className="flex min-w-0 basis-full flex-wrap gap-x-3 gap-y-1">
      {parts.map(({ label, value }) => (
        <div key={label} className="min-w-0">
          <dt className="sr-only">{label}</dt>
          <dd><Meta title={label} className="wrap-anywhere">{value}</Meta></dd>
        </div>
      ))}
    </dl>
  );
}

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
