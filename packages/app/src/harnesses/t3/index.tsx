// T3's registry entry: its setup panel, Settings row and status reader.
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
  statusKey: () => T3_STATUS_KEY,
  // The Browser URL (Settings) makes Open in T3 links work from other devices.
  webAddress: true,
};

function T3Row({ descriptor, machine, onManage, isDefault }: ConnectionRowProps) {
  const query = useT3Status();
  const read = statusRead(query);
  const summary = t3Summary(read);
  const disconnect = useDisconnectT3();
  const s = read.data;
  return (
    <ConnectionRow
      name={descriptor.name}
      machine={machine}
      detail={s === undefined ? (read.error ?? "") : t3RowDetail(s)}
      chip={{ tone: summary.tone, label: summary.state }}
      onManage={onManage}
      isDefault={isDefault}
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
