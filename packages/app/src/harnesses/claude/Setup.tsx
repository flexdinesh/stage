// Claude Code connection card (Settings > Connections, Manage drawer) and its Settings row. Stage runs tasks as
// Claude background sessions with Claude's own login: the card checks the claude command and its login (Connect),
// shows which Claude it uses, takes the Claude command, and disconnects. Status is the generic
// GET /harnesses/claude/status (shared cache with the status button). Logic: claude.ts.
import { useMutation } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useHarnessApi, useSetupCopy } from "../../api/harnessScope.tsx";
import { useApplyHarnessStatus, useDisconnectHarness, useHarnessStatus, useHarnesses } from "../../api/queries.ts";
import type { HarnessFact, HarnessStatus } from "../../api/types.ts";
import { AutoCompleteToggle } from "../../components/Completion.tsx";
import { NewTaskDefaults } from "../../components/NewTaskDefaults.tsx";
import { CheckConnection, ConfirmConnectionAction, HarnessFacts, HarnessFix, HarnessStatusChip, ReplacedConnectionNotice } from "../../components/HarnessPresentation.tsx";
import { Button, ErrorText, Field, Hint, Input, Muted, Panel, QueryState } from "../../components/ui.tsx";
import { CLAUDE, CLAUDE_DISCONNECT_PROMPT, approvalsText, claudeDetails, commandText, updatesText } from "./claude.ts";

export function ClaudeSetup() {
  const status = useHarnessStatus(CLAUDE);
  const s = status.data;

  const api = useHarnessApi();
  const apply = useApplyHarnessStatus(CLAUDE);
  const check = () => api.harnessStatus(CLAUDE, { refresh: true }).then(apply);
  return (
    <>
      <Panel
        title="Claude Code connection"
        actions={s === undefined ? undefined : <HarnessStatusChip status={s} />}
      >
        <ReplacedConnectionNotice replaced={s?.replaced} />
        <QueryState isPending={status.isPending} error={s === undefined ? status.error : null} hasData={s !== undefined}>
          {s === undefined ? null : s.configured ? (
            <Connected status={s} check={check} />
          ) : (
            <NotConnected status={s} check={check} />
          )}
        </QueryState>
        {s === undefined && status.error !== null ? <CheckConnection run={check} /> : null}
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
function NotConnected({ status, check }: { status: HarnessStatus; check: () => Promise<unknown> }) {
  const copy = useSetupCopy();
  const connect = useConnect();
  const availability = status.actions.find((action) => action.id === "connect");
  return (
    <div className="flex flex-col gap-3">
      <Muted>
        Stage runs each task as a Claude Code background session on {copy.machine}, signed in with Claude's own login. Stage stores no Claude
        credentials.
      </Muted>
      <HarnessFix fix={status.fix} reason={status.reason} />
      <HarnessFacts facts={status.facts} />
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <Button variant="primary" busy={connect.isPending} disabled={availability?.available !== true} onClick={() => {
          if (availability?.available === true && !connect.isPending) connect.mutate();
        }}>
          Connect
        </Button>
        <Hint>{availability?.reason || "Checks the claude command and that Claude is logged in."}</Hint>
      </div>
      <ErrorText error={connect.error} />
      <CheckConnection run={check} />
      <CommandForm status={status} />
    </div>
  );
}

function Connected({ status, check }: { status: HarnessStatus; check: () => Promise<unknown> }) {
  const connect = useConnect();
  const disconnect = useDisconnectHarness(CLAUDE);
  const details = claudeDetails(status);
  const { command, source } = commandText(status);
  const availability = status.actions.find((action) => action.id === "connect");
  const facts: HarnessFact[] = [
    { id: "binary", label: "Command", value: `${command} (${source})`, copyable: false },
    ...(status.facts ?? []).filter((fact) => fact.id !== "binary" && fact.id !== "events"),
  ];
  if (details !== null) {
    if (details.version !== "") facts.push({ id: "login", label: "Login", value: details.loggedIn ? "Logged in" : "Logged out" });
    facts.push({ id: "restarts", label: "Restarts", value: details.scope ? "Sessions keep running when Stage restarts" : "A Stage restart may stop Claude’s sessions" });
    if (details.events !== null) {
      const { text, reason } = updatesText(details.events);
      facts.push({ id: "events", label: "Updates", value: reason === null ? text : `${text} · ${reason}` });
      facts.push({ id: "approvals", label: "Approvals", value: approvalsText(details.events) });
    }
  }
  return (
    <div className="flex flex-col gap-3">
      <HarnessFix fix={status.fix} reason={status.connected ? undefined : status.reason} />
      {status.connected ? null : (
        <div className="flex flex-wrap items-center gap-2">
          <Button small busy={connect.isPending} disabled={availability?.available !== true} onClick={() => {
            if (availability?.available === true && !connect.isPending) connect.mutate();
          }}>Connect</Button>
          {availability?.reason ? <Hint>{availability.reason}</Hint> : null}
          <ErrorText error={connect.error} />
        </div>
      )}
      <HarnessFacts facts={facts} />
      <ul className="flex list-none flex-col gap-1 text-small text-muted">
        <li>
          Approvals and questions of the sessions Stage starts can be answered on the task, or in Claude Code with the task's resume command.
          Tracked sessions are answered in Claude Code.
        </li>
        <li>
          Claude must trust a folder before it runs there: run <code className="font-mono text-fg">claude</code> in it once.
        </li>
      </ul>
      <ConfirmConnectionAction action={{
        prompt: CLAUDE_DISCONNECT_PROMPT,
        busy: disconnect.isPending,
        error: disconnect.error,
        run: (done) => disconnect.mutate(undefined, { onSuccess: done }),
        reset: disconnect.reset,
      }}><CheckConnection run={check} /></ConfirmConnectionAction>
      <CommandForm status={status} />
      <AutoCompleteToggle harness={CLAUDE} className="border-t border-line pt-2" />
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
