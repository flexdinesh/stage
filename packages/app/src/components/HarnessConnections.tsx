// Harness connection setup (Settings > Connections, in the Manage drawer).
// A harness with its own panel registers it in CONNECTION_PANELS (its
// harness-specific UI, behind this one boundary); every other harness gets
// the generic card rendered from its setup schema (GET /api/harnesses).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type ComponentType, type FormEvent } from "react";
import { useHarnessApi, useScopedKey } from "../api/harnessScope.tsx";
import { harnessStatusQuery, invalidateConnectionChange, keys } from "../api/queries.ts";
import type { Harness, HarnessDescriptor, HarnessStatus, SetupAction, SetupField } from "../api/types.ts";
import { actionBody, initialValues, settingsBody, type SetupValues } from "../lib/connectionSetup.ts";
import { CodexSetup } from "./CodexSetup.tsx";
import { AutoCompleteToggle } from "./Completion.tsx";
import { NewTaskDefaults } from "./NewTaskDefaults.tsx";
import { OpenCodeSetup } from "./OpenCodeSetup.tsx";
import { T3ConnectionPanel } from "./T3Setup.tsx";
import { Button, Checkbox, ChoiceRow, Chip, Detail, Details, ErrorText, Field, InlineError, Input, Muted, Panel, QueryState } from "./ui.tsx";

/** Harness-specific setup panels by kind (each places its NewTaskDefaults); the generic card covers the rest. */
const CONNECTION_PANELS: Record<string, ComponentType> = {
  t3: T3ConnectionPanel,
  opencode: OpenCodeSetup,
  codex: CodexSetup,
};

/** One harness's setup on the harness scope's machine: its own panels, or the generic card and its New tasks panel. */
export function ConnectionSetup({ descriptor }: { descriptor: HarnessDescriptor }) {
  const Own = CONNECTION_PANELS[descriptor.kind];
  if (Own !== undefined) return <Own />;
  return (
    <>
      <ConnectionCard descriptor={descriptor} />
      <NewTaskDefaults harness={descriptor.kind} />
    </>
  );
}

/** A generic harness's status on the harness scope's machine (shared by its card and its Settings row). */
export function useHarnessStatus(kind: Harness) {
  const api = useHarnessApi();
  return useQuery(harnessStatusQuery(api, useScopedKey()([...keys.harnessStatus, kind]), kind));
}

/** Disconnects a generic harness on the harness scope's machine. */
export function useDisconnectHarness(kind: Harness, onDone?: () => void) {
  const api = useHarnessApi();
  const apply = useApply(kind);
  return useMutation({
    mutationFn: () => api.disconnectHarness(kind),
    onSuccess: (s) => {
      onDone?.();
      apply(s);
    },
  });
}

/** The generic card: state, settings, actions and disconnect from the setup schema. */
export function ConnectionCard({ descriptor }: { descriptor: HarnessDescriptor }) {
  const status = useHarnessStatus(descriptor.kind);
  return (
    <Panel
      title={`${descriptor.name} connection`}
      actions={
        status.data === undefined ? undefined : (
          <Chip tone={status.data.connected ? "ok" : status.data.configured ? "bad" : "idle"} dot>
            {status.data.state}
          </Chip>
        )
      }
    >
      <QueryState isPending={status.isPending} error={status.error} hasData={status.data !== undefined}>
        {status.data === undefined ? null : <ConnectionBody descriptor={descriptor} status={status.data} />}
      </QueryState>
    </Panel>
  );
}

function useApply(kind: string) {
  const qc = useQueryClient();
  const scoped = useScopedKey();
  return (s: HarnessStatus) => {
    qc.setQueryData(scoped([...keys.harnessStatus, kind]), s);
    void invalidateConnectionChange(qc, scoped);
  };
}

function ConnectionBody({ descriptor, status }: { descriptor: HarnessDescriptor; status: HarnessStatus }) {
  const disconnect = useDisconnectHarness(descriptor.kind);
  const reason = status.reason ?? status.lastError;
  return (
    <div className="flex flex-col gap-3">
      {reason === undefined || reason === "" || status.connected ? null : <InlineError>{reason}</InlineError>}
      {descriptor.setup.fields.length === 0 ? null : <SettingsForm descriptor={descriptor} status={status} />}
      {descriptor.setup.actions.map((a) => (
        <ActionForm key={a.id} kind={descriptor.kind} action={a} state={status.actions.find((s) => s.id === a.id)} />
      ))}
      <AutoCompleteToggle harness={descriptor.kind} className="border-t border-line pt-2" />
      {descriptor.setup.disconnect && status.configured ? (
        <div className="flex flex-col gap-1.5 border-t border-line pt-3">
          <div>
            <Button small busy={disconnect.isPending} onClick={() => disconnect.mutate()}>
              Disconnect
            </Button>
          </div>
          <ErrorText error={disconnect.error} />
        </div>
      ) : null}
    </div>
  );
}

type Values = SetupValues;

/** Inputs for fields. */
function Inputs({ fields, status, values, onChange }: { fields: SetupField[]; status?: HarnessStatus; values: Values; onChange: (id: string, v: string | boolean) => void }) {
  return (
    <>
      {fields.map((f) => {
        const st = status?.fields.find((s) => s.id === f.id);
        const value = values[f.id];
        if (f.kind === "bool") {
          return (
            <ChoiceRow key={f.id} control={<Checkbox checked={value === true} onChange={(e) => onChange(f.id, e.target.checked)} />}>
              {f.label}
            </ChoiceRow>
          );
        }
        return (
          <Field key={f.id} label={f.label} hint={f.help}>
            {(id) => (
              <Input
                id={id}
                type={f.kind === "secret" ? "password" : f.kind === "url" ? "url" : "text"}
                value={typeof value === "string" ? value : ""}
                placeholder={f.kind === "secret" && st?.set === true ? "(set)" : undefined}
                autoComplete="off"
                spellCheck={false}
                onChange={(e) => onChange(f.id, e.target.value)}
              />
            )}
          </Field>
        );
      })}
    </>
  );
}

function SettingsForm({ descriptor, status }: { descriptor: HarnessDescriptor; status: HarnessStatus }) {
  const api = useHarnessApi();
  const apply = useApply(descriptor.kind);
  const fields = descriptor.setup.fields;
  const [values, setValues] = useState<Values>(() => initialValues(fields, status.fields));
  // Compared with the fields as the form opened, not the live poll: an untouched field never reverts a change made elsewhere.
  const [opened] = useState(status.fields);
  const save = useMutation({
    mutationFn: () => api.configureHarness(descriptor.kind, settingsBody(fields, opened, values)),
    onSuccess: apply,
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-2">
      <Details>
        {status.fields.map((f) => (
          <Detail key={f.id} label={fields.find((d) => d.id === f.id)?.label ?? f.id}>
            {f.value !== "" ? f.value : f.set ? "(set)" : "-"}
          </Detail>
        ))}
      </Details>
      <Inputs fields={fields} status={status} values={values} onChange={(id, v) => setValues((p) => ({ ...p, [id]: v }))} />
      <div>
        <Button small type="submit" busy={save.isPending}>
          Save
        </Button>
      </div>
      <ErrorText error={save.error} />
    </form>
  );
}

function ActionForm({ kind, action, state }: { kind: string; action: SetupAction; state: HarnessStatus["actions"][number] | undefined }) {
  const api = useHarnessApi();
  const apply = useApply(kind);
  const input = action.input ?? [];
  const [values, setValues] = useState<Values>(() => initialValues(input, []));
  const run = useMutation({
    mutationFn: () => api.runHarnessAction(kind, action.id, actionBody(input, values)),
    onSuccess: (s) => {
      setValues(initialValues(input, []));
      apply(s);
    },
  });
  const available = state?.available !== false;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (available) run.mutate();
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-2 border-t border-line pt-3">
      <Inputs fields={input} values={values} onChange={(id, v) => setValues((p) => ({ ...p, [id]: v }))} />
      <div className="flex flex-wrap items-center gap-2">
        <Button small type="submit" busy={run.isPending} disabled={!available}>
          {action.label}
        </Button>
        {available || state?.reason === undefined ? null : <Muted>Unavailable: {state.reason}</Muted>}
      </div>
      <ErrorText error={run.error} />
    </form>
  );
}
