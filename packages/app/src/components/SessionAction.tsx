// A session's action on rows, toasts and alerts: Open in <harness> when the host has a destination, else Copy resume
// command for a harness whose sessions resume in its own client (Claude Code). Logic: lib/harnesses.ts sessionAction.
// A session on a remote machine names that machine, where the command must run (lib/machines.ts resumeLabel).
import { useHarnesses, useMachines } from "../api/queries.ts";
import type { SessionAction } from "../lib/harnesses.ts";
import { resumeLabel, resumeMachine } from "../lib/machines.ts";
import { AppButton, CopyButton, type Variant } from "./ui.tsx";

export function SessionActionButton({
  action,
  name,
  variant = "secondary",
  small = false,
  className,
  connectionId,
}: {
  action: SessionAction | null;
  /** The harness's display name ("Open in T3"). */
  name: string;
  variant?: Variant;
  small?: boolean;
  className?: string;
  /** The session's connection: its machine, when remote, is named on Copy resume command. */
  connectionId: number;
}) {
  const harnesses = useHarnesses();
  const { machines } = useMachines();
  if (action === null) return null;
  if (action.kind === "open") {
    return (
      <AppButton href={action.href} variant={variant} small={small} {...(className === undefined ? {} : { className })}>
        Open in {name}
      </AppButton>
    );
  }
  const machine = resumeMachine(machines, harnesses.connection(connectionId)?.machineId ?? 0);
  const title = machine === undefined ? action.command : `Run it in a terminal on ${machine}: ${action.command}`;
  return <CopyButton text={action.command} label={resumeLabel(machine)} title={title} small={small} variant={variant} className={className} />;
}
