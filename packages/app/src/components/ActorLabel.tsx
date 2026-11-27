import type { SessionRef, WorkAttribution } from "../api/types.ts";
import { SourceSessionLink } from "./StageLinks.tsx";
import { TextLink } from "./ui.tsx";

const labels = {
  agent: "Agent",
  machine: "Machine",
  personal: "Personal access",
  system: "Automation",
  cron: "Cron",
  historical: "Historical",
  unknown: "Unknown actor",
};

export function ActorLabel({ work }: { work: WorkAttribution }) {
  const { actor, cause } = work;
  const kind = actor?.kind ?? "historical";
  const name = actor?.displayName;
  let label: string;
  if (kind === "human") {
    label = name || (actor?.userId === undefined ? labels.unknown : `User #${actor.userId}`);
  } else {
    label = cause?.kind === "harness_observation" ? "Harness" : labels[kind];
    if (work.sourceMachineId != null && (kind === "agent" || kind === "machine")) {
      label = kind === "agent" ? `Agent · machine #${work.sourceMachineId}` : `Machine #${work.sourceMachineId}`;
    }
    if (name && actor?.initiatorUserId !== undefined) label += ` · via ${name}`;
  }
  return <span className="min-w-0 wrap-anywhere">{label}</span>;
}

export function ActorCause({ work, taskId, sourceSession }: { work: WorkAttribution; taskId?: number | null; sourceSession?: SessionRef | null }) {
  const cause = work.cause;
  if (cause?.kind === "dependency_release" && cause.taskId !== undefined) {
    return <TextLink to={`/tasks/${cause.taskId}`} variant="rest">Prerequisite #{cause.taskId}</TextLink>;
  }
  if (cause?.scheduleId !== undefined) {
    return <TextLink to={`/schedules/${cause.scheduleId}`} variant="rest">Schedule #{cause.scheduleId}</TextLink>;
  }
  const session = cause?.session;
  if (session !== undefined && (session.connectionId !== sourceSession?.connectionId || session.externalSessionId !== sourceSession?.externalSessionId)) {
    return <SourceSessionLink id={session.externalSessionId} connectionId={session.connectionId} />;
  }
  if (cause?.taskId !== undefined && cause.taskId !== taskId) {
    return <TextLink to={`/tasks/${cause.taskId}`} variant="rest">Task #{cause.taskId}</TextLink>;
  }
  return null;
}
