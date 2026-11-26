// Codex connection status (Setup.tsx, its Settings row and status reader): the generic harness status
// with Codex's details read structurally, so a malformed or missing details object reads as none instead of
// breaking the panel. Absent strings read as "" and absent nullable fields as null (the server may omit empties).
import type { HarnessStatus } from "../../api/types.ts";
import type { CodexAccount, CodexDetails, CodexState, CodexStatus } from "./types.ts";

export const CODEX_KIND = "codex";

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);

/** A string field: absent reads "", any other type is malformed (null). */
const text = (v: unknown): string | null => (v === undefined ? "" : typeof v === "string" ? v : null);

/** A nullable string field: absent reads null; `undefined` marks it malformed. */
const textOrNull = (v: unknown): string | null | undefined => (v === undefined || v === null ? null : typeof v === "string" ? v : undefined);

function accountType(v: unknown): CodexAccount["type"] | null {
  switch (v) {
    case "chatgpt":
    case "apiKey":
    case "amazonBedrock":
    case "gcp":
      return v;
    default:
      return null;
  }
}

function daemonState(v: unknown): CodexDetails["daemon"] | null {
  switch (v) {
    case "running":
    case "stopped":
    case "unknown":
      return v;
    case undefined:
      return "unknown";
    default:
      return null;
  }
}

/** The account, null when signed out; `undefined` when malformed. */
function account(v: unknown): CodexAccount | null | undefined {
  if (v === undefined || v === null) return null;
  if (!isRecord(v)) return undefined;
  const type = accountType(v.type);
  const email = textOrNull(v.email);
  const plan = textOrNull(v.plan);
  if (type === null || email === undefined || plan === undefined) return undefined;
  return { type, email, plan };
}

/** Codex's details from a status payload; null when absent or not the expected shape. */
export function codexDetails(v: unknown): CodexDetails | null {
  if (!isRecord(v)) return null;
  const binary = text(v.binary);
  const cliVersion = text(v.cliVersion);
  const appServerVersion = text(v.appServerVersion);
  const codexHome = text(v.codexHome);
  const fix = text(v.fix);
  const daemon = daemonState(v.daemon);
  const acct = account(v.account);
  const checkedAt = textOrNull(v.checkedAt);
  if (binary === null || cliVersion === null || appServerVersion === null || codexHome === null || fix === null || daemon === null || acct === undefined || checkedAt === undefined) {
    return null;
  }
  return { binary, cliVersion, appServerVersion, codexHome, daemon, account: acct, fix, checkedAt };
}

/** A Codex harness status with its details parsed. */
export function parseCodexStatus(s: HarnessStatus): CodexStatus {
  const { details, ...rest } = s;
  return { ...rest, details: codexDetails(details) };
}

/** Codex's state word, or null for one this build does not know (it then reads as a generic harness). */
export function codexState(state: string): CodexState | null {
  switch (state) {
    case "connected":
    case "not_connected":
    case "signed_out":
    case "unreachable":
    case "not_installed":
      return state;
    default:
      return null;
  }
}

/** "codex-cli 0.80.0 · app-server 0.80.0", empty parts dropped; "" when neither is known. */
export function codexVersionText(d: Pick<CodexDetails, "cliVersion" | "appServerVersion">): string {
  return [d.cliVersion === "" ? null : `codex-cli ${d.cliVersion}`, d.appServerVersion === "" ? null : `app-server ${d.appServerVersion}`]
    .filter((p) => p !== null)
    .join(" · ");
}

const PROVIDER: Record<CodexAccount["type"], string> = {
  chatgpt: "ChatGPT",
  apiKey: "API key",
  amazonBedrock: "Amazon Bedrock",
  gcp: "Google Cloud",
};

/** The panel's account line: "dee@example.com · plus" for ChatGPT, else the provider; "Not signed in" when signed out; null when unknown. */
export function codexAccountText(a: CodexAccount | null, state: string): string | null {
  if (a === null) return state === "signed_out" ? "Not signed in" : null;
  if (a.type !== "chatgpt") return PROVIDER[a.type];
  const parts = [a.email, a.plan].filter((p) => p !== null && p !== "");
  return parts.length === 0 ? PROVIDER.chatgpt : parts.join(" · ");
}

/** The one-line account in a row: the ChatGPT email (else "ChatGPT"), or the provider; null when signed out. */
export function codexAccountShort(a: CodexAccount | null): string | null {
  if (a === null) return null;
  if (a.type === "chatgpt") return a.email === null || a.email === "" ? PROVIDER.chatgpt : a.email;
  return PROVIDER[a.type];
}

/** "daemon running" / "daemon stopped"; null when unknown. */
export function codexDaemonText(daemon: CodexDetails["daemon"]): string | null {
  return daemon === "unknown" ? null : `daemon ${daemon}`;
}
