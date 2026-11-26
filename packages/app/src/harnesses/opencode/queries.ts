// OpenCode's status query on a machine.
import { queryOptions } from "@tanstack/react-query";
import { useScopedStatus } from "../status.ts";
import type { StatusReader } from "../types.ts";
import { OPENCODE_KIND, openCodeApi } from "./api.ts";
import { openCodeRowDetail, openCodeStatusView } from "./status.ts";
import type { OpenCodeStatus } from "./types.ts";

export const OPENCODE_STATUS_KEY: readonly unknown[] = ["opencode-status"];

export const OPENCODE_STATUS: StatusReader<OpenCodeStatus> = {
  query: (m) => queryOptions({ queryKey: m.key(OPENCODE_STATUS_KEY), queryFn: openCodeApi(m.api).status, refetchInterval: 15000 }),
  presentation: (status) => status.presentation,
  view: (read) => openCodeStatusView(read),
  rowDetail: (s) => openCodeRowDetail(s),
};

/** OpenCode's setup status on the harness scope's machine. */
export const useOpenCodeStatus = () => useScopedStatus(OPENCODE_STATUS, OPENCODE_KIND);
