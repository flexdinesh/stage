// A session's action on rows, toasts and alerts: Open in <harness> when the host has a destination, else Copy resume
// command for a harness whose sessions resume in its own client (Claude Code). Logic: lib/harnesses.ts sessionAction.
import type { SessionAction } from "../lib/harnesses.ts";
import { AppButton, CopyButton, type Variant } from "./ui.tsx";

export const RESUME_LABEL = "Copy resume command";

export function SessionActionButton({
  action,
  name,
  variant = "secondary",
  small = false,
  className,
}: {
  action: SessionAction | null;
  /** The harness's display name ("Open in T3"). */
  name: string;
  variant?: Variant;
  small?: boolean;
  className?: string;
}) {
  if (action === null) return null;
  if (action.kind === "open") {
    return (
      <AppButton href={action.href} variant={variant} small={small} {...(className === undefined ? {} : { className })}>
        Open in {name}
      </AppButton>
    );
  }
  return <CopyButton text={action.command} label={RESUME_LABEL} title={action.command} small={small} variant={variant} className={className} />;
}
