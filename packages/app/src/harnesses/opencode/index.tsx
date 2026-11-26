// OpenCode's registry entry: its setup panel, Settings row and status reader.
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
  statusKey: () => OPENCODE_STATUS_KEY,
  // Its address for other devices and web client URL (Advanced) make session links work from other devices.
  webAddress: true,
};

function OpenCodeRow({ descriptor, machine, onManage, isDefault }: ConnectionRowProps) {
  const query = useOpenCodeStatus();
  const read = statusRead(query);
  const summary = openCodeSummary(read);
  const disconnect = useDisconnectOpenCode();
  const s = read.data;
  return (
    <ConnectionRow
      name={descriptor.name}
      machine={machine}
      detail={s === undefined ? (read.error ?? "") : openCodeRowDetail(s)}
      chip={{ tone: summary.tone, label: summary.state }}
      onManage={onManage}
      isDefault={isDefault}
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
