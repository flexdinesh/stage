import type { NotificationDelivery, PushState } from "@stage/app/platform";
import type { NativeNotifications } from "./native.ts";

/** The Go side's state name as the app's delivery state; unknown names read as unsupported. */
export function toPushState(state: string): PushState {
  switch (state) {
    case "off":
    case "on":
    case "blocked":
      return state;
    default:
      return "unsupported";
  }
}

/**
 * Native system notifications: the resident Go process reads the server's notification stream and shows them
 * (docs/desktop.md#notifications), also while the window is closed. The page only controls and syncs them.
 */
export function desktopNotifications(native: NativeNotifications): NotificationDelivery {
  return {
    channel: "native",
    state: async () => toPushState(await native.notificationState()),
    enable: () => native.enableNotifications(),
    disable: () => native.disableNotifications(),
    test: () => native.testNotification(),
    closeShown: (tag) => native.closeNotifications(tag ?? "").catch(() => {}),
    syncShown: () => native.syncNotifications().catch(() => {}),
    onDelivered: (listener) => native.onNotificationDelivered(() => listener()),
    openSystemSettings: () => native.openNotificationSettings(),
  };
}
