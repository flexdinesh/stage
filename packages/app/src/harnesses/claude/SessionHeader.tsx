import { Meta } from "../../components/ui.tsx";
import type { SessionSlotProps } from "../types.ts";
import { claudeSessionDetails } from "./claude.ts";

const permissionLabels = new Map([
  ["default", "Default permissions"],
  ["manual", "Ask before acting"],
  ["acceptEdits", "Accept edits"],
  ["auto", "Auto mode"],
  ["plan", "Plan mode"],
  ["dontAsk", "Don't ask"],
  ["bypassPermissions", "Bypass permissions"],
]);

const effortLabels = new Map([["xhigh", "extra high"], ["max", "maximum"]]);

export function ClaudeSessionHeader({ activity }: SessionSlotProps) {
  const { permissionMode, effort } = claudeSessionDetails(activity?.details);
  const model = activity?.model;
  return (
    <>
      {model ? <Meta className="min-w-0 wrap-anywhere">{model}</Meta> : null}
      {permissionMode ? <Meta className="min-w-0 wrap-anywhere">{permissionLabels.get(permissionMode) ?? permissionMode}</Meta> : null}
      {effort ? <Meta className="min-w-0 wrap-anywhere">{`Effort ${effortLabels.get(effort) ?? effort}`}</Meta> : null}
    </>
  );
}
