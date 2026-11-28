import assert from "node:assert/strict";
import { test } from "node:test";
import { ApiError } from "../src/api/client.ts";
import { OPERATION_LABELS } from "../src/lib/capabilities.ts";
import { browseStart, canBrowse, emptyListing, isAbsolutePath, listingStatus, returnFocus } from "../src/lib/directoryBrowse.ts";
import { fakeApi } from "./fake-transport.mjs";

const caps = (operations) => ({ operations, models: null, modes: null });

test("Browse shows only when the connection declares directoryBrowse available", () => {
  assert.equal(canBrowse(caps({ directoryBrowse: { available: true } })), true);
  assert.equal(canBrowse(caps({ directoryBrowse: { available: false, reason: "Type the path." } })), false);
  // An older server without the operation, or capabilities still loading.
  assert.equal(canBrowse(caps({ workspaceAdd: { available: true } })), false);
  assert.equal(canBrowse(undefined), false);
  assert.equal(OPERATION_LABELS.directoryBrowse, "Browse folders for paths");
});

test("the browser opens at a typed absolute path, else the harness's default start", () => {
  assert.equal(browseStart("/srv/app"), "/srv/app");
  assert.equal(browseStart("  /srv/my app  "), "/srv/my app");
  assert.equal(browseStart(""), "");
  assert.equal(browseStart("relative/dir"), "");
  assert.equal(browseStart("~/code"), "");
  assert.equal(isAbsolutePath("/"), true);
  assert.equal(isAbsolutePath("C:\\work"), false);
});

const listing = (n, over = {}) => ({
  path: "/w", parent: "/",
  entries: Array.from({ length: n }, (_, i) => ({ name: `d${i}`, path: `/w/d${i}` })),
  truncated: false, ...over,
});

test("the live region counts the listed folders", () => {
  assert.equal(listingStatus(listing(0)), "No folders in /w");
  assert.equal(listingStatus(listing(1)), "1 folder in /w");
  assert.equal(listingStatus(listing(3)), "3 folders in /w");
  assert.equal(listingStatus(listing(500, { truncated: true })), "500+ folders in /w");
  assert.match(emptyListing(false), /Show hidden/);
  assert.equal(emptyListing(true), "No folders here.");
});

test("going up focuses the folder we came from", () => {
  assert.equal(returnFocus(listing(3), "/w/d2"), 2);
  assert.equal(returnFocus(listing(3), "/w/gone"), 0);
  assert.equal(returnFocus(listing(3), null), 0);
});

test("browseDirectories asks the connection's host, with hidden only when on", async () => {
  const sent = [];
  const { api } = fakeApi(async (path) => {
    sent.push(path);
    if (path.includes("missing")) return Response.json({ error: "Folder not found on the OpenCode host: /missing." }, { status: 400 });
    return Response.json(listing(1));
  });
  assert.deepEqual(await api.browseDirectories(4, "", false), listing(1));
  await api.browseDirectories(4, "/srv/my app", true);
  assert.deepEqual(sent, ["/connections/4/directories", "/connections/4/directories?path=%2Fsrv%2Fmy+app&hidden=true"]);
  await assert.rejects(api.browseDirectories(4, "/missing", false), (error) =>
    error instanceof ApiError && error.status === 400 && error.message === "Folder not found on the OpenCode host: /missing.");
});
