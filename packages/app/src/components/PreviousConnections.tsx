import { useState } from "react";
import { useScopeMachineId } from "../api/harnessScope.tsx";
import { useForgetConnection, useHarnesses } from "../api/queries.ts";
import type { Harness } from "../api/types.ts";
import { dateTime, relativeTime } from "../lib/format.ts";
import { forgetPrompt, previousConnections, usageText } from "../lib/harnesses.ts";
import { Alert, Button, ErrorText, cx } from "./ui.tsx";

/** Settings: a harness's replaced, not forgotten connections on the harness scope's machine, each with Forget behind an inline confirm. Hidden when none. */
export function PreviousConnections({ harness, className }: { harness: Harness; className?: string }) {
  const { list, kindName } = useHarnesses();
  const forget = useForgetConnection();
  const [confirm, setConfirm] = useState<number | null>(null);
  const previous = previousConnections(list, harness, useScopeMachineId());
  if (previous.length === 0) return null;
  return (
    <section className={cx("flex min-w-0 flex-col gap-2", className)} aria-label={`Previous ${kindName(harness)} connections`}>
      <h3 className="label-caps">Previous</h3>
      <ul className="border-y border-line">
        {previous.map((c) => (
          <li key={c.id} className="flex flex-col gap-2 border-b border-line py-2 last:border-b-0">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <div className="flex min-w-0 flex-1 flex-col">
                <code className="font-mono text-detail break-all text-fg">{c.label || `connection #${c.id}`}</code>
                <span className="font-mono text-meta text-muted">
                  {c.retiredAt === null ? "replaced" : <span title={dateTime(c.retiredAt)}>replaced {relativeTime(c.retiredAt)}</span>}
                  {" · "}
                  <span className="tabular">{usageText(c)}</span>
                </span>
              </div>
              <Button small variant="danger" disabled={confirm === c.id} onClick={() => {
                  forget.reset();
                  setConfirm(c.id);
                }}>
                Forget
              </Button>
            </div>
            {confirm === c.id ? (
              <Alert tone="bad">
                <p>{forgetPrompt(c)}</p>
                <div className="mt-2 flex flex-wrap gap-2">
                  <Button
                    small
                    variant="danger"
                    busy={forget.isPending}
                    onClick={() => forget.mutate(c.id, { onSuccess: () => setConfirm(null) })}
                  >
                    Forget
                  </Button>
                  <Button small variant="ghost" autoFocus disabled={forget.isPending} onClick={() => setConfirm(null)}>
                    Cancel
                  </Button>
                </div>
              </Alert>
            ) : null}
            {confirm === c.id ? <ErrorText error={forget.error} /> : null}
          </li>
        ))}
      </ul>
    </section>
  );
}
