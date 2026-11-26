// OpenCode's typed setup calls over the generic harness routes (docs/api.md, Harness connections), and its status
// details read as the card's state: an older server's missing pairing fields are filled in.
import { ApiError, type HarnessApi } from "../../api/client.ts";
import type { HarnessStatus } from "../../api/types.ts";
import type { OpenCodeStatus, OpenCodeStatusWire } from "./types.ts";

export const OPENCODE_KIND = "opencode";

export type OpenCodeSettings = Partial<Record<"endpoint" | "browserEndpoint" | "webClient" | "username" | "password", string>> & {
  /** Connect a different OpenCode service as a new connection, retiring the current one. */
  newInstance?: boolean;
};

/** Fills the pairing fields an older server does not send: Basic credentials when it has them, and no local service. */
export function normalizeOpenCodeStatus(w: OpenCodeStatusWire): OpenCodeStatus {
  return {
    ...w,
    auth: w.auth ?? (w.hasCredentials ? "basic" : "none"),
    local: w.local ?? { available: false, url: "", version: "", reason: "" },
  };
}

/** OpenCode's panel state from its harness status. */
export function openCodeDetails(s: HarnessStatus<OpenCodeStatusWire>): OpenCodeStatus {
  if (s.details === undefined) throw new ApiError(502, "OpenCode status without details");
  return normalizeOpenCodeStatus(s.details);
}

const done = (): void => undefined;

/** OpenCode's setup calls on a machine (`api`: that machine's harness calls); setting and action replies are read from the status. */
export const openCodeApi = (api: HarnessApi) => ({
  status: () => api.harnessStatus<OpenCodeStatusWire>(OPENCODE_KIND).then(openCodeDetails),
  /** 409 `opencode_auth_required` when the service demands a password; 409 code instance_changed when the address is another OpenCode service (instance_returned: a replaced one); resend with newInstance to connect it. */
  save: (body: OpenCodeSettings) => api.configureHarness(OPENCODE_KIND, { ...body }).then(done),
  /** Connects the OpenCode service on the machine with its own password (409 `opencode_local_unavailable`, or instance_changed / instance_returned as for save). */
  connectLocal: (newInstance = false) => api.runHarnessAction(OPENCODE_KIND, "connect-local", newInstance ? { newInstance: true } : undefined).then(done),
  /** Redeems a link from `opencode pair` (400 when invalid, 409 `opencode_pairing_expired`, or instance_changed / instance_returned: resend the same link with newInstance). */
  pair: (link: string, newInstance = false) => api.runHarnessAction(OPENCODE_KIND, "pair", newInstance ? { link, newInstance: true } : { link }).then(done),
  /** Forgets the OpenCode connection; tasks and history stay. */
  disconnect: () => api.disconnectHarness(OPENCODE_KIND).then(done),
});
