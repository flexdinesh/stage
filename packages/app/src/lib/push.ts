import type { NotificationDelivery, PushState } from "../platform/index.ts";

export type { PushState };

/** README section on serving stage over HTTPS (needed for push on other devices). */
export const HTTPS_DOCS_URL = "https://github.com/flexdinesh/stage#https-and-push-notifications";

/** Notifications drawer footer for a push state; null when push is on (or not yet known). */
export function pushFooterNote(state: PushState | undefined, channel?: NotificationDelivery["channel"]): string | null {
  if (state === undefined || state === "on") return null;
  if (channel === "native") return "Notifications are off on this device";
  return state === "insecure" ? "Push needs HTTPS or localhost" : "Push is off on this device";
}
