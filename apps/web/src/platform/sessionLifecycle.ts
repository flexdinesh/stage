import type { SessionLifecycle } from "@stage/app/platform";
import { flushSync } from "react-dom";

const SESSION_CHANGED = "stage-session-changed";

export function browserSessionLifecycle(clearPrivateState: () => void): SessionLifecycle {
  const channel = typeof BroadcastChannel === "undefined" ? null : new BroadcastChannel(SESSION_CHANGED);
  return {
    clearPrivateState,
    changed() {
      if (channel !== null) {
        // BroadcastChannel is bound to this origin and takes no targetOrigin argument.
        // eslint-disable-next-line unicorn/require-post-message-target-origin
        channel.postMessage(null);
      } else {
        try {
          localStorage.setItem(SESSION_CHANGED, crypto.randomUUID());
        } catch {
          // Focus and periodic rechecks still detect the change.
        }
      }
    },
    subscribe(listener) {
      const changed = () => listener("changed");
      const storage = (e: StorageEvent) => {
        if (e.key === SESSION_CHANGED) changed();
      };
      const resume = () => {
        if (document.visibilityState === "visible") listener("resume");
      };
      // Drop private DOM and form values before the browser snapshots this page for Back/Forward.
      const suspend = (e: PageTransitionEvent) => {
        if (e.persisted) flushSync(() => listener("suspend"));
      };
      channel?.addEventListener("message", changed);
      window.addEventListener("storage", storage);
      window.addEventListener("pagehide", suspend);
      window.addEventListener("pageshow", resume);
      window.addEventListener("focus", resume);
      document.addEventListener("visibilitychange", resume);
      return () => {
        channel?.removeEventListener("message", changed);
        window.removeEventListener("storage", storage);
        window.removeEventListener("pagehide", suspend);
        window.removeEventListener("pageshow", resume);
        window.removeEventListener("focus", resume);
        document.removeEventListener("visibilitychange", resume);
      };
    },
  };
}
