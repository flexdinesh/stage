// T3 onboarding: the setup screen that is the Dashboard until the first full
// sync, the Dashboard alert for later trouble, and the pairing, T3 URL and
// disconnect controls Settings shares. Logic lives in lib/onboarding.ts.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent, type ReactNode } from "react";
import { keys, useT3Status } from "../api/queries.ts";
import type { ReplacedConnection, T3Status } from "../api/types.ts";
import { replacedNotice } from "../lib/harnesses.ts";
import { dateTime, relativeTime, shortDate } from "../lib/format.ts";
import { T3_STATE_TONE, codeReason, dashboardAlert, disconnectAvailability, primaryAction, setupSteps, t3Identity } from "../lib/onboarding.ts";
import { useApi } from "../platform/context.tsx";
import { AutoCompleteToggle } from "./Completion.tsx";
import { PreviousConnections } from "./PreviousConnections.tsx";
import { CommandBox, Step, Steps } from "./Steps.tsx";
import { Alert, Button, Chip, Detail, Details, ErrorText, Field, Input, Muted, Panel, QueryState } from "./ui.tsx";

/** Pair, auto-pair and set-URL answer with the new status: show it at once (and whether T3 now counts as configured). */
function useSetStatus(): (s: T3Status) => void {
  const qc = useQueryClient();
  return (s) => {
    qc.setQueryData(keys.t3Status, s);
    void qc.invalidateQueries({ queryKey: keys.connections });
  };
}

/** Re-reads the status; the server probes T3 again when its last probe is stale. */
function useRetry() {
  const qc = useQueryClient();
  return useMutation({ mutationFn: () => qc.refetchQueries({ queryKey: keys.t3Status }, { throwOnError: true }) });
}

function CodeForm({ primary, onPaired }: { primary: boolean; onPaired?: (s: T3Status) => void }) {
  const api = useApi();
  const setStatus = useSetStatus();
  const [code, setCode] = useState("");
  const pair = useMutation({
    mutationFn: () => api.pair({ code: code.trim() }),
    onSuccess: (s) => {
      setCode("");
      setStatus(s);
      onPaired?.(s);
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (code.trim() !== "") pair.mutate();
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-2">
      <div className="flex flex-wrap items-end gap-2">
        <Field label="Pairing code" className="min-w-52 flex-1">
          {(id) => (
            <Input
              id={id}
              value={code}
              onChange={(e) => setCode(e.target.value)}
              placeholder="ABCD-1234"
              autoComplete="off"
              spellCheck={false}
              className="font-mono"
            />
          )}
        </Field>
        <Button type="submit" variant={primary ? "primary" : "secondary"} busy={pair.isPending} disabled={code.trim() === ""}>
          Pair
        </Button>
      </div>
      <ErrorText error={pair.error} />
    </form>
  );
}

/**
 * Pairing: one click through the t3 CLI when T3 runs here (a pairing code is
 * the fallback), else the code field with the command that creates one.
 * `primary` is false where pairing is not the region's main action (Settings
 * while connected).
 */
export function PairActions({ status, primary = true }: { status: T3Status; primary?: boolean }) {
  const api = useApi();
  const setStatus = useSetStatus();
  const [showCode, setShowCode] = useState(false);
  // Pairing another T3 retires the previous connection: say so once, where the user paired.
  const [replaced, setReplaced] = useState<ReplacedConnection | null>(null);
  const onPaired = (s: T3Status) => setReplaced(s.replaced ?? null);
  const auto = useMutation({
    mutationFn: api.autoPair,
    onSuccess: (s) => {
      setStatus(s);
      onPaired(s);
    },
  });
  const notice = replaced === null ? null : <Alert tone="plain">{replacedNotice(replaced)}</Alert>;
  if (!status.autoPair.available) {
    return (
      <div className="flex flex-col gap-3">
        {notice}
        <Muted>{codeReason(status.autoPair.reason)}</Muted>
        {status.autoPair.reason === "fake" ? null : <CommandBox command="t3 pair" />}
        <CodeForm primary={primary} onPaired={onPaired} />
      </div>
    );
  }
  // A failed auto-pair opens the code fallback.
  const codeOpen = showCode || auto.isError;
  return (
    <div className="flex flex-col gap-3">
      {notice}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <Button variant={primary ? "primary" : "secondary"} busy={auto.isPending} onClick={() => auto.mutate()}>
          Pair with T3
        </Button>
        <span className="font-mono text-meta text-muted">uses the t3 CLI on this machine</span>
      </div>
      <ErrorText error={auto.error} />
      {codeOpen ? (
        <CodeForm primary={false} onPaired={onPaired} />
      ) : (
        <div>
          <Button variant="ghost" small onClick={() => setShowCode(true)}>
            Use a pairing code
          </Button>
        </div>
      )}
    </div>
  );
}

/** The T3 URL: editable (Save, Reset to default), or locked by the T3_URL env. */
export function T3UrlEditor({ status }: { status: T3Status }) {
  const api = useApi();
  const setStatus = useSetStatus();
  const [draft, setDraft] = useState(status.t3Url);
  const save = useMutation({
    mutationFn: (url: string | null) => api.setT3Url({ url }),
    onSuccess: (s) => {
      setDraft(s.t3Url);
      setStatus(s);
    },
  });
  if (status.t3UrlSource === "env") {
    return (
      <div className="flex flex-col gap-1.5">
        <span className="label-caps">T3 URL</span>
        <div className="flex min-h-10 items-center border border-line bg-panel px-2.5 md:min-h-8">
          <code className="font-mono text-detail break-all text-fg">{status.t3Url}</code>
        </div>
        <p className="font-mono text-meta text-muted">set by T3_URL</p>
      </div>
    );
  }
  const url = draft.trim();
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (url !== "" && url !== status.t3Url) save.mutate(url);
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-1.5">
      <div className="flex flex-wrap items-end gap-2">
        <Field label="T3 URL" className="min-w-60 flex-1">
          {(id) => (
            <Input
              id={id}
              type="url"
              inputMode="url"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              placeholder="http://127.0.0.1:3773"
              autoComplete="off"
              spellCheck={false}
              className="font-mono"
            />
          )}
        </Field>
        <div className="flex gap-2">
          <Button type="submit" busy={save.isPending && save.variables !== null} disabled={save.isPending || url === "" || url === status.t3Url}>
            Save
          </Button>
          {status.t3UrlSource === "settings" ? (
            <Button variant="ghost" busy={save.isPending && save.variables === null} disabled={save.isPending} onClick={() => save.mutate(null)}>
              Reset
            </Button>
          ) : null}
        </div>
      </div>
      <p className="font-mono text-meta text-muted">{status.t3UrlSource === "settings" ? "saved in stage" : "default"}</p>
      <ErrorText error={save.error} />
    </form>
  );
}

/**
 * Disconnect (Settings T3 connection footer): forgets the token after an
 * inline confirm; the environment binding stays, so pairing this T3 resumes it. Locked while T3_ADMIN_TOKEN
 * would mint a new token at once.
 */
export function DisconnectT3({ status }: { status: T3Status }) {
  const api = useApi();
  const setStatus = useSetStatus();
  const [confirm, setConfirm] = useState(false);
  const disconnect = useMutation({
    mutationFn: api.disconnectT3,
    onSuccess: (s) => {
      setConfirm(false);
      setStatus(s);
    },
  });
  const availability = disconnectAvailability(status);
  if (availability === "hidden") return null;
  const label = status.t3 === null || status.t3.label === "" ? null : status.t3.label;
  // `connected` is a valid token T3 has not rejected: only then does it outlive the disconnect.
  const expires = status.connected ? status.tokenExpiresAt : null;
  return (
    <div className="flex flex-col gap-2 border-t border-line pt-3">
      <div className="flex flex-wrap items-center justify-end gap-x-3 gap-y-1">
        {availability === "locked" ? <span className="font-mono text-meta text-muted">renewed by T3_ADMIN_TOKEN</span> : null}
        <Button small variant="danger" disabled={availability === "locked" || confirm} onClick={() => setConfirm(true)}>
          Disconnect
        </Button>
      </div>
      {confirm ? (
        <Alert tone="bad">
          <p>
            {label === null ? "Disconnect T3?" : `Disconnect from ${label}?`} Stage forgets its token and stops syncing. Pair this T3 again to
            resume, or pair another T3 to replace this connection: its tasks and bindings stay here, read-only.
            {expires === null ? null : ` The token stays valid in T3 until ${shortDate(expires)}.`}
          </p>
          <div className="mt-2 flex flex-wrap gap-2">
            <Button small variant="danger" busy={disconnect.isPending} onClick={() => disconnect.mutate()}>
              Disconnect
            </Button>
            <Button small variant="ghost" autoFocus disabled={disconnect.isPending} onClick={() => setConfirm(false)}>
              Cancel
            </Button>
          </div>
        </Alert>
      ) : null}
      <ErrorText error={disconnect.error} />
    </div>
  );
}

function SyncNow() {
  const api = useApi();
  const qc = useQueryClient();
  const sync = useMutation({
    mutationFn: api.sync,
    // The status refetch sees the first sync land and refreshes the views it fills.
    onSettled: () => qc.invalidateQueries({ queryKey: keys.t3Status }),
  });
  return (
    <div className="flex flex-col gap-3">
      <Muted>Paired. The first full sync has not run yet.</Muted>
      <div>
        <Button variant="primary" busy={sync.isPending} onClick={() => sync.mutate()}>
          Sync now
        </Button>
      </div>
      <ErrorText error={sync.error} />
    </div>
  );
}

/** Unreachable: the command that starts T3, Retry (when the region has none), and the URL to point stage elsewhere. */
function ReachFix({ status, retry }: { status: T3Status; retry: ReturnType<typeof useRetry> | null }) {
  return (
    <div className="flex flex-col gap-3">
      <Muted>
        {status.t3UrlSource === "env"
          ? "Start T3 on this machine, or change T3_URL and restart stage."
          : "Start T3 on this machine, or point stage at the T3 server."}
      </Muted>
      <CommandBox command="t3 serve" />
      {retry === null ? null : (
        <>
          <div>
            <Button variant="primary" busy={retry.isPending} onClick={() => retry.mutate()}>
              Retry
            </Button>
          </div>
          <ErrorText error={retry.error} />
        </>
      )}
      <div className="border-t border-line pt-3">
        <T3UrlEditor status={status} />
      </div>
    </div>
  );
}

/** Setup panel: the step rows, then the one fix for the current state. */
export function T3SetupPanel({ status }: { status: T3Status }) {
  const retry = useRetry();
  const action = primaryAction(status);
  let fix: ReactNode;
  switch (action) {
    case "retry":
      fix = (
        <>
          <Alert tone="bad">
            No T3 server answered at <span className="font-mono break-words">{status.t3Url}</span>.
            {status.probeError === null ? null : <span className="block font-mono text-meta">{status.probeError}</span>}
          </Alert>
          <ReachFix status={status} retry={retry} />
        </>
      );
      break;
    case "autoPair":
    case "code":
      fix = <PairActions status={status} />;
      break;
    case "sync":
      fix = <SyncNow />;
      break;
    case null:
      fix = <Muted>Refreshing your T3 projects and tracked tasks. The Dashboard fills in when it finishes.</Muted>;
      break;
  }
  return (
    <div className="flex max-w-[640px] flex-col gap-3.5">
      <Panel title="T3 setup" meta={<span className="break-all">{status.t3Url}</span>} flush>
        <Steps label="Setup steps">
          {setupSteps(status, retry.isPending).map((s) => (
            <Step key={s.key} label={s.key} note={s.note} status={s.status} tabular={s.key === "first sync"} />
          ))}
        </Steps>
      </Panel>
      {fix}
    </div>
  );
}

/** Trouble after the first sync: an Alert above the Dashboard with the same fix inline. */
export function T3Alert({ status }: { status: T3Status | undefined }) {
  const api = useApi();
  const setStatus = useSetStatus();
  const retry = useRetry();
  const auto = useMutation({ mutationFn: api.autoPair, onSuccess: setStatus });
  const [open, setOpen] = useState(false);
  const alert = dashboardAlert(status);
  if (alert === null || status === undefined) return null;
  const action = primaryAction(status);
  const toggle = (label: string) => (
    <Button small variant="ghost" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
      {open ? "Hide" : label}
    </Button>
  );
  const actions =
    action === "retry" ? (
      <>
        <Button small busy={retry.isPending} onClick={() => retry.mutate()}>
          Retry
        </Button>
        {toggle("Show fix")}
      </>
    ) : action === "autoPair" ? (
      <>
        <Button small busy={auto.isPending} onClick={() => auto.mutate()}>
          Pair with T3
        </Button>
        {toggle("Use a pairing code")}
      </>
    ) : (
      toggle("Pair with a code")
    );
  const fixOpen = open || auto.isError;
  return (
    <div className="mb-3.5 flex flex-col gap-2">
      <Alert tone={alert.tone} actions={actions}>
        {alert.message}
      </Alert>
      <ErrorText error={retry.error ?? auto.error} />
      {fixOpen ? (
        <Panel>
          <div className="max-w-[640px]">
            {action === "retry" ? (
              <ReachFix status={status} retry={null} />
            ) : action === "autoPair" ? (
              <CodeForm primary />
            ) : (
              <PairActions status={status} />
            )}
          </div>
        </Panel>
      ) : null}
    </div>
  );
}

const when = (iso: string | null) => (iso === null ? "-" : `${dateTime(iso)} (${relativeTime(iso)})`);

/** The T3 setup panel in Settings (registered for kind t3 in HarnessConnections). */
export function T3ConnectionPanel() {
  const status = useT3Status();
  return (
    <QueryState isPending={status.isPending} error={status.error}>
      {status.data === undefined ? null : (
        <>
          <StatusCard status={status.data} />
          <PairCard status={status.data} />
        </>
      )}
    </QueryState>
  );
}

function StatusCard({ status }: { status: T3Status }) {
  return (
    <Panel
      title="T3 connection"
      actions={
        <Chip tone={T3_STATE_TONE[status.state]} dot>
          {status.state}
        </Chip>
      }
    >
      <div className="flex flex-col gap-3">
        <Details>
          <Detail label="T3">{t3Identity(status) ?? (status.reachable ? "-" : <span className="text-bad">{status.probeError ?? "not reachable"}</span>)}</Detail>
          <Detail label="T3 URL">{status.t3Url}</Detail>
          <Detail label="Public URL">{status.publicUrl}</Detail>
          <Detail label="Environment">{status.environmentId ?? "-"}</Detail>
          <Detail label="Client id">{status.clientId ?? "-"}</Detail>
          <Detail label="Token expires">{when(status.tokenExpiresAt)}</Detail>
          <Detail label="Admin token">
            {status.adminTokenConfigured ? "configured (auto-renew)" : "not configured"}
          </Detail>
          <Detail label="Last full sync">{when(status.lastFullSyncAt)}</Detail>
          <Detail label="Last error">
            {status.lastError === null ? "-" : <span className="text-bad">{status.lastError}</span>}
          </Detail>
        </Details>
        <AutoCompleteToggle harness="t3" className="border-t border-line pt-2" />
        <DisconnectT3 status={status} />
        <PreviousConnections harness="t3" className="border-t border-line pt-3" />
      </div>
    </Panel>
  );
}

/** Pairing (auto through the t3 CLI, or a code) and the T3 URL. */
function PairCard({ status }: { status: T3Status }) {
  const paired = status.state === "connected";
  return (
    <Panel title="Pair" meta={paired ? "paired · pair again to replace the token" : status.state === "unpaired" ? "not paired" : status.state}>
      <div className="flex flex-col gap-4">
        <PairActions status={status} primary={!paired} />
        <section className="flex flex-col gap-3 border-t border-line pt-3">
          <T3UrlEditor status={status} />
        </section>
      </div>
    </Panel>
  );
}

