// Codex's status query on a machine: the generic status with its details parsed.
import { queryOptions } from "@tanstack/react-query";
import { useScopedStatus } from "../status.ts";
import type { StatusReader } from "../types.ts";
import { CODEX_KIND, parseCodexStatus } from "./codex.ts";
import { codexRowDetail, codexStatusView } from "./status.ts";
import type { CodexStatus } from "./types.ts";

export const CODEX_STATUS_KEY: readonly unknown[] = ["codex-status"];

export const CODEX_STATUS: StatusReader<CodexStatus> = {
  query: (m) => queryOptions({ queryKey: m.key(CODEX_STATUS_KEY), queryFn: () => m.api.harnessStatus(CODEX_KIND).then(parseCodexStatus), refetchInterval: 15000 }),
  presentation: (status) => status,
  view: (read) => codexStatusView(read),
  rowDetail: (s) => codexRowDetail(s),
};

/** Codex's setup status on the harness scope's machine. */
export const useCodexStatus = () => useScopedStatus(CODEX_STATUS, CODEX_KIND);
