import assert from "node:assert/strict";
import { test } from "node:test";
import { ANCHOR_EDGE, ANCHOR_GAP, ANCHOR_TARGET_HEIGHT, anchorBox, anchorRoom } from "../src/lib/snippet-placement.ts";

/** Phone with the keyboard up: visible area 0..420, sheet body scrolling from 52. */
const base = { fieldLeft: 12, fieldWidth: 366, viewTop: 0, viewBottom: 420, containerTop: 52 };
const line = (lineTop, height = 24) => ({ lineTop, lineBottom: lineTop + height });

test("plenty of room: no scroll, full target height right under the / line", () => {
  const a = { ...base, ...line(80) };
  assert.equal(anchorRoom(a), 0);
  assert.deepEqual(anchorBox(a), { top: 104 + ANCHOR_GAP, left: 12, width: 366, maxHeight: ANCHOR_TARGET_HEIGHT });
});

test("/ near the keyboard: scrolls exactly the missing room, then fits the target height", () => {
  const a = { ...base, ...line(300) };
  const room = anchorRoom(a);
  assert.equal(room, 324 + ANCHOR_GAP + ANCHOR_TARGET_HEIGHT + ANCHOR_EDGE - 420);
  const after = { ...a, lineTop: a.lineTop - room, lineBottom: a.lineBottom - room };
  const box = anchorBox(after);
  assert.equal(box.maxHeight, ANCHOR_TARGET_HEIGHT);
  assert.equal(box.top + box.maxHeight + ANCHOR_EDGE, 420);
});

test("scroll is capped so the / line never goes above the container top", () => {
  const short = { ...base, viewBottom: 300, ...line(200) };
  assert.equal(anchorRoom(short), 200 - 52);
  const visual = { ...base, viewTop: 70, viewBottom: 300, ...line(200) };
  assert.equal(anchorRoom(visual), 200 - 70);
});

test("cramped: still opens below, using whatever space is left", () => {
  const a = { ...base, viewBottom: 300, ...line(52) };
  assert.equal(anchorRoom(a), 0);
  const box = anchorBox(a);
  assert.equal(box.top, 76 + ANCHOR_GAP);
  assert.equal(box.maxHeight, 300 - ANCHOR_EDGE - box.top);
  assert.equal(anchorBox({ ...base, viewBottom: 90, ...line(80) }).maxHeight, 0);
});

test("keyboard closing re-caps the height without moving the top", () => {
  const up = anchorBox({ ...base, ...line(200) });
  const down = anchorBox({ ...base, viewBottom: 800, ...line(200) });
  assert.equal(down.top, up.top);
  assert.ok(up.maxHeight < ANCHOR_TARGET_HEIGHT);
  assert.equal(down.maxHeight, ANCHOR_TARGET_HEIGHT);
});
