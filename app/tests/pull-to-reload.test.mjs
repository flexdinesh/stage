import assert from "node:assert/strict";
import { test } from "node:test";
import { PullToReloadGesture } from "../src/lib/pull-to-reload.ts";

const point = (clientY, clientX = 100, identifier = 1) => ({ identifier, clientX, clientY });

test("small downward movements are claimed before scrolling, without showing the indicator", () => {
  const gesture = new PullToReloadGesture();
  gesture.start(point(0));
  assert.equal(gesture.move(point(1)), true);
  assert.equal(gesture.phase, "idle");
  assert.equal(gesture.offset, 0);
  assert.equal(gesture.release(point(1)), false);
});

test("release reloads only at 80 CSS pixels, including a nonzero starting position", () => {
  for (const distance of [0, 7, 8, 79, 80, 81, 200]) {
    const gesture = new PullToReloadGesture();
    gesture.start(point(150));
    gesture.move(point(150 + distance));
    assert.equal(gesture.release(point(150 + distance)), distance >= 80);
    assert.equal(gesture.phase, distance >= 80 ? "reloading" : "idle");
  }
});

test("crossing the threshold alone never reloads; current release distance decides", () => {
  const gesture = new PullToReloadGesture();
  gesture.start(point(0));
  gesture.move(point(100));
  assert.equal(gesture.phase, "armed");
  gesture.move(point(30));
  assert.equal(gesture.phase, "pulling");
  assert.equal(gesture.release(point(3)), false);
});

test("release position overrides the last move", () => {
  const gesture = new PullToReloadGesture();
  gesture.start(point(0));
  gesture.move(point(100));
  assert.equal(gesture.release(point(79)), false);
  gesture.start(point(0));
  gesture.move(point(50));
  assert.equal(gesture.release(point(80)), true);
});

test("horizontal or upward intent permanently cancels the sequence", () => {
  for (const move of [point(10, 130), point(20, 120), point(-10)]) {
    const gesture = new PullToReloadGesture();
    gesture.start(point(0));
    assert.equal(gesture.move(move), false);
    assert.equal(gesture.move(point(100)), false);
    assert.equal(gesture.release(point(100)), false);
  }
});

test("a different touch identifier cancels and cannot re-arm", () => {
  const gesture = new PullToReloadGesture();
  gesture.start(point(0));
  gesture.move(point(100));
  assert.equal(gesture.move(point(100, 100, 2)), false);
  assert.equal(gesture.release(point(100)), false);
});

test("explicit cancellation disarms a pull and permits the next gesture", () => {
  const gesture = new PullToReloadGesture();
  gesture.start(point(0));
  gesture.move(point(100));
  gesture.cancel();
  assert.equal(gesture.phase, "idle");
  assert.equal(gesture.release(point(100)), false);
  gesture.start(point(0));
  assert.equal(gesture.release(point(100)), true);
});

test("a successful release is latched, including duplicate events and new touches", () => {
  const gesture = new PullToReloadGesture();
  gesture.start(point(0));
  assert.equal(gesture.release(point(100)), true);
  assert.equal(gesture.release(point(100)), false);
  gesture.start(point(0));
  assert.equal(gesture.move(point(100)), false);
  assert.equal(gesture.release(point(100)), false);
  assert.equal(gesture.phase, "reloading");
});

test("cancelling a scheduled reload permits a new gesture", () => {
  const gesture = new PullToReloadGesture();
  gesture.start(point(0));
  assert.equal(gesture.release(point(100)), true);
  gesture.cancel();
  gesture.start(point(0));
  assert.equal(gesture.release(point(100)), true);
});

test("content follows the pull with bounded, increasing resistance", () => {
  const gesture = new PullToReloadGesture();
  gesture.start(point(150));
  const offsets = [20, 40, 80, 160, 320, 10000].map((distance) => {
    gesture.move(point(150 + distance));
    return gesture.offset;
  });
  assert.ok(offsets[0] > 0);
  assert.ok(offsets[2] >= 48, "the armed pull leaves room for the indicator");
  assert.ok(offsets.every((offset, i) => offset < 112 && (i === 0 || offset > offsets[i - 1])));
  assert.ok((offsets[2] - offsets[1]) / 40 < (offsets[1] - offsets[0]) / 20, "resistance increases with distance");
  gesture.move(point(190));
  assert.equal(gesture.offset, offsets[1], "reversing the pull follows the current finger position");
});

test("release settles content immediately while the reloading phase stays latched", () => {
  for (const distance of [40, 80, 200]) {
    const gesture = new PullToReloadGesture();
    gesture.start(point(0));
    gesture.move(point(distance));
    assert.ok(gesture.offset > 0);
    assert.equal(gesture.release(point(distance)), distance >= 80);
    assert.equal(gesture.offset, 0);
    assert.equal(gesture.phase, distance >= 80 ? "reloading" : "idle");
  }
});

test("cancellation clears displacement before another gesture", () => {
  for (const cancel of [(g) => g.cancel(), (g) => g.move(point(-10)), (g) => g.move(point(100, 100, 2))]) {
    const gesture = new PullToReloadGesture();
    gesture.start(point(0));
    gesture.move(point(100));
    cancel(gesture);
    assert.equal(gesture.offset, 0);
    gesture.start(point(0));
    assert.equal(gesture.offset, 0);
    gesture.move(point(40));
    assert.ok(gesture.offset > 0);
  }
});
