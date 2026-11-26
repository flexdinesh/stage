// Codex's own status details (GET /api/harnesses/codex/status `details`; docs/api.md).
import type { HarnessStatus } from "../../api/types.ts";

/** Codex's harness state words (HarnessStatus.state). */
export type CodexState = "connected" | "not_connected" | "signed_out" | "unreachable" | "not_installed";
/** How the Codex CLI is signed in. */
export interface CodexAccount { type: "chatgpt" | "apiKey" | "amazonBedrock" | "gcp"; email: string | null; plan: string | null }
/** Codex's own status details (only its panel and summaries read them). */
export interface CodexDetails {
  /** The configured CLI path; "" uses codex on PATH. */
  binary: string;
  /** "" when unknown. */
  cliVersion: string;
  appServerVersion: string;
  codexHome: string;
  daemon: "running" | "stopped" | "unknown";
  account: CodexAccount | null;
  /** A shell command that fixes the current problem ("codex login"); "" none. */
  fix: string;
  checkedAt: string | null;
}
/** Codex's harness status with its details parsed (codex.ts); null details: missing or malformed. */
export type CodexStatus = Omit<HarnessStatus, "details"> & { details: CodexDetails | null };
