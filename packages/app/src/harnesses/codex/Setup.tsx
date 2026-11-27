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
import { AutoCompleteToggle } from "../../components/Completion.tsx";
import { NewTaskDefaults } from "../../components/NewTaskDefaults.tsx";
import { CheckConnection, ConfirmConnectionAction, HarnessFacts, HarnessFix, HarnessStatusChip, ReplacedConnectionNotice } from "../../components/HarnessPresentation.tsx";
import { Button, ErrorText, Field, Input, Muted, Panel, QueryState } from "../../components/ui.tsx";
import { CODEX_KIND as KIND, parseCodexStatus } from "./codex.ts";
import { CODEX_STATUS_KEY, useCodexStatus } from "./queries.ts";
import type { CodexStatus } from "./types.ts";

/** The CLI path setting (the descriptor's `binary` field). */
const BINARY: SetupField = { id: "binary", kind: "text", label: "Codex CLI", help: "Path to the codex binary. Empty uses codex on PATH." };

export const CODEX_DISCONNECT_PROMPT = "Disconnect Codex? Tasks and history stay. You can connect again any time. It stays listed as disconnected until you remove it.";

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
  const s = status.data;
  const api = useHarnessApi();
  const { apply } = useCodexCache();
  const check = () => api.harnessStatus(KIND, { refresh: true }).then(apply);
  return (
    <>
      <Panel
        title="Codex connection"
        actions={s === undefined ? undefined : <HarnessStatusChip status={s} />}
      >
        <ReplacedConnectionNotice replaced={s?.replaced} />
        <QueryState isPending={status.isPending} error={s === undefined ? status.error : null} hasData={s !== undefined}>
          {s === undefined ? null : <CodexBody status={s} check={check} />}
        </QueryState>
        {s === undefined && status.error !== null ? <CheckConnection run={check} /> : null}
      </Panel>
      <NewTaskDefaults harness={KIND} />
    </>
  );
}

function CodexBody({ status, check }: { status: CodexStatus; check: () => Promise<unknown> }) {
  return (
    <div className="flex flex-col gap-3">
      <HarnessFacts facts={status.facts} />
      <Connection status={status} check={check} />
      <SettingsForm status={status} />
      <AutoCompleteToggle harness={KIND} className="border-t border-line pt-2" />
    </div>
  );
}

function Connection({ status, check }: { status: CodexStatus; check: () => Promise<unknown> }) {
  const api = useHarnessApi();
  const { apply, recheck } = useCodexCache();
  const connect = useMutation({ mutationFn: () => api.runHarnessAction(KIND, "connect"), onSuccess: apply, onError: recheck });
  const disconnect = useDisconnectCodex();
  const reason = status.connected ? undefined : (status.reason ?? status.lastError);
  const availability = status.actions.find((action) => action.id === "connect");
  const connectError = connect.error instanceof ApiError && connect.error.message === reason ? null : connect.error;
  const checkButton = <CheckConnection run={check} />;
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <HarnessFix fix={status.fix} reason={reason} />
      {status.connected ? null : (
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="primary" busy={connect.isPending} disabled={availability?.available !== true} onClick={() => {
            if (availability?.available === true && !connect.isPending) connect.mutate();
          }}>Connect</Button>
          {availability?.reason ? <Muted>{availability.reason}</Muted> : null}
        </div>
      )}
      {status.configured ? <ConfirmConnectionAction action={{
        prompt: CODEX_DISCONNECT_PROMPT,
        busy: disconnect.isPending,
        error: disconnect.error,
        run: (done) => disconnect.mutate(undefined, { onSuccess: done }),
        reset: disconnect.reset,
      }}>{checkButton}</ConfirmConnectionAction> : checkButton}
      <ErrorText error={connectError} />
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
