import { useEffect, useRef, useState } from "react";
import { dateTime } from "../../lib/format.ts";
import { usePlatform } from "../../platform/context.tsx";
import { Alert, Button, Field, Input, Muted } from "../ui.tsx";

export interface IssuedSecret {
  value: string;
  expiresAt: string;
  label: "Invitation link" | "Reset link" | "Agent credential";
}

/** The issuing page owns the only UI copy. A successful copy or dismissal releases it. */
export function OneTimeSecret({ secret, onClear, hint }: { secret: IssuedSecret | null; onClear: () => void; hint: string }) {
  const { clipboard } = usePlatform();
  const [result, setResult] = useState<"idle" | "copied" | "failed">("idle");
  const [busy, setBusy] = useState(false);
  const field = useRef<HTMLInputElement>(null);
  const status = useRef<HTMLParagraphElement>(null);
  const [previous, setPrevious] = useState(secret);
  if (previous !== secret) {
    setPrevious(secret);
    if (secret !== null) setResult("idle");
  }
  useEffect(() => {
    if (secret !== null) field.current?.focus();
    if (secret === null && result === "copied") status.current?.focus();
  }, [secret, result]);

  async function copy(issued: IssuedSecret) {
    setBusy(true);
    try {
      await clipboard.writeText(issued.value);
      setResult("copied");
      onClear();
    } catch {
      setResult("failed");
    } finally {
      setBusy(false);
    }
  }

  if (secret === null) {
    return result === "copied"
      ? <p ref={status} tabIndex={-1} role="status" className="text-small text-fg">Copied to clipboard.</p>
      : null;
  }
  return (
    <div className="flex min-w-0 flex-col gap-2.5 border border-line bg-panel p-3">
      <Field label={secret.label}>
        {(id) => <Input id={id} ref={field} type="text" value={secret.value} readOnly autoComplete="off"
          onFocus={(e) => e.currentTarget.select()} />}
      </Field>
      <Muted>{hint} Expires {dateTime(secret.expiresAt)}.</Muted>
      {result === "failed" ? <Alert tone="bad">Couldn’t copy. Select the text to copy it manually.</Alert> : null}
      <div className="flex flex-wrap gap-2">
        <Button variant="primary" busy={busy} onClick={() => void copy(secret)}>Copy {secret.label.toLowerCase()}</Button>
        <Button variant="ghost" disabled={busy} onClick={onClear}>Dismiss</Button>
      </div>
    </div>
  );
}
