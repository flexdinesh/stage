// Following up a task's session from Stage: one composer panel on task detail. It leads under the needs-input alert
// when the turn ended asking you (Reply), else sits under Last word (Follow up). The page owns the draft and the send
// mutation, so both survive the panel moving and the session working. Logic: lib/followup.ts; hooks: api/queries.ts
// useConnectionOperation, useSendMessage. A Reply is guarded by the task's state revision (first answer wins): one that lost reads
// "Already answered, by the steerer or in the harness." and keeps the draft.
import { useId, type FormEvent, type KeyboardEvent } from "react";
import { busyHint, isSendKey, MAX_MESSAGE_BYTES, sendable, tooLong, type FollowUpView } from "../lib/followup.ts";
import { AnswerError } from "./Requests.tsx";
import { Button, InlineError, Kbd, Panel, RelTime, Textarea } from "./ui.tsx";

export function FollowUpPanel({
  view,
  harness,
  draft,
  onDraft,
  onSend,
  sending = false,
  sentAt = null,
  error = null,
}: {
  view: FollowUpView;
  harness: string;
  draft: string;
  onDraft: (text: string) => void;
  onSend: (text: string) => void;
  sending?: boolean;
  /** When the last message went from here (panel meta). */
  sentAt?: string | null;
  error?: unknown;
}) {
  const id = useId();
  const hintId = `${id}-hint`;
  const lead = view.placement === "lead";
  const long = tooLong(draft);
  const ready = !view.busy && !sending && sendable(draft);

  const send = () => {
    if (ready) onSend(draft);
  };
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    send();
  };
  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (!isSendKey(e)) return;
    e.preventDefault();
    send();
  };

  return (
    <Panel title={view.title} meta={sentAt === null ? undefined : <RelTime at={sentAt} prefix="sent" className="tabular" />}>
      <form onSubmit={submit} className="flex flex-col gap-2">
        <label htmlFor={id} className="sr-only">
          {`${view.title} to ${harness}`}
        </label>
        <Textarea
          id={id}
          rows={3}
          value={draft}
          onChange={(e) => onDraft(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder={lead ? `Answer ${harness}` : `Message ${harness}`}
          aria-describedby={hintId}
          aria-invalid={long || undefined}
          className="min-h-20 resize-y"
        />
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <p id={hintId} aria-live="polite" className="min-w-0 flex-1 font-mono text-meta text-muted">
            {view.busy ? (
              busyHint(harness)
            ) : long ? (
              <InlineError inline>{`Too long: ${harness} takes at most ${MAX_MESSAGE_BYTES / 1024} KiB from Stage.`}</InlineError>
            ) : (
              <>
                <span className="max-md:hidden">
                  <Kbd>Ctrl Enter</Kbd> sends.{" "}
                </span>
                {`Continues the same ${harness} session.`}
              </>
            )}
          </p>
          <Button type="submit" variant={lead ? "primary" : "secondary"} busy={sending} disabled={!ready} title={view.busy ? busyHint(harness) : undefined} className="max-md:w-full">
            Send
          </Button>
        </div>
        <AnswerError error={error} />
      </form>
    </Panel>
  );
}
