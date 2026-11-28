import { useId, type ReactNode } from "react";
import { ApiError } from "../../api/client.ts";
import type { Delivery } from "../../api/types.ts";
import { deliveryLabel, deliveryReceipt, isSendKey, sendable, tooLong } from "../../lib/sessionComposer.ts";
import { AnswerError } from "../Requests.tsx";
import { Button, InlineError, Kbd, Panel, RelTime, Textarea } from "../ui.tsx";

export function SessionComposer({
  harness, buttons, reasons, draft, onDraft, onSend, pending, result, destination,
}: {
  harness: string;
  buttons: readonly Delivery[];
  reasons: readonly string[];
  draft: string;
  onDraft: (text: string) => void;
  onSend: (delivery: Delivery) => void;
  pending?: Delivery | undefined;
  result?: { receipt?: { delivery: Delivery; at: string }; error?: unknown } | null;
  destination?: ReactNode;
}) {
  const id = useId();
  const hintId = `${id}-hint`;
  const primary = buttons[0];
  const long = tooLong(draft);
  const ready = pending === undefined && sendable(draft);
  const receipt = result?.receipt;
  const error = result?.error;
  return (
    <Panel
      title="Message"
      meta={receipt === undefined ? undefined : (
        <span role="status">{deliveryReceipt[receipt.delivery]} · <RelTime at={receipt.at} /></span>
      )}
    >
      <form className="flex flex-col gap-2.5" onSubmit={(event) => {
        event.preventDefault();
        if (primary !== undefined && ready) onSend(primary);
      }}>
        <label htmlFor={id} className="sr-only">Message {harness}</label>
        <Textarea
          id={id}
          rows={3}
          value={draft}
          onChange={(event) => onDraft(event.target.value)}
          onKeyDown={(event) => {
            if (!isSendKey(event.nativeEvent)) return;
            event.preventDefault();
            if (primary !== undefined && ready) onSend(primary);
          }}
          placeholder={`Message ${harness}`}
          aria-describedby={hintId}
          aria-invalid={long || undefined}
          className="min-h-20 resize-y"
        />
        <div className="flex flex-wrap items-center gap-2.5">
          <div id={hintId} className="min-w-40 flex-1 text-small text-muted" aria-live="polite">
            {long ? <InlineError inline>Message exceeds 32 KiB.</InlineError> : null}
            {reasons.map((reason) => <p key={reason}>{reason}</p>)}
            {primary === undefined ? destination : (
              <p className="font-mono text-meta max-md:hidden"><Kbd>Ctrl / ⌘ Enter</Kbd> · {deliveryLabel[primary]}</p>
            )}
          </div>
          {buttons.length === 0 ? null : (
            <div className="flex gap-2 max-sm:w-full">
              {buttons.map((delivery) => (
                <Button
                  key={delivery}
                  type={delivery === primary ? "submit" : "button"}
                  variant={delivery === primary ? "primary" : "secondary"}
                  busy={pending === delivery}
                  disabled={!ready}
                  onClick={delivery === primary ? undefined : () => { if (ready) onSend(delivery); }}
                  className="max-sm:flex-1"
                >
                  {deliveryLabel[delivery]}
                </Button>
              ))}
            </div>
          )}
        </div>
        {error instanceof ApiError && error.status === 409 && error.code === "session_busy"
          ? <InlineError inline><span role="status">{error.message}</span></InlineError>
          : <AnswerError error={error} />}
      </form>
    </Panel>
  );
}
