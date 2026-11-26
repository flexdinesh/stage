import { useMutation } from "@tanstack/react-query";
import { useEffect, useRef, useState, type ReactNode } from "react";
import type { HarnessFact, HarnessStatus, ReplacedConnection } from "../api/types.ts";
import { connectionStatusChip } from "../lib/connections.ts";
import { usageText } from "../lib/harnesses.ts";
import { Alert, Button, Chip, CommandBox, ConfirmInline, CopyButton, ErrorText, ExternalLink, Hint, cx } from "./ui.tsx";

export function HarnessStatusChip({ status }: { status: Pick<HarnessStatus, "phase" | "state"> }) {
  const { tone, label } = connectionStatusChip(status);
  return <Chip tone={tone} dot>{label}</Chip>;
}

const factTone = { neutral: "text-fg", ok: "text-ok", warn: "text-need-fg", bad: "text-bad" };

export function HarnessFacts({ facts, offline = false }: { facts?: readonly HarnessFact[] | undefined; offline?: boolean }) {
  if (facts === undefined || facts.length === 0) return null;
  return (
    <div className="min-w-0">
      {offline ? <Hint className="mb-1">Last known facts · machine offline</Hint> : null}
      <dl className="divide-y divide-line border-y border-line">
        {facts.map((fact) => (
          <div key={fact.id} className="grid min-w-0 grid-cols-[minmax(5rem,1fr)_minmax(0,2fr)] items-baseline gap-2 py-1.5">
            <dt className="text-meta wrap-anywhere text-muted">{fact.label}</dt>
            <dd className={cx("flex min-w-0 items-start gap-2 font-mono text-detail", factTone[fact.tone ?? "neutral"])}>
              <span className="min-w-0 flex-1 wrap-anywhere">{fact.link ? <ExternalLink href={fact.link}>{fact.value}</ExternalLink> : fact.value}</span>
              {fact.copyable ? <CopyButton text={fact.value} label="Copy" accessibleLabel={`Copy ${fact.label}`} className="shrink-0" /> : null}
            </dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

export function HarnessFix({ fix, reason }: { fix?: HarnessStatus["fix"]; reason?: string | undefined }) {
  if (!fix?.text && !fix?.command && !reason) return null;
  return (
    <div className="flex min-w-0 flex-col gap-2">
      {reason ? <Hint className="wrap-anywhere">{reason}</Hint> : null}
      {fix?.text && fix.text !== reason ? <p className="text-small wrap-anywhere">{fix.text}</p> : null}
      {fix?.command ? <CommandBox command={fix.command} /> : null}
    </div>
  );
}

export function ReplacedConnectionNotice({ replaced }: { replaced?: ReplacedConnection | null | undefined }) {
  if (replaced === undefined || replaced === null) return null;
  const usage = usageText(replaced);
  return (
    <div role="status">
      <Alert tone="plain">
        {replaced.label || `Connection #${replaced.id}`} moved to Previous connections{usage === null ? "" : ` (${usage})`}. Its tasks and history stay.
      </Alert>
    </div>
  );
}

export function CheckConnection({ run, reason }: { run: () => Promise<unknown>; reason?: string | undefined }) {
  const inFlight = useRef(false);
  const check = useMutation({
    mutationFn: run,
    onSettled: () => { inFlight.current = false; },
  });
  return (
    <div className="flex min-w-0 flex-col items-start gap-2">
      <Button small busy={check.isPending} disabled={reason !== undefined} onClick={() => {
        if (inFlight.current || reason !== undefined) return;
        inFlight.current = true;
        check.mutate();
      }}>
        Check again
      </Button>
      {reason === undefined ? null : <Hint className="wrap-anywhere">{reason}</Hint>}
      <ErrorText error={check.error} />
    </div>
  );
}

export interface ConnectionConfirmation {
  label?: string;
  prompt: string;
  busy: boolean;
  error: unknown;
  run: (done: () => void) => void;
  reset: () => void;
}

/** Shared inline disconnect and forget confirmation. */
export function ConfirmConnectionAction({ action, accessibleLabel, children }: { action: ConnectionConfirmation; accessibleLabel?: string; children?: ReactNode }) {
  const [confirm, setConfirm] = useState(false);
  const inFlight = useRef(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const label = action.label ?? "Disconnect";
  const close = () => {
    setConfirm(false);
    trigger.current?.focus();
  };
  useEffect(() => {
    if (!action.busy && action.error !== null && action.error !== undefined) inFlight.current = false;
  }, [action.busy, action.error]);
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div className="flex flex-wrap items-start gap-2">
        {children}
        <Button ref={trigger} small variant="ghost" aria-label={accessibleLabel} aria-expanded={confirm} onClick={() => {
          if (action.busy) return;
          action.reset();
          setConfirm(true);
        }}>
          {label}
        </Button>
      </div>
      {confirm ? (
        <ConfirmInline danger confirmLabel={label} busy={action.busy} error={action.error} onConfirm={() => {
          if (inFlight.current) return;
          inFlight.current = true;
          action.run(() => {
            inFlight.current = false;
            close();
          });
        }} onCancel={close}>
          {action.prompt}
        </ConfirmInline>
      ) : null}
    </div>
  );
}
