import { useMutation } from "@tanstack/react-query";
import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { AcceptanceRequest, CompletionPreview, Task } from "../api/types.ts";
import { useInvalidateWork } from "../api/queries.ts";
import { useApi } from "../platform/context.tsx";
import { autoStarts, startsText } from "../lib/completion.ts";
import { endLinger, nextLingerEnd, pruneLinger, startLinger, type Linger } from "../lib/dashboard.ts";
import { noteRequest } from "../lib/journal.ts";
import { Button, InlineError, errorText } from "./ui.tsx";

/** Rows completed here stay in place (completed chip + Undo) while they linger; outside a provider nothing lingers. */
interface CompletionLingerApi {
  lingering: ReadonlySet<number>;
  completed: (id: number) => void;
  reopened: (id: number) => void;
}

const LingerContext = createContext<CompletionLingerApi>({ lingering: new Set(), completed: () => {}, reopened: () => {} });

export const useCompletionLinger = () => useContext(LingerContext);

/** Holds just-completed rows for LINGER_MS and announces completions and reopens in a polite live region. */
export function CompletionLinger({ children }: { children: ReactNode }) {
  const [linger, setLinger] = useState<Linger>(() => new Map());
  const [said, setSaid] = useState("");
  useEffect(() => {
    const end = nextLingerEnd(linger);
    if (end === null) return;
    const timer = setTimeout(() => setLinger((m) => pruneLinger(m, Date.now())), Math.max(0, end - Date.now()));
    return () => clearTimeout(timer);
  }, [linger]);
  const value = useMemo<CompletionLingerApi>(
    () => ({
      lingering: new Set(linger.keys()),
      completed: (id) => {
        setLinger((m) => startLinger(m, id, Date.now()));
        setSaid(`Completed #${id}`);
      },
      reopened: (id) => {
        setLinger((m) => endLinger(m, id));
        setSaid(`#${id} back in review`);
      },
    }),
    [linger],
  );
  return (
    <LingerContext.Provider value={value}>
      {children}
      <span className="sr-only" aria-live="polite">
        {said}
      </span>
    </LingerContext.Provider>
  );
}

/** Reuses the last request (same requestId) while the preview it was built from is unchanged, so retries stay idempotent. */
function completeBody(previous: AcceptanceRequest | null, p: CompletionPreview): AcceptanceRequest {
  if (previous !== null && previous.revision === p.revision && previous.evidenceVersion === p.evidenceVersion) return previous;
  return { requestId: noteRequest(null, "acceptance", "decision").requestId, revision: p.revision, evidenceVersion: p.evidenceVersion, note: "" };
}

/**
 * One-click Complete for a review row: fetches the preview; a block is the error; auto-starting dependents ask for an
 * inline confirm first; otherwise it completes with an empty note.
 */
export function useQuickComplete(task: Task) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const linger = useCompletionLinger();
  const [pending, setPending] = useState<CompletionPreview | null>(null);
  const last = useRef<AcceptanceRequest | null>(null);
  const submit = useMutation({
    mutationFn: (p: CompletionPreview) => {
      const body = completeBody(last.current, p);
      last.current = body;
      return api.completeTask(task.id, body);
    },
    onSuccess: async () => {
      setPending(null);
      linger.completed(task.id);
      await invalidate();
    },
  });
  const check = useMutation({
    mutationFn: async () => {
      const p = await api.completionPreview(task.id);
      if (p.blockedReason !== "") throw new Error(p.blockedReason);
      return p;
    },
    onMutate: () => submit.reset(),
    onSuccess: (p) => {
      if (autoStarts(p, task).length > 0) setPending(p);
      else submit.mutate(p);
    },
  });
  return {
    start: () => check.mutate(),
    /** Set while auto-starting dependents wait for a confirm. */
    confirm:
      pending === null
        ? null
        : { starts: startsText(autoStarts(pending, task)), submit: () => submit.mutate(pending), cancel: () => setPending(null) },
    busy: check.isPending || submit.isPending,
    error: check.error ?? submit.error,
  };
}

/** Undo of a lingering completion: reopens at the fresh preview revision. */
export function useUndoComplete(task: Task) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const linger = useCompletionLinger();
  return useMutation({
    mutationFn: async () => {
      const p = await api.completionPreview(task.id);
      return api.reopenTask(task.id, { requestId: noteRequest(null, "acceptance", "decision").requestId, revision: p.revision });
    },
    // Refresh first so the row stays put until the reopened task is back in Awaiting review.
    onSuccess: async () => {
      await invalidate();
      linger.reopened(task.id);
    },
  });
}

interface Failure {
  id: number;
  error: unknown;
}
type Planned = { task: Task; preview: CompletionPreview };
type Checked = ({ ok: true } & Planned) | ({ ok: false } & Failure);

/** Awaiting review header "Complete all n": previews every listed review row, confirms inline, completes in turn. */
export function useCompleteAll(tasks: readonly Task[]) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const linger = useCompletionLinger();
  const [plan, setPlan] = useState<Planned[] | null>(null);
  const [failures, setFailures] = useState<Failure[]>([]);
  const prepare = useMutation({
    mutationFn: (list: readonly Task[]) =>
      Promise.all(
        list.map(async (task): Promise<Checked> => {
          try {
            const preview = await api.completionPreview(task.id);
            return preview.blockedReason === ""
              ? { ok: true, task, preview }
              : { ok: false, id: task.id, error: new Error(preview.blockedReason) };
          } catch (error) {
            return { ok: false, id: task.id, error };
          }
        }),
      ),
    onSuccess: (checked) => {
      const ready = checked.flatMap((c) => (c.ok ? [{ task: c.task, preview: c.preview }] : []));
      setFailures(checked.flatMap((c) => (c.ok ? [] : [{ id: c.id, error: c.error }])));
      setPlan(ready.length === 0 ? null : ready);
    },
  });
  const run = useMutation({
    mutationFn: async (items: readonly Planned[]) => {
      const failed: Failure[] = [];
      const done: number[] = [];
      for (const { task, preview } of items) {
        try {
          await api.completeTask(task.id, completeBody(null, preview));
          done.push(task.id);
        } catch (error) {
          failed.push({ id: task.id, error });
        }
      }
      // Linger starts when the batch ends, so early rows never expire before the refresh lands.
      for (const id of done) linger.completed(id);
      return failed;
    },
    onSuccess: async (failed) => {
      setPlan(null);
      setFailures(failed);
      await invalidate();
    },
  });
  return {
    tasks,
    plan,
    failures,
    starts: plan === null ? "" : startsText(plan.flatMap((p) => autoStarts(p.preview, p.task))),
    busy: prepare.isPending || run.isPending,
    running: run.isPending,
    start: () => {
      setFailures([]);
      prepare.mutate(tasks);
    },
    confirm: () => {
      if (plan !== null) run.mutate(plan);
    },
    cancel: () => setPlan(null),
    dismiss: () => setFailures([]),
  };
}

type CompleteAll = ReturnType<typeof useCompleteAll>;

/** Header actions: "Complete all n", then the inline confirm. Shown from two listed review rows (or mid-run). */
export function CompleteAllActions({ state: s }: { state: CompleteAll }) {
  if (s.plan !== null)
    return (
      <>
        <span className="font-mono text-meta text-fg tabular">
          Complete {s.plan.length} task{s.plan.length === 1 ? "" : "s"}?
        </span>
        {s.starts === "" ? null : <span className="font-mono text-meta text-muted tabular">{s.starts}</span>}
        <Button small variant="primary" busy={s.running} onClick={s.confirm}>
          Complete
        </Button>
        <Button small variant="ghost" disabled={s.running} onClick={s.cancel}>
          Cancel
        </Button>
      </>
    );
  if (s.tasks.length < 2) return null;
  return (
    <Button small variant="ghost" busy={s.busy} onClick={s.start}>
      Complete all <span className="tabular">{s.tasks.length}</span>
    </Button>
  );
}

/** Tasks Complete all could not complete, each with its error; above the rows. */
export function CompleteAllFailures({ state: s }: { state: CompleteAll }) {
  if (s.failures.length === 0) return null;
  return (
    <div className="flex items-start gap-2 border-b border-line px-3 py-2">
      <ul className="flex min-w-0 flex-1 flex-col gap-0.5">
        {s.failures.map((f) => (
          <li key={f.id}>
            <InlineError>
              #{f.id}: {errorText(f.error)}
            </InlineError>
          </li>
        ))}
      </ul>
      <Button small variant="ghost" onClick={s.dismiss}>
        Dismiss
      </Button>
    </div>
  );
}
