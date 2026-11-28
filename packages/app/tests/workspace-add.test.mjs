import assert from "node:assert/strict";
import { test } from "node:test";
import { ApiError } from "../src/api/client.ts";
import { fieldInput } from "../src/lib/connectionSetup.ts";
import {
  addConnection, addedChoice, addFailure, addRequest, canSubmit, mintRequestId, nextRequestKey, pickSource,
  requestSignature, sourceControl,
} from "../src/lib/workspaceAdd.ts";
import { fakeApi } from "./fake-transport.mjs";

const connection = (over = {}) => ({
  id: 3, machineId: 1, harness: "t3", configured: true, autoComplete: false, active: true, label: "laptop",
  retiredAt: null, forgottenAt: null, tasks: 0, bindings: 0, newTaskConfig: null, ...over,
});

const clone = {
  id: "clone", label: "Clone repository", help: "The harness clones it.",
  input: [
    { id: "url", label: "Repository URL", kind: "url", required: true },
    { id: "path", label: "Folder", kind: "path", required: true },
    { id: "shallow", label: "Shallow", kind: "bool" },
  ],
  availability: { available: true },
};
const folder = { id: "folder", label: "Existing folder", input: [{ id: "path", label: "Folder", kind: "path", required: true }], availability: { available: true } };
const trust = { ...folder, id: "trust", label: "Trust folder", confirm: "Trust this folder in the harness?" };
const off = { id: "off", label: "Remote", availability: { available: false, reason: "Needs a newer harness." } };

const caps = (over = {}) => ({
  operations: { workspaceAdd: { available: true } }, models: null, modes: null, workspaceSources: [clone, folder], ...over,
});

test("connections without a usable add are disabled with the harness's reason", () => {
  assert.deepEqual(
    pick(addConnection(connection(), { data: caps(), error: null })),
    { available: true, reason: null, checking: false, sources: 2 },
  );
  const unsupported = caps({ operations: { workspaceAdd: { available: false, reason: "Adding workspaces from Stage is not supported for T3 Code yet." } } });
  assert.equal(addConnection(connection(), { data: unsupported, error: null }).reason, "Adding workspaces from Stage is not supported for T3 Code yet.");
  assert.equal(addConnection(connection({ configured: false }), { data: undefined, error: null }).reason, "Not connected.");
  assert.deepEqual(pick(addConnection(connection(), { data: undefined, error: null })), { available: false, reason: "Checking…", checking: true, sources: 0 });
  assert.equal(addConnection(connection(), { data: undefined, error: new Error("offline") }).reason, "offline");
  // The op is on, but every source is off: the first source's reason.
  assert.equal(addConnection(connection(), { data: caps({ workspaceSources: [off] }), error: null }).reason, "Needs a newer harness.");
  assert.equal(addConnection(connection(), { data: caps({ workspaceSources: [] }), error: null }).available, false);
  // An older server without workspaceAdd at all.
  assert.equal(addConnection(connection(), { data: caps({ operations: {} }), error: null }).available, false);
});

function pick(c) {
  return { available: c.available, reason: c.reason, checking: c.checking, sources: c.sources.length };
}

test("the source control is hidden for one source, segmented for two to four, a dropdown beyond", () => {
  assert.equal(sourceControl([]), "hidden");
  assert.equal(sourceControl([clone]), "hidden");
  assert.equal(sourceControl([clone, folder]), "segmented");
  assert.equal(sourceControl([clone, folder, trust, off]), "segmented");
  assert.equal(sourceControl([clone, folder, trust, off, { ...folder, id: "x" }]), "select");
});

test("an unavailable source is never picked", () => {
  assert.equal(pickSource([off, folder], null)?.id, "folder");
  assert.equal(pickSource([off, folder], "off")?.id, "folder");
  assert.equal(pickSource([clone, folder], "folder")?.id, "folder");
  assert.equal(pickSource([off], null), undefined);
});

test("Add needs every required field and, when the source has one, the agreed confirmation", () => {
  assert.equal(canSubmit(clone, {}, false), false);
  assert.equal(canSubmit(clone, { url: "https://example.test/r.git", path: "  " }, false), false);
  assert.equal(canSubmit(clone, { url: "https://example.test/r.git", path: "/src/r" }, false), true);
  assert.equal(canSubmit(trust, { path: "/src/r" }, false), false);
  assert.equal(canSubmit(trust, { path: "/src/r" }, true), true);
  assert.equal(canSubmit(off, {}, true), false);
  assert.equal(canSubmit(undefined, {}, true), false);
});

test("the request carries the entered input, the project to join and the confirmation only when declared", () => {
  // Projects page: Stage groups it.
  assert.deepEqual(addRequest(clone, { url: "u", path: "/p", shallow: false }, { requestId: "k", confirmed: false }),
    { sourceId: "clone", input: { url: "u", path: "/p" }, requestId: "k" });
  // Project detail and the task form opened from a project: it joins that project.
  assert.deepEqual(addRequest(folder, { path: "/p" }, { requestId: "k", projectId: 12, confirmed: true }),
    { sourceId: "folder", input: { path: "/p" }, requestId: "k", projectId: 12 });
  assert.deepEqual(addRequest(trust, { path: "/p" }, { requestId: "k", confirmed: true }),
    { sourceId: "trust", input: { path: "/p" }, requestId: "k", confirmed: true });
});

test("the same request keeps its key; a changed one gets a new key", () => {
  let n = 0;
  const mint = () => `key-${++n}`;
  const body = addRequest(clone, { url: "u", path: "/p" }, { requestId: "", confirmed: false });
  const first = nextRequestKey(null, requestSignature(3, body), mint);
  assert.equal(first.id, "key-1");
  // Input key order never matters.
  const reordered = { ...body, input: { path: "/p", url: "u" } };
  assert.equal(nextRequestKey(first, requestSignature(3, reordered), mint).id, "key-1");
  assert.equal(nextRequestKey(first, requestSignature(3, { ...body, input: { url: "u", path: "/q" } }), mint).id, "key-2");
  assert.equal(nextRequestKey(first, requestSignature(4, body), mint).id, "key-3");
  assert.equal(nextRequestKey(first, requestSignature(3, { ...body, projectId: 9 }), mint).id, "key-4");
});

test("request ids are random hex under the server's limit", () => {
  const id = mintRequestId((bytes) => bytes.fill(171));
  assert.equal(id, `ws-add-${"ab".repeat(16)}`);
  assert.ok(id.length <= 256);
  assert.notEqual(mintRequestId(), mintRequestId());
});

test("failures read as uncertain, not moved, or a relayed error", () => {
  assert.equal(addFailure(null), null);
  assert.deepEqual(addFailure(new ApiError(504, "T3 Code did not answer in time", "outcome_uncertain")), { kind: "uncertain", message: "T3 Code did not answer in time" });
  assert.equal(addFailure(new ApiError(504, "gateway"))?.kind, "uncertain");
  assert.equal(addFailure(new ApiError(409, "added; moving failed", "membership_failed"))?.kind, "membership");
  assert.deepEqual(addFailure(new ApiError(400, "folder does not exist")), { kind: "error", message: "folder does not exist" });
  assert.equal(addFailure(new ApiError(502, "not listed", "workspace_not_listed"))?.kind, "error");
  assert.equal(addFailure(new TypeError("Failed to fetch"))?.message, "Failed to fetch");
});

test("Check again after an uncertain outcome resends the same request id, and a success selects the new destination", async () => {
  const sent = [];
  let replies = 0;
  const { api } = fakeApi(async (path, init) => {
    sent.push({ path, body: JSON.parse(String(init.body)) });
    replies += 1;
    if (replies === 1) return Response.json({ error: "T3 Code did not confirm the result", code: "outcome_uncertain" }, { status: 504 });
    return Response.json({
      workspace: { connectionId: 3, workspaceId: "ws_new", title: "repo", connectionLabel: "laptop", machineLabel: "Main", available: true, path: "/src/repo" },
      group: { id: 12, title: "repo" }, existing: false,
    }, { status: 201 });
  });
  const values = { url: "https://example.test/r.git", path: "/src/repo" };
  let key = null;
  const send = () => {
    const body = addRequest(clone, values, { requestId: "", projectId: 12, confirmed: false });
    key = nextRequestKey(key, requestSignature(3, body), () => mintRequestId());
    return api.addWorkspace(3, { ...body, requestId: key.id });
  };
  const error = await send().then(() => null, (e) => e);
  assert.equal(addFailure(error)?.kind, "uncertain");
  const result = await send();
  assert.equal(sent.length, 2);
  assert.equal(sent[0].path, "/connections/3/workspaces");
  assert.equal(sent[1].body.requestId, sent[0].body.requestId);
  assert.deepEqual(sent[1].body, { sourceId: "clone", input: values, requestId: sent[0].body.requestId, projectId: 12 });
  assert.deepEqual(addedChoice(result), { id: "ws_new", label: "repo", detail: "/src/repo" });
  // Without a title or path (an agent-shaped projection), the id still names it.
  assert.deepEqual(addedChoice({ ...result, workspace: { ...result.workspace, title: "", path: undefined } }), { id: "ws_new", label: "ws_new", detail: "ws_new" });
});

test("path fields render monospace; secrets and URLs keep their input types", () => {
  assert.deepEqual(fieldInput({ kind: "path" }), { type: "text", mono: true });
  assert.deepEqual(fieldInput({ kind: "text" }), { type: "text", mono: false });
  assert.deepEqual(fieldInput({ kind: "url" }), { type: "url", mono: false });
  assert.deepEqual(fieldInput({ kind: "secret" }), { type: "password", mono: false });
});
