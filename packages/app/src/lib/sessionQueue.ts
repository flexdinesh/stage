import type { QueuedMessage, Task } from "../api/types.ts";

export type QueueSession = Pick<Task, "launchAttempt" | "externalSessionId">;

export const taskQueueKey = (taskId: number, session: QueueSession) =>
  ["task-queue", taskId, session.launchAttempt, session.externalSessionId];

const sourceLabel: Record<QueuedMessage["source"], string | null> = {
  stage: "Stage",
  harness: "Harness",
  "": null,
};

const zeroTime = Date.parse("0001-01-01T00:00:00Z");

export function queueView(messages: readonly QueuedMessage[]) {
  return messages.map(({ id, text, delivery, createdAt, source }) => {
    const timestamp = Date.parse(createdAt);
    return {
      id,
      preview: text.trim().replace(/\s+/g, " ") || "Message preview unavailable",
      canSteer: delivery === "queue",
      at: Number.isFinite(timestamp) && timestamp !== zeroTime ? createdAt : null,
      source: sourceLabel[source],
    };
  });
}
