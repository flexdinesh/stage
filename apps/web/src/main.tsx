// Browser host: the shared app (@stage/app) over same-origin /api, with Web Push via the service worker.
import { StageApp, createApi } from "@stage/app";
import "@stage/app/index.css";
import type { Platform } from "@stage/app/platform";
import { createRoot } from "react-dom/client";
import { browserAttention } from "./platform/attention.ts";
import { browserClipboard } from "./platform/clipboard.ts";
import { browserHistoryControls } from "./platform/historyControls.ts";
import { browserLinks } from "./platform/links.ts";
import { browserStorage } from "./platform/storage.ts";
import { browserTransport } from "./platform/transport.ts";
import { webPushNotifications } from "./platform/webPush.ts";

// Service worker (Web Push only, no caching). Built only for production; /sw.js does not exist in dev.
if (import.meta.env.PROD && "serviceWorker" in navigator) {
  navigator.serviceWorker.register("/sw.js", { scope: "/" }).catch((err: unknown) => {
    console.error("service worker registration failed", err);
  });
}

const transport = browserTransport();
const platform: Platform = {
  transport,
  clipboard: browserClipboard(),
  links: browserLinks(),
  notifications: webPushNotifications(createApi(transport)),
  attention: browserAttention(),
  storage: browserStorage(),
  hostHistoryControls: browserHistoryControls(),
};

const root = document.getElementById("root");
if (root === null) throw new Error("#root element missing");

createRoot(root).render(<StageApp platform={platform} />);
