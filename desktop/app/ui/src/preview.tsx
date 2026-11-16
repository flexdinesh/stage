// Dev-only preview (`pnpm --filter stage-desktop dev`, then /preview.html?state=<name>): renders the
// desktop host's connection states with a fake shell, for visual checks in a browser. Not part of the build
// (vite builds index.html only). With STAGE_DESKTOP_DEV_SOCKET set, "ready" states reach a real dev server.
import { StageApp } from "@stage/app";
import "@stage/app/index.css";
import type { HostState, Platform } from "@stage/app/platform";
import { createRoot } from "react-dom/client";
import { desktopHost } from "./platform/host.ts";
import { desktopLinks } from "./platform/links.ts";
import { INITIAL_STATE, type DesktopNative } from "./platform/native.ts";
import { desktopStorage } from "./platform/storage.ts";
import { desktopTransport } from "./platform/transport.ts";

const app = { name: "Stage Dev", version: "0.9.0-12-gabc1234", identity: "stage-dev", apiVersion: 1 };
const server = {
  version: "0.9.0-12-gabc1234",
  apiVersion: 1,
  identity: "stage-dev",
  publicUrl: null,
  socket: "/run/user/1000/stage-dev/stage.sock",
  managed: true,
  pid: 4242,
};
const base: HostState = { ...INITIAL_STATE, app };
const log = [
  "level=INFO msg=\"listening\" addr=unix:///run/user/1000/stage-dev/stage.sock",
  "level=ERROR msg=\"open database\" err=\"disk I/O error\"",
  "level=ERROR msg=\"stage serve: exit 1\"",
];

const STATES: Record<string, HostState> = {
  "starting-service": { ...base, step: "service", title: "Starting Stage", message: "Starting the Stage service" },
  "starting-api": { ...base, step: "api", title: "Starting Stage", message: "Checking the Stage server" },
  failed: {
    ...base,
    phase: "failed",
    title: "Stage server failed to start",
    message: "The service exited with status 1.",
    fix: "Check the log below; `stage service logs` shows more.",
    logTail: log,
  },
  stopped: { ...base, phase: "stopped", title: "Stage server is not running", message: "The per-user service is stopped." },
  unsupported: {
    ...base,
    phase: "unsupported",
    title: "No per-user service manager",
    message: "systemd --user is not reachable in this session.",
    fix: "Log in through a session with a systemd user manager, or run the server yourself.",
    command: "stage serve --listen unix",
  },
  misconfigured: {
    ...base,
    phase: "misconfigured",
    title: "Stage can't start its server",
    message: "The service configuration is invalid.",
    problems: [
      { code: "service_env_invalid", message: "service.env is readable by other users", fix: "chmod 600 ~/.config/stage-dev/service.env" },
      { code: "definition_foreign", message: "stage-dev.service was not written by Stage", fix: "Remove or rename the unit, then retry" },
    ],
  },
  location: {
    ...base,
    phase: "misconfigured",
    title: "Move Stage to a permanent folder",
    message: "Stage is running from a temporary location (/tmp/Stage Dev).",
    fix: "Move the app to Applications (macOS) or install it, then open it again.",
  },
  conflict: {
    ...base,
    phase: "conflict",
    title: "Another Stage server is using your data",
    message: "A server started from a terminal (pid 31337) holds the data directory and isn't listening on the socket.",
    fix: "Stop it, or restart it with a socket listener.",
    command: "stage serve --listen unix,127.0.0.1:7380",
  },
  incompatible: {
    ...base,
    phase: "incompatible",
    server: { ...server, version: "0.7.0", apiVersion: 0 },
    title: "Stage server speaks another API",
    message: "Server API v0, this app needs v1.",
    fix: "Restart the server onto the app's version.",
  },
  ready: { ...base, phase: "ready", step: null, title: "Stage is ready", server, epoch: 1 },
  "ready-skew": { ...base, phase: "ready", title: "Stage is ready", server: { ...server, version: "0.8.2" }, versionSkew: true, epoch: 1 },
  "ready-foreground": { ...base, phase: "ready", title: "Stage is ready", server: { ...server, managed: false, pid: 31337 }, foreground: true, epoch: 1 },
  reconnecting: { ...base, phase: "reconnecting", title: "Reconnecting to the Stage server", message: "It stopped answering 6s ago.", server, epoch: 1 },
  "stopped-after": { ...base, phase: "stopped", title: "Stage server stopped", message: "It has not answered for 30s.", server, epoch: 1 },
};

const name = new URLSearchParams(location.search).get("state") ?? "starting-service";
const state = STATES[name] ?? STATES["starting-service"] ?? base;
const wait = () => new Promise<void>((resolve) => setTimeout(resolve, 800));
const native: DesktopNative = {
  state: () => Promise.resolve(state),
  onState: () => () => {},
  onNavigate: () => () => {},
  takeNavigation: () => Promise.resolve(""),
  retry: wait,
  startServer: wait,
  restartServer: wait,
  diagnostics: () => Promise.resolve(`Stage Dev ${app.version} (stage-dev)\nserver: ${state.server?.version ?? "-"}\nphase: ${state.phase}\n`),
  copyText: (text) => navigator.clipboard.writeText(text),
  openExternal: (url) => Promise.resolve(void window.open(url, "_blank", "noopener")),
  openApp: () => Promise.resolve(),
  loginStartup: () => Promise.resolve(false),
  setLoginStartup: () => Promise.resolve(),
};

const host = desktopHost(native);
const platform: Platform = {
  transport: desktopTransport(),
  clipboard: { writeText: (text) => native.copyText(text) },
  links: desktopLinks(native, host.current),
  attention: { setNeedsInput: () => {} },
  storage: desktopStorage(),
  host,
};

const root = document.getElementById("root");
if (root === null) throw new Error("#root element missing");
createRoot(root).render(<StageApp platform={platform} />);
