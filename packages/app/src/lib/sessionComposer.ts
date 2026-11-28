import type { Delivery, SendMessageRequest } from "../api/types.ts";

interface Operation {
  available: boolean;
  reason?: string;
}

export const deliveryLabel = { steer: "Steer", queue: "Queue", send: "Send" };
export const deliveryReceipt = { steer: "Steered", queue: "Queued", send: "Sent" };

const unavailable = (reason: string) => ({ buttons: [], reasons: [reason] });

export function composerDelivery(
  state: string,
  operations: Partial<Record<Delivery, Operation>> | undefined,
  requests: { ready: boolean; count: number; unlisted?: string },
  harness: string,
) {
  const candidates: readonly Delivery[] = state === "running" ? ["steer", "queue"] : ["send"];
  if (state === "needs_input") {
    if (!requests.ready) return unavailable("Checking pending requests…");
    if (requests.count > 0) return unavailable("Answer the pending requests above.");
    if (requests.unlisted) return unavailable(requests.unlisted);
  } else if (state !== "running" && state !== "done" && state !== "idle") {
    return unavailable(state === "launching" ? "Waiting for launch confirmation." : "This session is unavailable for messages.");
  }
  if (operations === undefined) return unavailable("Checking message delivery…");
  const buttons = candidates.filter((delivery) => operations[delivery]?.available === true);
  const reasons = candidates.flatMap((delivery) => {
    const op = operations[delivery];
    if (op?.available) return [];
    return [op?.reason?.trim() || `${deliveryLabel[delivery]} is unavailable in ${harness}.`];
  });
  return { buttons, reasons: [...new Set(reasons)] };
}

export const MAX_MESSAGE_BYTES = 32 * 1024;
export const tooLong = (text: string): boolean => new TextEncoder().encode(text).length > MAX_MESSAGE_BYTES;
export const sendable = (text: string): boolean => text.trim() !== "" && !tooLong(text);

export const isSendKey = (e: { key: string; ctrlKey: boolean; metaKey: boolean; isComposing?: boolean }): boolean =>
  e.key === "Enter" && (e.ctrlKey || e.metaKey) && !e.isComposing;

export function sessionMessage(text: string, delivery: Delivery, state: string, revision: number): SendMessageRequest & { delivery: Delivery } {
  return delivery === "send" && state === "needs_input"
    ? { text, delivery, expectedStateRevision: revision }
    : { text, delivery };
}
