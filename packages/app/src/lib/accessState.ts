import { ApiError, type Api } from "../api/client.ts";
import type { AccessStatus, Account } from "../api/types.ts";
import type { Platform } from "../platform/index.ts";
import { createSharedSession } from "./sharedSession.ts";

type Session = ReturnType<typeof createSharedSession>;
export type Access =
  | { kind: "checking" | "personal" | "setup_pending" | "signed_out" }
  | { kind: "error"; error: unknown }
  | { kind: "signed_in"; account: Account; session: Session; generation: number };

export const CHECKING: Access = { kind: "checking" };
const RECHECK_MS = 30_000;

async function readAccessStatus(api: Api): Promise<AccessStatus> {
  try {
    return await api.accessStatus();
  } catch (error) {
    // An older compatible personal server can still be selected by a native host.
    if (!(error instanceof ApiError) || error.status !== 404) throw error;
    const health = await api.health();
    if (health.service !== "stage" || health.status !== "ok" || health.sharedAccess === true ||
        typeof health.instanceId !== "string" || health.instanceId === "") throw error;
    return { mode: "personal", installationId: health.instanceId };
  }
}

/** Observe the installation and human account outside the protected query client. */
export function observeAccess(api: Api, platform: Platform, render: (view: Access, previous: Access) => void) {
  let view: Access = CHECKING;
  let installationId = "";
  let stopped = false;
  let suspended = false;
  let pending = false;
  let generation = 0;
  let revision = 0;

  const show = (next: Access) => {
    const previous = view;
    if (view.kind === "signed_in" && !(next.kind === "signed_in" && next.session === view.session)) view.session.close();
    view = next;
    render(next, previous);
  };

  async function check(discard = false) {
    if (stopped || suspended || (!discard && pending)) return;
    if (discard) show(CHECKING);
    const ticket = ++revision;
    const current = () => !stopped && ticket === revision;
    pending = true;
    try {
      const access = await readAccessStatus(api);
      if (!current()) return;
      if (access.mode === "personal" || access.mode === "setup_pending") {
        if (view.kind !== access.mode) show({ kind: access.mode });
        return;
      }
      if (access.mode !== "claimed") {
        show({ kind: "error", error: new Error("Stage returned an unknown access mode.") });
        return;
      }
      const account = await api.account();
      if (!current()) return;
      if (account.status !== "active" || typeof account.email !== "string" || !["admin", "member"].includes(account.role)) {
        show({ kind: "error", error: new Error("Stage could not verify this account.") });
        return;
      }
      if (view.kind === "signed_in" && access.installationId === installationId && view.account.id === account.id &&
          view.account.role === account.role) {
        if (view.account.displayName !== account.displayName || view.account.email !== account.email) show({ ...view, account });
        return;
      }
      installationId = access.installationId;
      const session = createSharedSession(platform, () => {
        if (view.kind === "signed_in" && view.session === session) void check(true);
      }, () => void check());
      show({ kind: "signed_in", account, session, generation: ++generation });
    } catch (error) {
      if (!current()) return;
      if (error instanceof ApiError && error.code === "authentication_required") {
        if (view.kind !== "signed_out") show({ kind: "signed_out" });
      } else if (view.kind !== "signed_in") {
        show({ kind: "error", error });
      }
    } finally {
      if (current()) pending = false;
    }
  }

  const unsubscribe = platform.sessionLifecycle?.subscribe((event) => {
    if (view.kind === "personal" && event !== "resume") return;
    if (event === "suspend") {
      suspended = true;
      revision++;
      pending = false;
      show(CHECKING);
      return;
    }
    if (event === "resume") suspended = false;
    void check(event === "changed");
  });
  const timer = setInterval(() => {
    if (view.kind !== "personal") void check();
  }, RECHECK_MS);
  void check();
  return {
    check,
    close() {
      stopped = true;
      unsubscribe?.();
      clearInterval(timer);
      if (view.kind === "signed_in") view.session.close();
    },
  };
}
