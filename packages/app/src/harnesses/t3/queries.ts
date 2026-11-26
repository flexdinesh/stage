// T3's status query: polled faster while a sync runs; the first full sync refreshes the views it fills.
import { queryOptions, type QueryClient } from "@tanstack/react-query";
import type { HarnessApi } from "../../api/client.ts";
import { refreshAfterFirstSync } from "../../api/queries.ts";
import { useScopedStatus } from "../status.ts";
import type { StatusReader } from "../types.ts";
import { T3_KIND, t3Api } from "./api.ts";
import { firstSyncLanded } from "./onboarding.ts";
import { t3RowDetail, t3StatusView } from "./status.ts";
import type { T3Status } from "./types.ts";

export const T3_STATUS_KEY: readonly unknown[] = ["t3Status"];

const T3_STATUS_POLL_MS = 5000;
const T3_SYNC_POLL_MS = 1000;

/** T3's status on a machine (`api`, `key`: that machine's calls and scoped key). */
function t3StatusQuery(api: HarnessApi, key: readonly unknown[], qc: QueryClient) {
  return queryOptions({
    queryKey: key,
    // Compared here (once per fetch, however many observers) so the first sync refreshes the views it fills.
    queryFn: async () => {
      const prev = qc.getQueryData<T3Status>(key);
      const next = await t3Api(api).status();
      if (firstSyncLanded(prev, next)) void refreshAfterFirstSync(qc);
      return next;
    },
    refetchInterval: (q) => (q.state.data?.sync.running === true ? T3_SYNC_POLL_MS : T3_STATUS_POLL_MS),
  });
}

export const T3_STATUS: StatusReader<T3Status> = {
  query: (m, _kind, qc) => t3StatusQuery(m.api, m.key(T3_STATUS_KEY), qc),
  view: (read) => t3StatusView(read),
  rowDetail: (s) => t3RowDetail(s),
};

/** T3's setup status on the harness scope's machine. */
export const useT3Status = () => useScopedStatus(T3_STATUS, T3_KIND);
