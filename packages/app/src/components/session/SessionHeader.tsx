import type { ReactNode } from "react";
import type { TaskDetail } from "../../api/types.ts";
import { Chip, CopyButton, Meta, Panel, type ChipTone } from "../ui.tsx";
import { AttemptSwitcher } from "./AttemptSwitcher.tsx";

const STATE_TONE: Readonly<Record<string, ChipTone>> = {
  running: "run", launching: "run", needs_input: "need", done: "done", idle: "idle",
  failed: "bad", interrupted: "bad", stopped: "idle", cancelled: "idle",
};

export const SESSION_ACTIONS_ANCHOR = "session-actions";

export function SessionHeader({
  task, harness, attempt, state, reason, sessionId, extra, actions,
}: {
  task: TaskDetail;
  harness: string;
  attempt: number | undefined;
  state?: string | undefined;
  reason?: string | undefined;
  sessionId?: string | undefined;
  extra?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <Panel
      title={`${harness} session`}
      actions={<AttemptSwitcher taskId={task.id} sessions={task.sessions} selected={attempt} />}
      flush
    >
      {state || sessionId || extra || actions ? (
        <div className="flex flex-col gap-2.5 px-3 py-row">
          {state || sessionId || extra ? (
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
              {state ? <Chip tone={STATE_TONE[state] ?? "plain"} dot>{state.replaceAll("_", " ")}</Chip> : null}
              {reason ? <Meta className="min-w-0 wrap-anywhere">{reason}</Meta> : null}
              {sessionId ? (
                <div className="flex min-w-0 items-center gap-2 sm:ml-auto">
                  <Meta title={sessionId} className="truncate">{sessionId}</Meta>
                  <CopyButton small text={sessionId} label="Copy session ID" title={sessionId} />
                </div>
              ) : null}
              {extra}
            </div>
          ) : null}
          {actions ? (
            <div id={SESSION_ACTIONS_ANCHOR} tabIndex={-1} className="flex scroll-mt-[calc(4.25rem+var(--inset-top))] flex-wrap items-center gap-2 focus:outline-none lg:scroll-mt-[calc(1rem+var(--edge-band))]">
              {actions}
            </div>
          ) : null}
        </div>
      ) : null}
    </Panel>
  );
}
