// T3's registry entry: its setup panel, Settings row and status reader.
import { connectionStatusChip } from "../../lib/connections.ts";
import { ConnectionRow } from "../../components/ConnectionRow.tsx";
import { shortDate } from "../../lib/format.ts";
import { statusRead } from "../../lib/connections.ts";
import { statusSlots } from "../status.ts";
import type { ConnectionRowProps, HarnessSlots } from "../types.ts";
import { disconnectAvailability, t3DisconnectPrompt } from "./onboarding.ts";
import { T3_STATUS, T3_STATUS_KEY, useT3Status } from "./queries.ts";
import { T3ConnectionPanel, useDisconnectT3 } from "./Setup.tsx";
import { t3RowDetail, t3Summary } from "./status.ts";

export const T3_SLOTS: HarnessSlots = {
  Setup: T3ConnectionPanel,
  Row: T3Row,
  ...statusSlots(T3_STATUS),
  handledSetupActions: (id) => id === "pair" || id === "auto-pair",
  statusKey: () => T3_STATUS_KEY,
};

function T3Row({ descriptor, machine, onManage, isDefault, overflow }: ConnectionRowProps) {
  const query = useT3Status();
  const read = statusRead(query);
  const summary = t3Summary(read);
  const disconnect = useDisconnectT3();
  const s = read.data;
  return (
    <ConnectionRow
      name={descriptor.name}
      machine={machine}
      detail={s === undefined ? (read.error ?? "") : (s.presentation?.reason || t3RowDetail(s))}
      chip={s?.presentation === undefined ? { tone: summary.tone, label: summary.state } : connectionStatusChip(s.presentation)}
      onManage={onManage}
      isDefault={isDefault}
      overflow={overflow}
      disconnect={
        s === undefined || disconnectAvailability(s) === "hidden"
          ? null
          : {
              prompt: t3DisconnectPrompt(s, shortDate),
              busy: disconnect.isPending,
              error: disconnect.error,
              run: (done) => disconnect.mutate(undefined, { onSuccess: done }),
              reset: disconnect.reset,
            }
      }
    />
  );
}
