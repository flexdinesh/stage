import assert from "node:assert/strict";
import { test } from "node:test";
import { ApiError } from "../src/api/client.ts";
import {
  activeOn,
  forgetPrompt,
  harnessLookup,
  isDisconnected,
  isRetired,
  linkSchemes,
  retiredNotice,
  sessionLinks,
  resumeCommand,
  sessionAction,
  folderName,
  usageText,
} from "../src/lib/harnesses.ts";
import { canStart, canStop } from "../src/lib/tasks.ts";
import { openCodeApi } from "../src/harnesses/opencode/api.ts";
import { isInstanceChanged, isInstanceReturned, newServicePrompt, returnedServicePrompt } from "../src/harnesses/opencode/opencode.ts";
import { replacedNotice } from "../src/harnesses/t3/onboarding.ts";
import { fakeApi } from "./fake-transport.mjs";

const connection = (id, harness, extra = {}) => ({
  id,
  harness,
  configured: true,
  autoComplete: true,
  active: true,
  label: "",
  retiredAt: null,
  forgottenAt: null,
  tasks: 0,
  bindings: 0,
  ...extra,
});

// Ids are not fixed: T3 is 5 now, the first T3 (id 1) was replaced.
const t3 = connection(5, "t3", { label: "laptop" });
const openCode = connection(2, "opencode", { label: "http://oc:4096" });
const oldT3 = connection(1, "t3", { active: false, label: "desktop", retiredAt: "2026-11-10T00:00:00Z", tasks: 4, bindings: 2 });
const forgotten = connection(3, "t3", { active: false, label: "vm", retiredAt: "2026-11-01T00:00:00Z", forgottenAt: "2026-11-02T00:00:00Z" });
const list = [t3, openCode, oldT3, forgotten];
// Names come from the server's descriptors (GET /harnesses).
const descriptor = (kind, name) => ({ kind, name, localProcesses: false, sessions: "machine", stateReasons: [], closures: [], available: true });
const descriptors = [descriptor("t3", "T3"), descriptor("opencode", "OpenCode")];

test("useHarnesses lookups name and branch by harness, never by id", () => {
  const h = harnessLookup(list, descriptors);
  assert.equal(h.name(5), "T3");
  assert.equal(h.name(1), "T3");
  assert.equal(h.name(2), "OpenCode");
  assert.equal(h.name(99), "Harness");
  assert.equal(h.kindName("opencode"), "OpenCode");
  assert.equal(h.descriptor("t3")?.name, "T3");
  // Before descriptors load (or for a kind this build does not know) a kind reads as itself.
  assert.equal(harnessLookup(list).name(2), "opencode");
  assert.equal(h.kindName("claude"), "claude");
  assert.equal(h.harness(1), "t3");
  assert.equal(h.harness(2), "opencode");
  assert.equal(h.harness(99), null);
  assert.equal(h.active(5), true);
  assert.equal(h.active(1), false);
  assert.equal(h.active(99), false);
  assert.equal(h.retired(1), true);
  assert.equal(h.retired(5), false);
  assert.equal(h.retired(99), false, "an unknown id is not retired");
  assert.equal(h.label(1), "desktop");
  assert.equal(h.activeId("t3"), 5);
  assert.equal(h.activeId("opencode"), 2);
  assert.equal(h.activeConnection("t3"), t3);
  assert.deepEqual(h.activeList.map((c) => c.id), [5, 2]);
});

test("connectedList holds active, set-up connections only; unconfigured flags active ones not set up", () => {
  const down = connection(7, "t3", { configured: false, label: "box", tasks: 3 });
  const h = harnessLookup([t3, down, openCode, oldT3]);
  assert.deepEqual(h.connectedList.map((c) => c.id), [5, 2]);
  assert.deepEqual(h.activeList.map((c) => c.id), [5, 7, 2]);
  assert.equal(h.unconfigured(7), true);
  assert.equal(h.unconfigured(5), false);
  assert.equal(h.unconfigured(1), false, "a retired connection is not unconfigured");
  assert.equal(h.unconfigured(99), false);
});

test("disconnected: active, not set up, and set up once or holding history; a never-set-up placeholder is not", () => {
  assert.equal(isDisconnected(connection(1, "t3", { configured: false, label: "box" })), true);
  assert.equal(isDisconnected(connection(1, "t3", { configured: false, tasks: 2 })), true);
  assert.equal(isDisconnected(connection(1, "t3", { configured: false, bindings: 1 })), true);
  assert.equal(isDisconnected(connection(2, "opencode", { configured: false })), false, "placeholder");
  assert.equal(isDisconnected(connection(1, "t3", { label: "box" })), false, "connected");
  assert.equal(isDisconnected({ ...oldT3, configured: false }), false, "retired");
});

test("activeId is null when a harness has no active connection", () => {
  const h = harnessLookup([oldT3, openCode]);
  assert.equal(h.activeId("t3"), null);
  assert.equal(h.activeConnection("t3"), undefined);
});

const task = (extra = {}) => ({
  id: 9,
  harness: "t3",
  connectionId: 1,
  connectionActive: false,
  connectionLabel: "desktop",
  acceptanceId: null,
  adopted: false,
  readiness: null,
  treeStatus: null,
  state: "failed",
  externalSessionId: "thread-9",
  links: { native: "t3code://threads/env/thread-9", web: "http://t3/thread-9" },
  ...extra,
});

test("a task on a replaced connection is retired: no Start, Retry or Stop", () => {
  assert.equal(isRetired(task()), true);
  assert.equal(isRetired(task({ connectionActive: true })), false);
  assert.equal(canStart(task()), false);
  assert.equal(canStart(task({ connectionActive: true })), true);
  assert.equal(canStop(task({ state: "running" })), false);
  assert.equal(canStop(task({ state: "running", connectionActive: true })), true);
});

test("a retired task has no Open in harness destination", () => {
  assert.equal(sessionLinks(task()), null);
  assert.deepEqual(sessionLinks(task({ connectionActive: true })), { native: "t3code://threads/env/thread-9", web: "http://t3/thread-9", command: null });
  // Notifications and waiting sessions carry no connectionActive: their links stay.
  assert.deepEqual(sessionLinks({ links: { web: "http://oc/s" } }), { native: null, web: "http://oc/s", command: null });
});

test("a session with only a resume command offers it to copy, never on a replaced connection", () => {
  const cli = { links: { command: "harness resume s-1" } };
  assert.deepEqual(sessionLinks(cli), { native: null, web: null, command: "harness resume s-1" });
  assert.equal(resumeCommand(cli), "harness resume s-1");
  assert.equal(resumeCommand({ ...cli, connectionActive: false }), null);
  assert.equal(resumeCommand({ links: { web: "http://oc/s" } }), null);
});

test("session action opens the host's destination, else offers the resume command, else nothing", () => {
  const claude = { links: { command: "claude attach 1a2b3c4d" } };
  assert.deepEqual(sessionAction("t3code://threads/t-1", claude), { kind: "open", href: "t3code://threads/t-1" });
  assert.deepEqual(sessionAction(null, claude), { kind: "resume", command: "claude attach 1a2b3c4d" });
  // A replaced connection is history: no resume command either.
  assert.equal(sessionAction(null, { ...claude, connectionActive: false }), null);
  assert.equal(sessionAction(null, { links: { web: "http://oc/s" } }), null);
  assert.equal(sessionAction(null, {}), null);
});

test("retired notice names the harness and the instance Stage no longer uses", () => {
  assert.equal(
    retiredNotice(task(), "T3"),
    "Ran on T3 'desktop', which Stage no longer uses. Its history and result stay here.",
  );
  assert.equal(
    retiredNotice(task({ harness: "opencode", connectionLabel: "http://oc:4096" }), "OpenCode"),
    "Ran on OpenCode 'http://oc:4096', which Stage no longer uses. Its history and result stay here.",
  );
});

test("retired connection usage and forget copy", () => {
  assert.equal(usageText(oldT3), "4 tasks · 2 bindings");
  assert.equal(usageText({ tasks: 1, bindings: 1 }), "1 task · 1 binding");
  assert.equal(
    forgetPrompt(oldT3),
    "Forget desktop? Its tasks keep their history and stay read-only. Its project bindings stop asking for repair, and pairing this instance again starts a new connection.",
  );
});

test("active connections stay scoped to their machine", () => {
  const remote = connection(9, "t3", { machineId: 2, label: "desk" });
  const remoteOld = connection(8, "t3", { machineId: 2, active: false, label: "desk-old", retiredAt: "2026-11-11T00:00:00Z" });
  const here = [t3, oldT3].map((c) => ({ ...c, machineId: 1 }));
  const all = [...here, remote, remoteOld];
  assert.equal(activeOn(all, "t3", 1)?.id, 5);
  assert.equal(activeOn(all, "t3", 2)?.id, 9);
  assert.equal(activeOn(all, "opencode", 2), undefined);
});

test("Forget deletes the retired connection", async () => {
  const seen = [];
  const { api } = fakeApi(async (path, init) => {
    seen.push({ path, method: init.method });
    return new Response(null, { status: 204 });
  });
  await api.forgetConnection(1);
  assert.deepEqual(seen, [{ path: "/connections/1", method: "DELETE" }]);
});

test("OpenCode instance_changed is confirmed, then the same body is resent with newInstance", async () => {
  const bodies = [];
  const { api } = fakeApi(async (_path, init) => {
    const body = JSON.parse(init.body);
    bodies.push(body);
    if (body.newInstance !== true) return Response.json({ error: "another OpenCode service", code: "instance_changed" }, { status: 409 });
    return Response.json({});
  });
  const settings = { endpoint: "http://new:4096" };
  const err = await openCodeApi(api).save(settings).then(() => null, (e) => e);
  assert.ok(err instanceof ApiError);
  assert.equal(isInstanceChanged(err), true);
  await openCodeApi(api).save({ ...settings, newInstance: true });
  assert.deepEqual(bodies, [{ endpoint: "http://new:4096" }, { endpoint: "http://new:4096", newInstance: true }]);
  assert.equal(isInstanceChanged(new ApiError(409, "x", "connection_retired")), false);
  assert.equal(isInstanceChanged(new Error("x")), false);
  const back = new ApiError(409, "x", "instance_returned");
  assert.equal(isInstanceChanged(back), true);
  assert.equal(isInstanceReturned(back), true);
  assert.equal(isInstanceReturned(err), false);
});

test("connect-as-new-service and pairing-replaced copy", () => {
  assert.equal(
    newServicePrompt("http://new:4096", { label: "http://oc:4096", tasks: 3 }),
    "http://new:4096 is a different OpenCode service. Connect it as a new service? http://oc:4096 becomes read-only: its 3 tasks keep their last state and its project bindings need rebinding.",
  );
  assert.equal(
    returnedServicePrompt("http://old:4096", { label: "http://new:4096" }),
    "http://old:4096 is the OpenCode service Stage used before. Connect it again? Its tasks and project bindings come back, and http://new:4096 becomes read-only.",
  );
  assert.equal(
    replacedNotice({ id: 1, label: "desktop", tasks: 4, bindings: 2 }),
    "Paired. T3 'desktop' was replaced; its 4 tasks stay read-only. Bind projects to continue.",
  );
});

test("session and workspace endpoints take the connection id", async () => {
  const paths = [];
  const { api } = fakeApi(async (path) => {
    paths.push(path);
    return Response.json({ workspaces: [], task: null });
  });
  await api.sessionContext({ connectionId: 5, externalSessionId: "a/b" });
  await api.workspaces(5);
  assert.deepEqual(paths, ["/connections/5/sessions/a%2Fb/context", "/connections/5/workspaces"]);
});

test("a session row opens the host destination, else copies the resume command, else offers nothing", () => {
  const claude = { links: { command: "claude attach 1a2b3c4d" } };
  assert.deepEqual(sessionAction("t3code://threads/env/t", claude), { kind: "open", href: "t3code://threads/env/t" });
  assert.deepEqual(sessionAction(null, claude), { kind: "resume", command: "claude attach 1a2b3c4d" });
  assert.deepEqual(sessionAction("", claude), { kind: "resume", command: "claude attach 1a2b3c4d" });
  assert.equal(sessionAction(null, { links: {} }), null);
  assert.equal(sessionAction(null, { links: { command: "" } }), null);
});

test("folderName is a folder path's last segment", () => {
  assert.equal(folderName("/home/u/src/app"), "app");
  assert.equal(folderName("/home/u/src/app/"), "app");
  assert.equal(folderName("/"), null);
  assert.equal(folderName(""), null);
  assert.equal(folderName(null), null);
  assert.equal(folderName(undefined), null);
});

test("link schemes are valid, unreserved and sorted across all descriptors", () => {
  const schemeDescriptors = [
    { linkSchemes: ["t3code", "agent+app", "open2.dev", "stage-local", "http", "vbscript", "javascript", "invalid scheme", "T3CODE", "7app"] },
    { linkSchemes: ["t3code", "agent+app", "data", "file", "blob", "about", "mailto", "tel", "ftp", "ftps", "https", "stage", "open:"] },
    {},
  ];
  assert.deepEqual(linkSchemes(schemeDescriptors), ["agent+app", "open2.dev", "t3code"]);
  assert.deepEqual(linkSchemes([]), []);
});
