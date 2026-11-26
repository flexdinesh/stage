// T3's typed setup calls over the generic harness routes (docs/api.md, Harness connections), and its status details
// read as the panel's state: an older server's missing onboarding fields are filled in.
import { ApiError, type HarnessApi } from "../../api/client.ts";
import type { HarnessStatus } from "../../api/types.ts";
import type { PairRequest, T3SettingsRequest, T3State, T3Status, T3StatusWire } from "./types.ts";

export const T3_KIND = "t3";

/** State an older server (no `state`) implies: `connected` is a valid token, else unpaired or expired. */
function legacyState(w: T3StatusWire, now: number): T3State {
  if (w.connected) return "connected";
  if (w.tokenExpiresAt !== null && Date.parse(w.tokenExpiresAt) <= now) return "expired";
  return "unpaired";
}

/** Fills the onboarding fields an older server does not send, so the UI never reads a missing object. */
export function normalizeT3Status(w: T3StatusWire, now = Date.now()): T3Status {
  return {
    ...w,
    state: w.state ?? legacyState(w, now),
    t3UrlSource: w.t3UrlSource ?? "default",
    reachable: w.reachable ?? true,
    checkedAt: w.checkedAt ?? null,
    probeError: w.probeError ?? null,
    t3: w.t3 ?? null,
    autoPair: w.autoPair ?? { available: false, reason: null },
    sync: w.sync ?? { running: false, startedAt: null, projects: 0, threads: 0 },
  };
}

/** T3's panel state from its harness status (details plus the connection a pairing replaced). */
export function t3Details(s: HarnessStatus<T3StatusWire>, now = Date.now()): T3Status {
  if (s.details === undefined) throw new ApiError(502, "T3 status without details");
  return normalizeT3Status({ ...s.details, replaced: s.replaced ?? null }, now);
}

/** T3's setup calls on a machine (`api`: that machine's harness calls). */
export const t3Api = (api: HarnessApi) => ({
  status: () => api.harnessStatus<T3StatusWire>(T3_KIND).then((s) => t3Details(s)),
  pair: (body: PairRequest) => api.runHarnessAction<T3StatusWire>(T3_KIND, "pair", { ...body }).then((s) => t3Details(s)),
  /** Pairs through the t3 CLI on the machine (409 when unavailable, 502 when it fails). */
  autoPair: () => api.runHarnessAction<T3StatusWire>(T3_KIND, "auto-pair").then((s) => t3Details(s)),
  /** Forgets the T3 token, URL, browser URL and admin token; the connection record stays, so pairing the same T3 resumes it. */
  disconnect: () => api.disconnectHarness<T3StatusWire>(T3_KIND).then((s) => t3Details(s)),
  /** 400 when a URL is invalid; 409 when paired and the URL serves another T3 environment. */
  saveSettings: (body: T3SettingsRequest) => api.configureHarness<T3StatusWire>(T3_KIND, { ...body }).then((s) => t3Details(s)),
});
