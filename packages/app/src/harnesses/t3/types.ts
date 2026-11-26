// T3's own status details and setup bodies (GET /api/harnesses/t3/status `details`; docs/api.md).
import type { HarnessStatus, ReplacedConnection } from "../../api/types.ts";

/** Precedence: unreachable > unpaired > expired > rejected > connected. */
export type T3State = "unreachable" | "unpaired" | "expired" | "rejected" | "connected";

/** Where the T3 URL comes from: saved in Settings, or the default. */
export type T3UrlSource = "settings" | "default";

/** Why the server cannot pair through the local t3 CLI. */
export type AutoPairReason = "not_local" | "cli_not_found" | "fake";

/** The T3 server that answered the last probe. */
export interface T3Identity {
  environmentId: string;
  label: string;
  serverVersion: string;
}

/** Background full sync; counts tick while it runs. */
export interface T3SyncProgress {
  running: boolean;
  startedAt: string | null;
  projects: number;
  threads: number;
}

/** The details of GET /api/harnesses/t3/status (also of its settings, actions and disconnect replies). */
export interface T3Status {
  /** Holds a valid token (unchanged meaning). */
  connected: boolean;
  state: T3State;
  t3Url: string;
  /** The saved Browser URL, else the T3 URL. */
  publicUrl: string;
  t3UrlSource: T3UrlSource;
  environmentId: string | null;
  clientId: string | null;
  tokenExpiresAt: string | null;
  /** An admin token is saved in Settings (Stage renews its token itself). */
  adminTokenConfigured: boolean;
  lastError: string | null;
  lastFullSyncAt: string | null;
  /** Last probe of the T3 URL answered. */
  reachable: boolean;
  checkedAt: string | null;
  probeError: string | null;
  t3: T3Identity | null;
  autoPair: { available: boolean; reason: AutoPairReason | null };
  sync: T3SyncProgress;
  /** Pair and auto-pair only: the previous T3 connection this pairing replaced. */
  replaced?: ReplacedConnection | null;
  presentation?: HarnessStatus;
}

/** T3Status as an older server may send it: fields added with onboarding can be missing. */
export type T3StatusWire = Omit<
  T3Status,
  "state" | "t3UrlSource" | "reachable" | "checkedAt" | "probeError" | "t3" | "autoPair" | "sync"
> &
  Partial<Pick<T3Status, "state" | "t3UrlSource" | "reachable" | "checkedAt" | "probeError" | "t3" | "autoPair" | "sync">>;

/** PUT T3 settings: at least one field; a string sets it, null clears it (url: the default, publicUrl: the T3 URL, adminToken: none). */
export interface T3SettingsRequest {
  url?: string | null;
  /** Browser URL for Open in T3 links. */
  publicUrl?: string | null;
  /** Lets Stage renew its token itself. */
  adminToken?: string | null;
}

export interface PairRequest {
  code: string;
}
