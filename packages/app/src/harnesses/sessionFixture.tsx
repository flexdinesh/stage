import type { Activity, Capabilities } from "../api/types.ts";
import { Chip, Panel } from "../components/ui.tsx";
import type { SessionSlots } from "./types.ts";

// Matches the hosted test connection used by the engine's session surface contract.
export const SESSION_FIXTURE_KIND = "hosted";

export const SESSION_FIXTURE_SLOTS: SessionSlots = {
  HeaderExtra: ({ current }) => <Chip>{current ? "Live fixture" : "Recorded fixture"}</Chip>,
  Sections: ({ activity }) => {
    if (typeof activity?.details !== "object" || activity.details === null || !("summary" in activity.details)) return null;
    const { summary } = activity.details;
    return typeof summary === "string" ? <Panel title="Fixture details"><p>{summary}</p></Panel> : null;
  },
};

export const FIXTURE_DELIVERY: Capabilities["operations"] = {
  steer: { available: true },
  queue: { available: true },
  send: { available: true },
  activity: { available: true },
};

export const FIXTURE_ACTIVITY: Activity = {
  model: "Fixture model",
  at: "2026-11-28T14:00:00Z",
  truncated: true,
  plan: [
    { text: "Read the dispatch path", status: "done" },
    { text: "Verify message delivery", status: "in_progress" },
    { text: "Run the contract checks", status: "pending" },
  ],
  files: [{ path: "server/internal/execution/activity.go", additions: 12, deletions: 3, status: "modified" }],
  items: [
    { id: "1", at: "2026-11-28T13:59:00Z", kind: "command", title: "Run contract checks", preview: "go test ./internal/engine", text: "", status: "done" },
    { id: "2", at: "2026-11-28T14:00:00Z", kind: "message_agent", title: "Assistant message", text: "Delivery checks passed. Reviewing the activity bounds.", preview: "", status: "running" },
  ],
  details: { summary: "Activity read by the registered fixture slot." },
};
