// Dev-only preview (`pnpm --filter @stage/desktop dev`, then /preview.html?state=<name>): renders the
// desktop host's connection states with a fake shell, for visual checks in a browser. Not part of the build
// (vite builds index.html only). With STAGE_DESKTOP_DEV_SOCKET set, "ready" states reach a real dev server.
// The fake shell saves three servers (this machine, "Studio", "Lab"); switching plays a short connect, Lab
// stays unreachable, and testing a `.lan` URL answers like a refused hostname.
import { StageApp } from "@stage/app";
import "@stage/app/index.css";
import type { HostServerEntry, HostState, HostTarget, Platform } from "@stage/app/platform";
import { createRoot } from "react-dom/client";
import { desktopHost } from "./platform/host.ts";
import { desktopLinks } from "./platform/links.ts";
import { INITIAL_STATE, type DesktopNative, type NativeServers } from "./platform/native.ts";
import { desktopStorage } from "./platform/storage.ts";
import { desktopTransport } from "./platform/transport.ts";

const app = { name: "Stage Dev", version: "0.9.0-12-gabc1234", identity: "stage-dev", apiVersion: 1 };
const server = {
  id: "local",
  version: "0.9.0-12-gabc1234",
  apiVersion: 1,
  identity: "stage-dev",
  publicUrl: null,
  socket: "/run/user/1000/stage-dev/stage.sock",
  managed: true,
  pid: 4242,
};
const base: HostState = { ...INITIAL_STATE, app };
const box: HostTarget = { id: "s-1a2b3c4d", name: "Studio", kind: "remote", url: "http://192.168.1.20:7380" };
const lab: HostTarget = { id: "s-5e6f7a8b", name: "Lab", kind: "remote", url: "http://10.0.0.7:7380" };
const local: HostTarget = INITIAL_STATE.target;
const remote: HostState = { ...base, target: box };
const remoteServer = { ...server, id: box.id, socket: "", managed: false, pid: null };
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
  "remote-starting": { ...remote, step: "server", title: `Connecting to ${box.name}` },
  unreachable: {
    ...remote,
    phase: "unreachable",
    title: `Can't reach ${box.name}`,
    message: `No Stage server answers at ${box.url}: dial tcp 192.168.1.20:7380: connect: connection refused`,
    fix: "Check that Stage runs on that machine and listens on an address this machine can reach (stage serve --listen 0.0.0.0:7380), then Retry.",
    epoch: 1,
  },
  "unreachable-refused": {
    ...remote,
    target: { ...box, url: "http://studio.lan:7380" },
    phase: "unreachable",
    title: `${box.name} refused the connection`,
    message: 'The Stage server at http://studio.lan:7380 refused this app: host "studio.lan:7380" not allowed: add its origin to STAGE_ALLOWED_ORIGINS (or set STAGE_PUBLIC_URL) if it is a trusted proxy',
    fix: "On that server, allow this address, restart it, then Retry:",
    command: "STAGE_ALLOWED_ORIGINS=http://studio.lan:7380",
  },
  "remote-ready": { ...remote, phase: "ready", step: null, title: "Connected", server: remoteServer, epoch: 2, serverEpoch: 1 },
  "remote-reconnecting": {
    ...remote,
    phase: "reconnecting",
    title: "Reconnecting to Stage server",
    message: "It stopped answering 6s ago.",
    server: remoteServer,
    epoch: 2,
    serverEpoch: 1,
  },
};

const name = new URLSearchParams(location.search).get("state") ?? "starting-service";
let state = STATES[name] ?? STATES["starting-service"] ?? base;
const stateListeners = new Set<(s: HostState) => void>();
const emitState = (next: HostState) => {
  state = next;
  for (const l of stateListeners) l(next);
};
const wait = () => new Promise<void>((resolve) => setTimeout(resolve, 800));
const native: DesktopNative = {
  state: () => Promise.resolve(state),
  onState: (listener) => {
    stateListeners.add(listener);
    return () => stateListeners.delete(listener);
  },
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

let entries: HostServerEntry[] = [local, box, lab].map((t) => ({
  ...t,
  active: state.target.id === t.id,
  needsInput: t.id === local.id ? 2 : t.id === box.id ? 3 : null,
}));
const serverListeners = new Set<(list: HostServerEntry[]) => void>();
const emitServers = () => {
  for (const l of serverListeners) l(entries);
};

/** Where a switch lands: this machine and Studio connect, Lab stays unreachable. */
function connected(target: HostTarget, epoch: number): HostState {
  if (target.id === lab.id) {
    return {
      ...base,
      target,
      phase: "unreachable",
      title: `Can't reach ${target.name}`,
      message: `No Stage server answers at ${target.url}: dial tcp 10.0.0.7:7380: i/o timeout`,
      fix: "Check that Stage runs on that machine and listens on an address this machine can reach (stage serve --listen 0.0.0.0:7380), then Retry.",
      epoch,
    };
  }
  const srv = target.kind === "local" ? server : { ...remoteServer, id: target.id };
  return { ...base, target, phase: "ready", step: null, title: "Stage is ready", server: srv, epoch: epoch + 1, serverEpoch: 1 };
}

const servers: NativeServers = {
  servers: () => Promise.resolve(entries),
  addServer: (input) => {
    const added: HostServerEntry = { id: `s-${entries.length}`, name: input.name, kind: "remote", url: input.url, active: false, needsInput: null };
    entries = [...entries, added];
    emitServers();
    return Promise.resolve(added);
  },
  updateServer: (id, input) => {
    entries = entries.map((e) => (e.id === id ? { ...e, ...input } : e));
    emitServers();
    const updated = entries.find((e) => e.id === id);
    return updated === undefined ? Promise.reject(new Error("no such server")) : Promise.resolve(updated);
  },
  removeServer: (id) => {
    entries = entries.filter((e) => e.id !== id);
    emitServers();
    return Promise.resolve();
  },
  switchServer: (id) => {
    const entry = entries.find((e) => e.id === id);
    if (entry === undefined) return Promise.reject(new Error("no such server"));
    if (entry.active) return Promise.resolve();
    const target: HostTarget = { id: entry.id, name: entry.name, kind: entry.kind, url: entry.url };
    const epoch = state.epoch;
    emitState({ ...base, target, step: target.kind === "remote" ? "server" : "service", title: `Connecting to ${target.name}`, epoch });
    entries = entries.map((e) => ({ ...e, active: e.id === id }));
    emitServers();
    void wait().then(() => {
      if (state.target.id === id) emitState(connected(target, epoch));
    });
    return Promise.resolve();
  },
  testServer: async (url) => {
    await wait();
    return url.includes(".lan")
      ? { reachable: true, apiVersion: 0, identity: "", version: "", error: "host not allowed", hint: "Add it to STAGE_ALLOWED_ORIGINS on that server.", command: `STAGE_ALLOWED_ORIGINS=${url}` }
      : { reachable: true, apiVersion: 1, identity: "stage", version: server.version, error: "", hint: "", command: "" };
  },
  onServers: (listener) => {
    serverListeners.add(listener);
    return () => serverListeners.delete(listener);
  },
};

const host = desktopHost(native, servers);
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
