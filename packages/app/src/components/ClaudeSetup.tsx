// Claude Code connection card (Settings > Connections, Manage drawer) and its Settings row. Stage runs tasks as
// Claude background sessions with Claude's own login: the card checks the claude command and its login (Connect),
// shows which Claude it uses, takes the Claude command, and disconnects. Status is the generic
// GET /harnesses/claude/status (shared cache with the status button). Logic: lib/claude.ts.
import { useMutation } from "@tanstack/react-query";
import { useState, type FormEvent, type ReactNode } from "react";
import { useHarnessApi, useSetupCopy } from "../api/harnessScope.tsx";
import { useApplyHarnessStatus, useDisconnectHarness, useHarnessStatus, useHarnesses } from "../api/queries.ts";
import type { HarnessStatus } from "../api/types.ts";
import { CLAUDE, CLAUDE_DISCONNECT_PROMPT, claudeChip, claudeDetails, claudeFix, commandText, configText, versionText } from "../lib/claude.ts";
import { AutoCompleteToggle } from "./Completion.tsx";
import { NewTaskDefaults } from "./NewTaskDefaults.tsx";
import { Alert, Button, Chip, CommandBox, ConfirmInline, ErrorText, Field, Hint, Input, Muted, Panel, QueryState } from "./ui.tsx";

export function ClaudeSetup() {
  const status = useHarnessStatus(CLAUDE);
  const s = status.data;
  const chip = s === undefined ? null : claudeChip(s);
  return (
    <>
      <Panel
        title="Claude Code connection"
        actions={
          chip === null ? undefined : (
            <Chip tone={chip.tone} dot>
              {chip.label}
            </Chip>
          )
        }
      >
        <QueryState isPending={status.isPending} error={status.error} hasData={s !== undefined}>
          {s === undefined ? null : s.configured ? (
            <Connected status={s} checking={status.isFetching} check={() => void status.refetch()} />
          ) : (
            <NotConnected status={s} />
          )}
        </QueryState>
      </Panel>
      <NewTaskDefaults harness={CLAUDE} />
    </>
  );
}

/** Connect: checks the command and its login, then records the connection. */
function useConnect() {
  const api = useHarnessApi();
  const apply = useApplyHarnessStatus(CLAUDE);
  return useMutation({ mutationFn: () => api.runHarnessAction(CLAUDE, "connect"), onSuccess: apply });
}

/** Not set up (or disconnected): what Stage does with Claude Code, what stops it here, and Connect. */
function NotConnected({ status }: { status: HarnessStatus }) {
  const copy = useSetupCopy();
  const connect = useConnect();
  const problem = status.state !== "disconnected" && status.reason !== undefined && status.reason !== "" ? status.reason : null;
  return (
    <div className="flex flex-col gap-3">
      <Muted>
        Stage runs each task as a Claude Code background session on {copy.machine}, signed in with Claude's own login. Stage stores no Claude
        credentials.
      </Muted>
      {problem === null ? null : <Problem status={status} reason={problem} />}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <Button variant="primary" busy={connect.isPending} onClick={() => connect.mutate()}>
          Connect
        </Button>
        <Hint>checks the claude command and that Claude is logged in</Hint>
      </div>
      <ErrorText error={connect.error} />
      <CommandForm status={status} />
    </div>
  );
}

/** A problem state's reason and, when a terminal fixes it, the command to run. */
function Problem({ status, reason, action }: { status: HarnessStatus; reason: string; action?: ReactNode }) {
  const fix = claudeFix(status);
  return (
    <div className="flex flex-col gap-2">
      <Alert tone="need" {...(action === undefined ? {} : { actions: action })}>
        {reason}
      </Alert>
      {fix === null ? null : <CommandBox command={fix} />}
    </div>
  );
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="border-b border-line py-1.5 pr-4 font-mono text-meta text-muted">{label}</dt>
      <dd className="min-w-0 border-b border-line py-1.5 font-mono text-detail break-words">{children}</dd>
    </>
  );
}

/** Set up: which Claude it uses, a problem's fix and Connect again, how tasks run, and its actions. */
function Connected({ status, checking, check }: { status: HarnessStatus; checking: boolean; check: () => void }) {
  const connect = useConnect();
  const d = claudeDetails(status);
  const { command, source } = commandText(status);
  const problem = !status.connected && status.reason !== undefined && status.reason !== "" ? status.reason : null;
  return (
    <div className="flex flex-col gap-3">
      {problem === null ? null : (
        <>
          <Problem
            status={status}
            reason={problem}
            action={
              <Button small busy={connect.isPending} onClick={() => connect.mutate()}>
                Connect again
              </Button>
            }
          />
          <ErrorText error={connect.error} />
        </>
      )}
      <dl className="grid grid-cols-[max-content_1fr] border-t border-line">
        <Fact label="Command">
          <span className="break-all">{command}</span> <span className="text-muted">({source})</span>
        </Fact>
        <Fact label="Version">{d === null || d.version === "" ? "-" : versionText(d.version)}</Fact>
        <Fact label="Config">{d === null || d.configDirectory === "" ? "-" : configText(d.configDirectory)}</Fact>
        <Fact label="Login">{d === null || d.version === "" ? "-" : d.loggedIn ? "logged in" : "logged out"}</Fact>
        <Fact label="Restarts">{d === null ? "-" : d.scope ? "sessions keep running when Stage restarts" : "a Stage restart may stop Claude's sessions"}</Fact>
      </dl>
      <ul className="flex list-none flex-col gap-1 text-small text-muted">
        <li>Questions and approvals are answered in Claude Code: copy a task's resume command and run it in a terminal.</li>
        <li>
          Claude must trust a folder before it runs there: run <code className="font-mono text-fg">claude</code> in it once.
        </li>
      </ul>
      <Actions checking={checking} check={check} />
      <CommandForm status={status} />
      <AutoCompleteToggle harness={CLAUDE} className="border-t border-line pt-2" />
    </div>
  );
}

/** Check connection, and Disconnect behind an inline confirm. */
function Actions({ checking, check }: { checking: boolean; check: () => void }) {
  const [confirm, setConfirm] = useState(false);
  const disconnect = useDisconnectHarness(CLAUDE, () => setConfirm(false));
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <Button small busy={checking} onClick={check}>
          Check connection
        </Button>
        <Button
          small
          variant="ghost"
          disabled={confirm}
          onClick={() => {
            disconnect.reset();
            setConfirm(true);
          }}
        >
          Disconnect
        </Button>
      </div>
      {confirm ? (
        <ConfirmInline danger confirmLabel="Disconnect" busy={disconnect.isPending} error={disconnect.error} onConfirm={() => disconnect.mutate()} onCancel={() => setConfirm(false)}>
          {CLAUDE_DISCONNECT_PROMPT}
        </ConfirmInline>
      ) : null}
    </div>
  );
}

/** The Claude command (blank: Stage's own lookup); its help comes from the setup schema. */
function CommandForm({ status }: { status: HarnessStatus }) {
  const api = useHarnessApi();
  const apply = useApplyHarnessStatus(CLAUDE);
  const help = useHarnesses().descriptor(CLAUDE)?.setup.fields.find((f) => f.id === "binary")?.help;
  const field = status.fields.find((f) => f.id === "binary");
  const saved = field?.source === "saved" ? field.value : "";
  const [value, setValue] = useState(saved);
  const save = useMutation({
    mutationFn: (binary: string | null) => api.configureHarness(CLAUDE, { binary }),
    onSuccess: apply,
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const next = value.trim();
    save.mutate(next === "" ? null : next);
  };
  const changed = value.trim() !== saved;
  return (
    <form onSubmit={submit} className="flex flex-col gap-2 border-t border-line pt-3">
      <Field label="Claude command" {...(help === undefined ? {} : { hint: help })}>
        {(id) => (
          <Input id={id} value={value} placeholder="claude" autoComplete="off" spellCheck={false} className="font-mono" onChange={(e) => setValue(e.target.value)} />
        )}
      </Field>
      <div>
        <Button small type="submit" busy={save.isPending} disabled={!changed}>
          Save
        </Button>
      </div>
      <ErrorText error={save.error} />
    </form>
  );
}
