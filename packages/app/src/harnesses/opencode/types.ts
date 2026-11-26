// OpenCode's own status details (GET /api/harnesses/opencode/status `details`; docs/api.md).

export interface OpenCodeStatus {
  connectionId: number;
  endpoint: string;
  browserEndpoint: string;
  webClient: string;
  hasCredentials: boolean;
  /** `rejected`: OpenCode answered but refused Stage's sign-in (pairing expired or password changed). */
  state: "unconfigured" | "connected" | "unavailable" | "rejected";
  reason?: string;
  checkedAt?: string;
  /** How Stage signs in: the local service's own password, a pairing session, Basic credentials, or none. */
  auth: OpenCodeAuth;
  /** Pairing session expiry (RFC3339); paired only. */
  expiresAt?: string;
  /** The OpenCode service discovered on the Stage server's machine (one-click Connect). */
  local: OpenCodeLocal;
}
/** Status as sent; servers before OpenCode pairing omit `auth` and `local` (see normalizeOpenCodeStatus). */
export type OpenCodeStatusWire = Omit<OpenCodeStatus, "auth" | "local"> & Partial<Pick<OpenCodeStatus, "auth" | "local">>;
export type OpenCodeAuth = "none" | "local" | "paired" | "basic";
export interface OpenCodeLocal {
  available: boolean;
  url: string;
  version: string;
  reason: "" | "missing" | "invalid" | "unreachable";
}
