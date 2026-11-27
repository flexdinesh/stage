import { useMutation } from "@tanstack/react-query";
import { useRef, useState } from "react";
import type { ActionResult, HarnessAction } from "../api/types.ts";
import { HarnessFacts } from "./HarnessPresentation.tsx";
import { BlockedReason } from "./LaunchGate.tsx";
import { Button, ConfirmInline, ErrorText, ExternalLink, Hint } from "./ui.tsx";

type RunAction = (id: string, input: Record<string, unknown>, confirmed: boolean) => Promise<ActionResult>;

/**
 * Session actions without input as task toolbar buttons (beside Open and
 * Stop). An unavailable one stays visible, disabled, with the harness's
 * reason. Its confirmation and outcome take a full row at the end of the
 * toolbar, so the buttons never shift.
 */
export function SessionToolbarActions({ actions, run }: { actions: readonly HarnessAction[]; run: RunAction }) {
  const [confirming, setConfirming] = useState<string | null>(null);
  const inFlight = useRef(false);
  const triggers = useRef(new Map<string, HTMLButtonElement>());
  const mutation = useMutation({
    mutationFn: ({ id, confirmed }: { id: string; confirmed: boolean }) => run(id, {}, confirmed),
    onSettled: () => { inFlight.current = false; },
  });
  if (actions.length === 0) return null;
  const pending = actions.find((action) => action.id === confirming);
  const dispatch = (action: HarnessAction, confirmed: boolean) => {
    if (inFlight.current || !action.availability.available) return;
    inFlight.current = true;
    setConfirming(null);
    mutation.mutate({ id: action.id, confirmed });
    if (confirmed) triggers.current.get(action.id)?.focus();
  };
  const press = (action: HarnessAction) => {
    if (inFlight.current) return;
    mutation.reset();
    if (action.destructive || action.confirm) setConfirming(action.id);
    else dispatch(action, false);
  };
  const cancel = () => {
    if (pending !== undefined) triggers.current.get(pending.id)?.focus();
    setConfirming(null);
  };
  const result = mutation.data;
  return (
    <>
      {actions.map((action) => {
        const reason = action.availability.available ? undefined : action.availability.reason || "This action is unavailable.";
        const running = mutation.isPending && mutation.variables.id === action.id;
        return (
          <BlockedReason key={action.id} reason={reason} showOnPhone className="max-md:flex-1">
            <Button
              ref={(el) => { if (el === null) triggers.current.delete(action.id); else triggers.current.set(action.id, el); }}
              variant={action.primary ? "primary" : "secondary"}
              busy={running}
              disabled={reason !== undefined || (mutation.isPending && !running)}
              aria-expanded={action.destructive || action.confirm ? confirming === action.id : undefined}
              onClick={() => press(action)}
              // Blocked, BlockedReason's column holds the button and its reason on a phone: fill its width, never flex its height.
              className={reason === undefined ? "max-md:flex-1" : "max-md:w-full"}
            >
              {action.label}
            </Button>
          </BlockedReason>
        );
      })}
      {pending === undefined ? null : (
        <div className="order-last basis-full">
          <ConfirmInline danger={pending.destructive} confirmLabel={pending.label}
            disabled={!pending.availability.available}
            onConfirm={() => dispatch(pending, true)} onCancel={cancel}>
            {pending.confirm || `Run ${pending.label}?`}
          </ConfirmInline>
        </div>
      )}
      <div role="status" className="order-last flex basis-full flex-col gap-1 empty:hidden">
        {mutation.error ? <ErrorText error={mutation.error} /> : null}
        {result?.message ? <Hint className="wrap-anywhere">{result.message}</Hint> : null}
        {result?.link ? <ExternalLink href={result.link}>Open result</ExternalLink> : null}
        {result?.facts?.length ? <HarnessFacts facts={result.facts} /> : null}
        {result?.journalError ? <ErrorText error={result.journalError} /> : null}
      </div>
    </>
  );
}
