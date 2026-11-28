// OpenCode's registry entry: its setup panel, Settings row and status reader.
import { connectionStatusChip } from "../../lib/connections.ts";
import { ConnectionRow } from "../../components/ConnectionRow.tsx";
import { Meta } from "../../components/ui.tsx";
import { statusRead } from "../../lib/connections.ts";
import { statusSlots } from "../status.ts";
import type { ConnectionRowProps, HarnessSlots, SessionSlotProps } from "../types.ts";
import { OPENCODE_STATUS, OPENCODE_STATUS_KEY, useOpenCodeStatus } from "./queries.ts";
import { OPENCODE_DISCONNECT_PROMPT, OpenCodeSetup, useDisconnectOpenCode } from "./Setup.tsx";
import { openCodeRowDetail, openCodeSummary } from "./status.ts";

export const OPENCODE_SLOTS: HarnessSlots = {
  session: { HeaderExtra: OpenCodeSessionHeader },
  Setup: OpenCodeSetup,
  Row: OpenCodeRow,
  ...statusSlots(OPENCODE_STATUS),
  handledSetupActions: (id) => id === "connect-local" || id === "pair" || id === "check",
  statusKey: () => OPENCODE_STATUS_KEY,
};

function OpenCodeSessionHeader({ activity }: SessionSlotProps) {
  const details = activity?.details;
  if (typeof details !== "object" || details === null) return null;
  const agent = "agent" in details && typeof details.agent === "string" ? details.agent : "";
  const model = "model" in details && typeof details.model === "string" ? details.model : "";
  const variant = "variant" in details && typeof details.variant === "string" ? details.variant : "";
  if (!agent && !model) return null;
  return (
    <div className="flex min-w-0 basis-full flex-wrap gap-x-3 gap-y-1.5">
      {agent ? <Meta className="min-w-0 wrap-anywhere">Agent {agent}</Meta> : null}
      {model ? <Meta className="min-w-0 wrap-anywhere">Model {model}{variant ? ` (${variant})` : ""}</Meta> : null}
    </div>
  );
}

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
