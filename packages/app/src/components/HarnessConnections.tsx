// Harness connection setup in Settings: one card per connectable harness.
// A harness with its own panel registers it in CONNECTION_PANELS (its
// harness-specific UI, behind this one boundary); every other harness gets
// the generic card rendered from its setup schema (GET /api/harnesses).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type ComponentType, type FormEvent } from "react";
import { useHarnessApi, useScopedKey, useSetupCopy } from "../api/harnessScope.tsx";
import { keys, useScopedHarnessDescriptors } from "../api/queries.ts";
import type { HarnessDescriptor, HarnessStatus, SetupAction, SetupField } from "../api/types.ts";
import { actionBody, initialValues, settingsBody, type SetupValues } from "../lib/connectionSetup.ts";
import { AutoCompleteToggle } from "./Completion.tsx";
import { OpenCodeSetup } from "./OpenCodeSetup.tsx";
import { PreviousConnections } from "./PreviousConnections.tsx";
import { T3ConnectionPanel } from "./T3Setup.tsx";
import { Button, Checkbox, ChoiceRow, Chip, Detail, Details, ErrorText, Field, InlineError, Input, Muted, Panel, QueryState } from "./ui.tsx";

/** Harness-specific setup panels by kind; the generic card covers the rest. */
const CONNECTION_PANELS: Record<string, ComponentType> = {
  t3: T3ConnectionPanel,
  opencode: OpenCodeSetup,
};

/** The harness scope's machine's connection setup. */
export function HarnessConnections() {
  const descriptors = useScopedHarnessDescriptors();
  const connectable = (descriptors.data?.harnesses ?? []).filter((d) => d.connectable);
  return (
    <QueryState isPending={descriptors.isPending} error={descriptors.error} hasData={descriptors.data !== undefined}>
      {connectable.map((d) => {
        const Own = CONNECTION_PANELS[d.kind];
        return Own === undefined ? <ConnectionCard key={d.kind} descriptor={d} /> : <Own key={d.kind} />;
      })}
    </QueryState>
  );
}

/** The generic card: state, settings, actions and disconnect from the setup schema. */
export function ConnectionCard({ descriptor }: { descriptor: HarnessDescriptor }) {
  const api = useHarnessApi();
  const status = useQuery({ queryKey: useScopedKey()([...keys.harnessStatus, descriptor.kind]), queryFn: () => api.harnessStatus(descriptor.kind), refetchInterval: 15000 });
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
  const key = useScopedKey()([...keys.harnessStatus, kind]);
  return (s: HarnessStatus) => {
    qc.setQueryData(key, s);
    void qc.invalidateQueries({ queryKey: keys.connections });
  };
}

function ConnectionBody({ descriptor, status }: { descriptor: HarnessDescriptor; status: HarnessStatus }) {
  const api = useHarnessApi();
  const apply = useApply(descriptor.kind);
  const disconnect = useMutation({ mutationFn: () => api.disconnectHarness(descriptor.kind), onSuccess: apply });
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
      <PreviousConnections harness={descriptor.kind} className="border-t border-line pt-3" />
    </div>
  );
}

type Values = SetupValues;

/** Inputs for fields; locked ones are read-only. */
function Inputs({ fields, status, values, onChange }: { fields: SetupField[]; status?: HarnessStatus; values: Values; onChange: (id: string, v: string | boolean) => void }) {
  const copy = useSetupCopy();
  return (
    <>
      {fields.map((f) => {
        const st = status?.fields.find((s) => s.id === f.id);
        const locked = st?.locked === true;
        const hint = locked ? copy.envLock(f.env) : f.help;
        const value = values[f.id];
        if (f.kind === "bool") {
          return (
            <ChoiceRow key={f.id} control={<Checkbox checked={value === true} disabled={locked} onChange={(e) => onChange(f.id, e.target.checked)} />}>
              {f.label}
            </ChoiceRow>
          );
        }
        return (
          <Field key={f.id} label={f.label} hint={hint}>
            {(id) => (
              <Input
                id={id}
                type={f.kind === "secret" ? "password" : f.kind === "url" ? "url" : "text"}
                value={typeof value === "string" ? value : ""}
                placeholder={f.kind === "secret" && st?.set === true ? "(set)" : undefined}
                disabled={locked}
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
