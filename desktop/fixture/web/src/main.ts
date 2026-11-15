// The probe page: runs the check suite over the bridge the app was started
// with and publishes the results on window.stageFixture for e2e/.

import { runChecks, type Result } from "./checks.ts";
import { transportFor, type BridgeName, type Stream } from "./transport.ts";
import { Fixture } from "./mygo.ts";
import "./style.css";

interface FixtureState {
  bridge: BridgeName;
  done: boolean;
  results: Result[];
  env: Record<string, string | boolean>;
  /** Opens a transcript stream and resolves once it is open (e2e: close/quit cancellation). */
  holdStream(): Promise<boolean>;
  run(): Promise<Result[]>;
  /** As the window's close button: hides; the app keeps running. */
  closeWindow(): Promise<void>;
  /** Explicit Quit: the app exits, the Stage server does not. */
  quit(): Promise<void>;
}

declare global {
  interface Window {
    stageFixture?: FixtureState;
  }
}

const params = new URLSearchParams(location.search);
const bridge: BridgeName = params.get("bridge") === "fetch" ? "fetch" : "scheme";
const transport = transportFor(bridge);

function el<K extends keyof HTMLElementTagNameMap>(tag: K, text = "", cls = ""): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (text) e.textContent = text;
  if (cls) e.className = cls;
  return e;
}

const root = document.getElementById("probe");
if (!root) throw new Error("no #probe");

const header = el("header");
header.append(el("h1", "Stage fixture"), el("p", `bridge ${bridge === "fetch" ? "A, fetch plugin" : "B, scheme forwarding"} · ${location.origin}`, "meta"));
const actions = el("div", "", "actions");
const runBtn = el("button", "Run checks");
const notifyBtn = el("button", "Notify");
const downloadLink = el("a", "Download file");
const closeBtn = el("button", "Close window");
const quitBtn = el("button", "Quit");
actions.append(runBtn, notifyBtn, downloadLink, closeBtn, quitBtn);
const status = el("p", "", "status");
const table = el("table");
const tbody = el("tbody");
const head = el("thead");
const hr = el("tr");
for (const h of ["check", "result", "ms", "detail"]) hr.append(el("th", h));
head.append(hr);
table.append(head, tbody);
const envList = el("dl", "", "env");
const gallery = el("div", "", "gallery");
root.append(header, actions, status, table, el("h2", "Environment"), envList, gallery);

const env: Record<string, string | boolean> = {
  origin: location.origin,
  isSecureContext: window.isSecureContext,
  serviceWorker: "serviceWorker" in navigator,
  clipboard: "clipboard" in navigator,
  eventSource: "EventSource" in window,
  userAgent: navigator.userAgent,
};

function renderEnv() {
  envList.replaceChildren();
  for (const [k, v] of Object.entries(env)) envList.append(el("dt", k), el("dd", String(v)));
}

function addRow(r: Result) {
  const tr = el("tr", "", r.ok ? "ok" : "fail");
  tr.append(el("td", r.name), el("td", r.ok ? "pass" : "FAIL"), el("td", String(r.ms)), el("td", r.detail));
  tbody.append(tr);
}

let held: Stream | null = null;

const state: FixtureState = {
  bridge,
  done: false,
  results: [],
  env,
  async holdStream() {
    held?.close();
    const flag = { opened: false };
    held = transport.stream("/api/threads/hold/transcript/stream", {
      onOpen: () => {
        flag.opened = true;
      },
      onItem: () => {},
      onReset: () => {},
      onError: () => {},
    });
    for (let i = 0; i < 160 && !flag.opened; i++) await new Promise((r) => setTimeout(r, 50));
    return flag.opened;
  },
  async run() {
    state.done = false;
    runBtn.disabled = true;
    tbody.replaceChildren();
    gallery.replaceChildren();
    status.textContent = "running…";
    const results = await runChecks(transport, gallery, addRow);
    const failed = results.filter((r) => !r.ok).length;
    status.textContent = failed ? `${failed} of ${results.length} failed` : `all ${results.length} passed`;
    state.results = results;
    state.done = true;
    runBtn.disabled = false;
    return results;
  },
  closeWindow: () => Fixture.closeWindow(),
  quit: () => Fixture.quit(),
};
window.stageFixture = state;

runBtn.addEventListener("click", () => void state.run());
notifyBtn.addEventListener("click", () => {
  Fixture.notify("Stage fixture", "Click to bring the window back").then(
    () => (status.textContent = "notification sent"),
    (err: unknown) => (status.textContent = `notification failed: ${String(err)}`),
  );
});
// Downloads: bridge B links the API directly; bridge A fetches a blob.
downloadLink.href = bridge === "scheme" ? "/api/fixture/download" : "#";
downloadLink.download = "stage-fixture.txt";
downloadLink.addEventListener("click", (ev) => {
  if (bridge === "scheme") return;
  ev.preventDefault();
  void transport
    .request("/api/fixture/download")
    .then((res) => res.blob())
    .then((blob) => {
      const a = el("a");
      a.href = URL.createObjectURL(blob);
      a.download = "stage-fixture.txt";
      a.click();
      setTimeout(() => URL.revokeObjectURL(a.href), 1000);
    });
});
closeBtn.addEventListener("click", () => void state.closeWindow());
quitBtn.addEventListener("click", () => void state.quit());

Fixture.info().then(
  (info) => {
    env.goos = info.goos;
    env.goarch = info.goarch;
    env.mygo = info.mygo;
    env.notificationsSupported = info.notificationsSupported;
    env.pid = String(info.pid);
    renderEnv();
  },
  (err: unknown) => {
    env.goInfo = `failed: ${String(err)}`;
    renderEnv();
  },
);
renderEnv();
if (params.get("autorun") === "1") void state.run();
