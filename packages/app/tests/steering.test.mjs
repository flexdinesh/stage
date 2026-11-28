import assert from "node:assert/strict";
import { test } from "node:test";
import {
  HOLD_PRESETS,
  deliveryError,
  deliveryLine,
  endReasonLabel,
  eventPending,
  heldAlert,
  heldChip,
  holdCountdown,
  holdLabel,
  holdOptions,
  journalEventLabel,
  journalEventText,
  mintSteerRequestId,
  steererPhase,
  steeringEventLine,
} from "../src/lib/steering.ts";

const now = Date.parse("2026-11-28T12:00:00Z");
const at = (ms) => new Date(now + ms).toISOString();

test("steerer phase: live while it can take updates, starting before launch, stopped after a failed run", () => {
  for (const s of ["running", "done", "needs_input"]) assert.equal(steererPhase(s), "live");
  for (const s of ["backlog", "launching"]) assert.equal(steererPhase(s), "starting");
  for (const s of ["failed", "interrupted", "cancelled"]) assert.equal(steererPhase(s), "stopped");
});

test("delivery line names the last update and what the next digest carries", () => {
  assert.equal(deliveryLine({ lastDeliveredAt: at(-120_000), pending: 3 }, now), "Last update 2m ago · 3 pending");
  assert.equal(deliveryLine({ lastDeliveredAt: null, pending: 0 }, now), "No update sent yet · 0 pending");
});

test("delivery error hides only the not-running error a stopped steerer already shows", () => {
  assert.equal(deliveryError({ lastError: null, state: "running" }), null);
  assert.equal(deliveryError({ lastError: "steerer not running", state: "failed" }), null);
  assert.equal(deliveryError({ lastError: "steerer not running", state: "running" }), "steerer not running");
  assert.equal(deliveryError({ lastError: "dial tcp: refused", state: "running" }), "dial tcp: refused");
});

test("hold countdown rounds up to whole minutes and never reads zero", () => {
  assert.equal(holdCountdown(at(4 * 60_000), now), "4m");
  assert.equal(holdCountdown(at(3 * 60_000 + 1), now), "4m");
  assert.equal(holdCountdown(at(61_000), now), "2m");
  assert.equal(holdCountdown(at(60_000), now), "<1m");
  assert.equal(holdCountdown(at(5_000), now), "<1m");
  assert.equal(holdCountdown(at(-5_000), now), "<1m");
});

test("held chip and alert name the steerer and when the user hears about it", () => {
  const hold = { until: at(4 * 60_000), steererTaskId: 480 };
  assert.deepEqual(heldChip(hold, now), { label: "Steerer · 4m", title: "Steerer #480 is handling this. You will be notified in 4m." });
  assert.equal(heldAlert(hold, now), "Steerer #480 is handling this. You'll be notified in 4m if it doesn't answer.");
  assert.equal(heldChip({ ...hold, steererTaskId: null }, now).title, "The steerer is handling this. You will be notified in 4m.");
});

test("hold labels and options: presets, plus a value set elsewhere in order", () => {
  assert.deepEqual(HOLD_PRESETS, [0, 120, 300, 600, 900]);
  assert.equal(holdLabel(0), "Off");
  assert.equal(holdLabel(300), "5 min");
  assert.equal(holdLabel(90), "90 s");
  assert.deepEqual(holdOptions(300).map((o) => [o.label, o.checked]), [["Off", false], ["2 min", false], ["5 min", true], ["10 min", false], ["15 min", false]]);
  assert.deepEqual(holdOptions(3600).map((o) => o.seconds), [0, 120, 300, 600, 900, 3600]);
  assert.equal(holdOptions(420).find((o) => o.checked)?.label, "7 min");
});

test("end reasons read in words", () => {
  assert.equal(endReasonLabel("tree_finished"), "tree finished");
  assert.equal(endReasonLabel("task_removed"), "steerer task removed");
  assert.equal(endReasonLabel("stopped"), "stopped");
  assert.equal(endReasonLabel("something_new"), "something new");
});

test("feed lines carry the member title and the event's detail", () => {
  assert.deepEqual(steeringEventLine({ kind: "member_needs_input", payload: { title: "Build API", stateRevision: 4 } }), { label: "needs input", title: "Build API", detail: null });
  assert.deepEqual(steeringEventLine({ kind: "member_done", payload: { title: "Build API", reply: "Done." } }), { label: "run finished", title: "Build API", detail: "Done." });
  assert.equal(steeringEventLine({ kind: "member_failed", payload: { title: "x", error: "boom" } }).detail, "boom");
  assert.equal(steeringEventLine({ kind: "member_answered", payload: { title: "x", via: "respond", requestId: "r" } }).detail, "through a request");
  assert.equal(steeringEventLine({ kind: "member_answered", payload: { title: "x", via: "send" } }).detail, "through a message");
  assert.deepEqual(steeringEventLine({ kind: "note", payload: { title: "x", category: "blocker", text: "Waiting on keys" } }), { label: "blocker note", title: "x", detail: "Waiting on keys" });
  assert.equal(steeringEventLine({ kind: "hold_escalated", payload: { timeout: true } }).detail, "Steerer did not answer");
  assert.equal(steeringEventLine({ kind: "hold_escalated", payload: { reason: "needs a product call" } }).detail, "Steerer: needs a product call");
  assert.equal(steeringEventLine({ kind: "tree_status", payload: { from: "open", to: "done" } }).detail, "open to done");
  assert.equal(steeringEventLine({ kind: "steering_ended", payload: { reason: "replaced" } }).detail, "replaced");
  assert.equal(steeringEventLine({ kind: "tree_finished", payload: { reason: "members_completed" } }).detail, "every member completed");
  assert.deepEqual(steeringEventLine({ kind: "future_kind", payload: {} }), { label: "future kind", title: null, detail: null });
});

test("an event is pending after the active cursor unless the steerer caused it", () => {
  const active = { deliveredThrough: 40 };
  assert.equal(eventPending({ id: 41, self: false }, active), true);
  assert.equal(eventPending({ id: 41, self: true }, active), false);
  assert.equal(eventPending({ id: 40, self: false }, active), false);
  assert.equal(eventPending({ id: 41, self: false }, null), false);
});

test("tree journal steering events read in words", () => {
  assert.equal(journalEventLabel("steering_replaced"), "steerer replaced");
  assert.equal(journalEventLabel("member_added"), "member added");
  assert.equal(journalEventText("steering_ended", "Steering by #12 ended (tree_finished)"), "Steering by #12 ended: tree finished");
  assert.equal(journalEventText("steering_ended", "Steering by #12 ended (stopped)"), "Steering by #12 ended: stopped");
  assert.equal(journalEventText("steering_hold_changed", "Steering hold: 5m0s -> off"), "Steering hold: 5m to off");
  assert.equal(journalEventText("steering_hold_changed", "Steering hold: 1h0m0s -> 2m0s"), "Steering hold: 1h to 2m");
  assert.equal(journalEventText("steering_started", "Steering by #12 started"), "Steering by #12 started");
});

test("steer request ids are random and prefixed", () => {
  const a = mintSteerRequestId();
  assert.match(a, /^steer-[0-9a-f]{32}$/);
  assert.notEqual(a, mintSteerRequestId());
});
