// T3 setup (Settings > Connections, in the Manage drawer): pairing, settings and
// disconnect, and the status card once paired. Logic lives in onboarding.ts.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent, type ReactNode } from "react";
import { invalidateConnectionChange } from "../../api/queries.ts";
import type { HarnessFact, ReplacedConnection } from "../../api/types.ts";
import { dateTime, relativeTime, shortDate } from "../../lib/format.ts";
import { useHarnessApi, useScopedKey, useSetupCopy } from "../../api/harnessScope.tsx";
import { useApi } from "../../platform/context.tsx";
import { AutoCompleteToggle } from "../../components/Completion.tsx";
import { NewTaskDefaults } from "../../components/NewTaskDefaults.tsx";
import { Step, Steps } from "../../components/Steps.tsx";
import { CheckConnection, ConfirmConnectionAction, HarnessFacts, HarnessFix, HarnessStatusChip, ReplacedConnectionNotice } from "../../components/HarnessPresentation.tsx";
import { Alert, Button, ErrorText, Field, Hint, Input, Muted, Panel, QueryState } from "../../components/ui.tsx";
import { T3_KIND, t3Api } from "./api.ts";
import { codeReason, disconnectAvailability, primaryAction, savedBrowserUrl, setupSteps, t3Configured, t3DisconnectPrompt, t3SettingsChanges } from "./onboarding.ts";
import { T3_STATUS_KEY, useT3Status } from "./queries.ts";
import type { T3SettingsRequest, T3Status } from "./types.ts";

/** Pair, auto-pair and set-URL answer with the new status: show it at once (and whether T3 now counts as configured). */
function useSetStatus(): (s: T3Status) => void {
  const qc = useQueryClient();
  const scoped = useScopedKey();
  return (s) => {
    qc.setQueryData(scoped(T3_STATUS_KEY), s);
    void invalidateConnectionChange(qc, scoped);
  };
}

/** Re-reads the status; the server probes T3 again when its last probe is stale. */
function useRetry() {
  const qc = useQueryClient();
  const key = useScopedKey()(T3_STATUS_KEY);
  return useMutation({ mutationFn: () => qc.refetchQueries({ queryKey: key, exact: true }, { throwOnError: true }) });
}

function CodeForm({ primary, onPaired }: { primary: boolean; onPaired?: (s: T3Status) => void }) {
  const api = t3Api(useHarnessApi());
  const setStatus = useSetStatus();
  const [code, setCode] = useState("");
  const pair = useMutation({
    mutationFn: (value: string) => api.pair({ code: value }),
    onSuccess: (s) => {
      setCode("");
      setStatus(s);
      onPaired?.(s);
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (code.trim() === "" || pair.isPending) return;
    pair.mutate(code.trim());
    setCode("");
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
          Pair with code
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
export function PairActions({ status, primary = true, showFix = true }: { status: T3Status; primary?: boolean; showFix?: boolean }) {
  const api = t3Api(useHarnessApi());
  const copy = useSetupCopy();
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
  const notice = <ReplacedConnectionNotice replaced={replaced} />;
  if (!status.autoPair.available) {
    return (
      <div className="flex flex-col gap-3">
        {notice}
        <Muted>{codeReason(status.autoPair.reason)}</Muted>
        {!showFix || status.autoPair.reason === "fake" ? null : <HarnessFix fix={{ text: "Generate a pairing code on the T3 host.", command: "t3 pair" }} />}
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
          Pair automatically
        </Button>
        <span className="font-mono text-meta text-muted">uses the t3 CLI on {copy.machine}</span>
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

type SettingsSave = { which: "save" | "resetUrl" | "clearToken"; body: T3SettingsRequest };

/**
 * T3 settings: the T3 URL (Reset to default), and unless `urlOnly` the Browser URL and the admin token. Save sends
 * only the fields that changed.
 */
export function T3SettingsForm({ status, urlOnly = false }: { status: T3Status; urlOnly?: boolean }) {
  const api = t3Api(useHarnessApi());
  const setStatus = useSetStatus();
  const [url, setUrl] = useState(status.t3Url);
  const [browser, setBrowser] = useState(savedBrowserUrl(status));
  const [token, setToken] = useState("");
  const save = useMutation({
    mutationFn: (v: SettingsSave) => api.saveSettings(v.body),
    onSuccess: (s) => {
      setUrl(s.t3Url);
      setBrowser(savedBrowserUrl(s));
      setToken("");
      setStatus(s);
    },
  });
  // urlOnly leaves the other fields as saved, so they never send.
  const body = t3SettingsChanges(status, urlOnly ? { url, browserUrl: savedBrowserUrl(status), adminToken: "" } : { url, browserUrl: browser, adminToken: token });
  const changed = Object.keys(body).length > 0;
  const busy = (which: SettingsSave["which"]) => save.isPending && save.variables?.which === which;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!changed || save.isPending) return;
    save.mutate({ which: "save", body });
    setToken("");
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-3">
      <div className="flex flex-col gap-1.5">
        <div className="flex flex-wrap items-end gap-2">
          <Field label="T3 URL" className="min-w-60 flex-1">
            {(id) => (
              <Input
                id={id}
                type="url"
                inputMode="url"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                placeholder="http://127.0.0.1:3773"
                autoComplete="off"
                spellCheck={false}
                className="font-mono"
              />
            )}
          </Field>
          {status.t3UrlSource === "settings" ? (
            <Button variant="ghost" busy={busy("resetUrl")} disabled={save.isPending} onClick={() => save.mutate({ which: "resetUrl", body: { url: null } })}>
              Reset
            </Button>
          ) : null}
        </div>
        <Hint>{status.t3UrlSource === "settings" ? "Saved in stage." : "Default."}</Hint>
      </div>
      {urlOnly ? null : (
        <>
          <Field label="Browser URL" hint="Used for Open in T3 links. Empty uses the T3 URL.">
            {(id) => (
              <Input
                id={id}
                type="url"
                inputMode="url"
                value={browser}
                onChange={(e) => setBrowser(e.target.value)}
                placeholder={status.t3Url}
                autoComplete="off"
                spellCheck={false}
                className="font-mono"
              />
            )}
          </Field>
          <div className="flex flex-col gap-1.5">
            <div className="flex flex-wrap items-end gap-2">
              <Field label="Admin token" className="min-w-60 flex-1">
                {(id) => (
                  <Input
                    id={id}
                    type="password"
                    value={token}
                    onChange={(e) => setToken(e.target.value)}
                    placeholder={status.adminTokenConfigured ? "saved" : undefined}
                    autoComplete="new-password"
                    spellCheck={false}
                    className="font-mono"
                  />
                )}
              </Field>
              {status.adminTokenConfigured ? (
                <Button variant="ghost" busy={busy("clearToken")} disabled={save.isPending} onClick={() => save.mutate({ which: "clearToken", body: { adminToken: null } })}>
                  Clear
                </Button>
              ) : null}
            </div>
            <Hint>{status.adminTokenConfigured ? "Saved. Stage renews its token automatically; type a new one to replace it." : "Optional. Lets Stage renew its token automatically."}</Hint>
          </div>
        </>
      )}
      <div>
        <Button type="submit" busy={busy("save")} disabled={save.isPending || !changed}>
          Save
        </Button>
      </div>
      <ErrorText error={save.error} />
    </form>
  );
}

/** Disconnect (Settings T3 connection footer): forgets the token and settings after an inline confirm; pairing this T3 again resumes. */
export function DisconnectT3({ status }: { status: T3Status }) {
  const disconnect = useDisconnectT3();
  if (disconnectAvailability(status) === "hidden") return null;
  return <ConfirmConnectionAction action={{
    prompt: t3DisconnectPrompt(status, shortDate),
    busy: disconnect.isPending,
    error: disconnect.error,
    run: (done) => disconnect.mutate(undefined, { onSuccess: done }),
    reset: disconnect.reset,
  }} />;
}

/** Disconnects T3 on the harness scope's machine and shows the new status at once. */
export function useDisconnectT3(onDone?: () => void) {
  const api = t3Api(useHarnessApi());
  const setStatus = useSetStatus();
  return useMutation({
    mutationFn: api.disconnect,
    onSuccess: (s) => {
      onDone?.();
      setStatus(s);
    },
  });
}

function SyncNow() {
  const api = useApi();
  const qc = useQueryClient();
  const sync = useMutation({
    mutationFn: api.sync,
    // The status refetch sees the first sync land and refreshes the views it fills.
    onSettled: () => qc.invalidateQueries({ queryKey: T3_STATUS_KEY }),
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
  const copy = useSetupCopy();
  return (
    <div className="flex flex-col gap-3">
      <HarnessFix fix={{ text: `Start T3 on ${copy.machine}, or update the T3 URL.`, command: "t3 serve" }} />
      {retry === null ? null : <CheckConnection run={() => retry.mutateAsync()} />}
      <div className="border-t border-line pt-3">
        <T3SettingsForm status={status} urlOnly />
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

const when = (iso: string | null) => (iso === null ? "-" : `${dateTime(iso)} (${relativeTime(iso)})`);

/** The T3 setup panel in Settings (its registry entry's Setup); the status card once T3 is set up. */
export function T3ConnectionPanel() {
  const status = useT3Status();
  const api = t3Api(useHarnessApi());
  const setStatus = useSetStatus();
  const check = () => api.status(true).then(setStatus);
  return (
    <QueryState isPending={status.isPending} error={status.data === undefined ? status.error : null} hasData={status.data !== undefined}>
      {status.data === undefined ? null : (
        <>
          <StatusCard status={status.data} check={check} />
          <PairCard status={status.data} />
          <NewTaskDefaults harness={T3_KIND} />
        </>
      )}
    </QueryState>
  );
}

function StatusCard({ status, check }: { status: T3Status; check: () => Promise<unknown> }) {
  const presentation = status.presentation;
  const fix = status.autoPair.reason === "fake" && presentation?.fix?.command === "t3 pair" ? undefined : presentation?.fix;
  const facts: HarnessFact[] = [...(presentation?.facts ?? [])];
  if (status.lastFullSyncAt !== null) facts.push({ id: "lastFullSync", label: "Last full sync", value: when(status.lastFullSyncAt) });
  return (
    <Panel title="T3 connection" actions={presentation === undefined ? undefined : <HarnessStatusChip status={presentation} />}>
      <div className="flex min-w-0 flex-col gap-3">
        <HarnessFix fix={fix} reason={status.probeError ?? status.lastError ?? presentation?.reason} />
        <HarnessFacts facts={facts} />
        <div className="flex flex-wrap items-start gap-2">
          <CheckConnection run={check} />
          <DisconnectT3 status={status} />
        </div>
        {t3Configured(status) ? <AutoCompleteToggle harness={T3_KIND} className="border-t border-line pt-2" /> : null}
      </div>
    </Panel>
  );
}

/** Pairing (auto through the t3 CLI, or a code) and the T3 settings. */
function PairCard({ status }: { status: T3Status }) {
  const paired = status.state === "connected";
  return (
    <Panel title="Pair" meta={paired ? "paired · pair again to replace the token" : status.state === "unpaired" ? "not paired" : status.state}>
      <div className="flex flex-col gap-4">
        <PairActions status={status} primary={!paired} showFix={false} />
        <section className="flex flex-col gap-3 border-t border-line pt-3">
          <T3SettingsForm status={status} />
        </section>
      </div>
    </Panel>
  );
}

