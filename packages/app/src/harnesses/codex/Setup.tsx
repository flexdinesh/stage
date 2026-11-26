// Codex connection panel (Settings > Connections, in the Manage drawer): the CLI's versions, account, CODEX_HOME and
// app-server daemon; when not connected, why and the command that fixes it, then Connect; Check again and Disconnect
// once connected; the CLI path setting. Logic: codex.ts, status.ts (codexSummary).
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { ApiError } from "../../api/client.ts";
import { useHarnessApi, useScopedKey } from "../../api/harnessScope.tsx";
import { invalidateConnectionChange } from "../../api/queries.ts";
import type { HarnessStatus, SetupField } from "../../api/types.ts";
import { initialValues, settingsBody, type SetupValues } from "../../lib/connectionSetup.ts";
import { statusRead } from "../../lib/connections.ts";
import { AutoCompleteToggle } from "../../components/Completion.tsx";
import { NewTaskDefaults } from "../../components/NewTaskDefaults.tsx";
import { Button, Chip, CommandBox, ConfirmInline, ErrorText, Field, InlineError, Input, MetaLine, Muted, Panel, QueryState, RelTime } from "../../components/ui.tsx";
import { CODEX_KIND as KIND, codexAccountText, codexDaemonText, codexVersionText, parseCodexStatus } from "./codex.ts";
import { CODEX_STATUS_KEY, useCodexStatus } from "./queries.ts";
import { codexSummary } from "./status.ts";
import type { CodexDetails, CodexStatus } from "./types.ts";

/** The CLI path setting (the descriptor's `binary` field). */
const BINARY: SetupField = { id: "binary", kind: "text", label: "Codex CLI", help: "Path to the codex binary. Empty uses codex on PATH." };

export const CODEX_DISCONNECT_PROMPT = "Disconnect Codex? Tasks and history stay. You can connect again any time. It stays listed as disconnected until you forget it.";

/** `apply` stores the status a call answered with and refreshes what a connection change touches; `recheck` refetches it. */
function useCodexCache() {
  const qc = useQueryClient();
  const scoped = useScopedKey();
  return {
    apply: (s: HarnessStatus) => {
      qc.setQueryData(scoped(CODEX_STATUS_KEY), parseCodexStatus(s));
      void invalidateConnectionChange(qc, scoped);
    },
    recheck: () => void qc.invalidateQueries({ queryKey: scoped(CODEX_STATUS_KEY) }),
  };
}

/** Disconnects Codex on the harness scope's machine (the panel and its Settings row). */
export function useDisconnectCodex(onDone?: () => void) {
  const api = useHarnessApi();
  const { apply } = useCodexCache();
  return useMutation({
    mutationFn: () => api.disconnectHarness(KIND),
    onSuccess: (s) => {
      onDone?.();
      apply(s);
    },
  });
}

export function CodexSetup() {
  const status = useCodexStatus();
  const summary = codexSummary(statusRead(status));
  return (
    <>
      <Panel
        title="Codex connection"
        actions={
          <Chip tone={summary.tone} dot>
            {summary.state}
          </Chip>
        }
      >
        <QueryState isPending={status.isPending} error={status.error} hasData={status.data !== undefined}>
          {status.data === undefined ? null : <CodexBody status={status.data} />}
        </QueryState>
      </Panel>
      <NewTaskDefaults harness={KIND} />
    </>
  );
}

function CodexBody({ status }: { status: CodexStatus }) {
  return (
    <div className="flex flex-col gap-3">
      {status.details === null ? null : <Facts details={status.details} state={status.state} />}
      <Connection status={status} />
      <SettingsForm status={status} />
      <AutoCompleteToggle harness={KIND} className="border-t border-line pt-2" />
    </div>
  );
}

/** Mono fact lines: versions, account, CODEX_HOME, daemon and when it was checked. */
function Facts({ details: d, state }: { details: CodexDetails; state: string }) {
  const version = codexVersionText(d);
  const account = codexAccountText(d.account, state);
  const daemon = codexDaemonText(d.daemon);
  return (
    <div className="flex flex-col gap-0.5">
      {version === "" ? null : <MetaLine className="break-all">{version}</MetaLine>}
      {account === null ? null : <MetaLine className="break-all">{account}</MetaLine>}
      {d.codexHome === "" ? null : <MetaLine className="break-all">CODEX_HOME {d.codexHome}</MetaLine>}
      {daemon === null && d.checkedAt === null ? null : (
        <MetaLine>
          {daemon}
          {d.checkedAt === null ? null : <RelTime at={d.checkedAt} prefix="checked" />}
        </MetaLine>
      )}
    </div>
  );
}

/** Why it is not connected and the command that fixes it, then Connect; Check again once connected; Disconnect behind a confirm. */
function Connection({ status }: { status: CodexStatus }) {
  const api = useHarnessApi();
  const { apply, recheck } = useCodexCache();
  const [confirm, setConfirm] = useState(false);
  // A failed connect may have changed the state (signed out, not installed): read it again.
  const connect = useMutation({ mutationFn: () => api.runHarnessAction(KIND, "connect", {}), onSuccess: apply, onError: recheck });
  const check = useMutation({ mutationFn: () => api.harnessStatus(KIND, { refresh: true }), onSuccess: apply });
  const disconnect = useDisconnectCodex(() => setConfirm(false));
  const reason = status.connected ? "" : (status.reason ?? status.lastError ?? "");
  const fix = status.connected ? "" : (status.details?.fix ?? "");
  const action = status.actions.find((a) => a.id === "connect");
  const canConnect = action?.available !== false;
  // The connect error that only repeats the reason shown above is not printed twice.
  const connectError = connect.error instanceof ApiError && connect.error.message === reason ? null : connect.error;
  return (
    <div className="flex flex-col gap-2">
      {reason === "" ? null : <InlineError>{reason}</InlineError>}
      {fix === "" ? null : <CommandBox command={fix} />}
      <div className="flex flex-wrap items-center gap-2">
        {status.connected ? (
          <Button small busy={check.isPending} onClick={() => check.mutate()}>
            Check again
          </Button>
        ) : (
          <Button variant="primary" busy={connect.isPending} disabled={!canConnect} onClick={() => connect.mutate()}>
            Connect
          </Button>
        )}
        {status.connected || canConnect || action?.reason === undefined ? null : <Muted>Unavailable: {action.reason}</Muted>}
        {status.configured ? (
          <Button
            small
            variant="danger"
            className="ml-auto"
            disabled={confirm}
            onClick={() => {
              disconnect.reset();
              setConfirm(true);
            }}
          >
            Disconnect
          </Button>
        ) : null}
      </div>
      {confirm ? (
        <ConfirmInline danger confirmLabel="Disconnect" busy={disconnect.isPending} error={disconnect.error} onConfirm={() => disconnect.mutate()} onCancel={() => setConfirm(false)}>
          {CODEX_DISCONNECT_PROMPT}
        </ConfirmInline>
      ) : null}
      <ErrorText error={status.connected ? check.error : connectError} />
    </div>
  );
}

/** The CLI path; Save sends only a change (cleared resets to codex on PATH). */
function SettingsForm({ status }: { status: CodexStatus }) {
  const api = useHarnessApi();
  const { apply } = useCodexCache();
  const [values, setValues] = useState<SetupValues>(() => initialValues([BINARY], status.fields));
  // Compared with the settings as last opened or saved, not the live poll: an untouched field never reverts a change made elsewhere.
  const [saved, setSaved] = useState(status.fields);
  const body = settingsBody([BINARY], saved, values);
  const save = useMutation({
    mutationFn: () => api.configureHarness(KIND, body),
    onSuccess: (s) => {
      setSaved(s.fields);
      apply(s);
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  const value = values[BINARY.id];
  return (
    <form onSubmit={submit} className="flex flex-col gap-2 border-t border-line pt-3">
      <Field label={BINARY.label} hint={BINARY.help}>
        {(id) => (
          <Input
            id={id}
            value={typeof value === "string" ? value : ""}
            placeholder="codex"
            autoComplete="off"
            spellCheck={false}
            className="font-mono"
            onChange={(e) => setValues({ [BINARY.id]: e.target.value })}
          />
        )}
      </Field>
      <div>
        <Button small type="submit" busy={save.isPending} disabled={Object.keys(body).length === 0}>
          Save
        </Button>
      </div>
      <ErrorText error={save.error} />
    </form>
  );
}
