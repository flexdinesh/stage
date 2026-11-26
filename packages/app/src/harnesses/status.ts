// A harness's status reader as registry slots: every connection's view on its own machine (the status button and
// launch gate), and the live chip on the harness scope's machine (a Config panel's Harness row).
import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useHarnessApi, useScopedKey } from "../api/harnessScope.tsx";
import { harnessStatusQuery, keys } from "../api/queries.ts";
import type { Harness, HarnessStatus } from "../api/types.ts";
import { harnessStatusView, liveChip, statusRead } from "../lib/connections.ts";
import { machineKey, machineScopeId } from "../lib/machines.ts";
import { useApi } from "../platform/context.tsx";
import type { ConnectionStatus, HarnessSlots, MachineCalls, MachineConnection, PollOverride, StatusReader } from "./types.ts";

/** The generic status (GET /harnesses/{kind}/status), as a kind without its own reader reads it. */
export const GENERIC_STATUS: StatusReader<HarnessStatus> = {
  query: (m, kind) => harnessStatusQuery(m.api, m.key([...keys.harnessStatus, kind]), kind),
  view: harnessStatusView,
  presentation: (status) => status,
  rowDetail: (s) => s.reason ?? s.lastError ?? "",
};

/** The status slots of a reader. */
export function statusSlots<S>(reader: StatusReader<S>): Pick<HarnessSlots, "useStatuses" | "LiveChip" | "Presentation"> {
  return {
    Presentation: function Presentation({ kind, enabled, children }) {
      const query = useScopedStatus(reader, kind, enabled);
      return children(query.data === undefined ? undefined : reader.presentation(query.data));
    },
    useStatuses: <C extends MachineConnection>(connections: readonly C[], poll: PollOverride, name: (kind: Harness) => string): ConnectionStatus<C>[] => {
      const api = useApi();
      const qc = useQueryClient();
      const on = (m: MachineConnection["machine"]): MachineCalls => {
        const id = machineScopeId(m);
        return { api: id === null ? api : api.forMachine(id), key: (k: readonly unknown[]) => machineKey(id, k) };
      };
      const queries = useQueries({ queries: connections.map((c) => ({ ...reader.query(on(c.machine), c.harness, qc), ...poll })) });
      return connections.flatMap((connection, i) => {
        const query = queries[i];
        return query === undefined ? [] : [{ connection, view: reader.view(statusRead(query), connection.harness, name(connection.harness)) }];
      });
    },
    LiveChip: function LiveChip({ kind, name, children }) {
      const read = statusRead(useScopedStatus(reader, kind));
      const view = reader.view(read, kind, name);
      return children(liveChip(view.summary, read.data === undefined ? "" : reader.rowDetail(read.data), view.detail.error));
    },
  };
}

/** One harness's scoped status query (its Setup panel and Settings row share it). */
export function useScopedStatus<S>(reader: StatusReader<S>, kind: Harness, enabled = true) {
  const api = useHarnessApi();
  const key = useScopedKey();
  const qc = useQueryClient();
  return useQuery({ ...reader.query({ api, key }, kind, qc), enabled });
}
