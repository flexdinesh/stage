// The generic registry entry, for any harness kind without its own (index.ts): its setup card rendered from the
// descriptor's setup schema (GET /api/harnesses) and the generic HarnessStatus, its Settings row and status reader.
import { useMutation } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useHarnessApi } from "../api/harnessScope.tsx";
import { keys, useApplyHarnessStatus, useDisconnectHarness, useHarnessStatus } from "../api/queries.ts";
import type { HarnessDescriptor, HarnessStatus, SetupAction, SetupField } from "../api/types.ts";
import { actionBody, initialValues, settingsBody, type SetupValues } from "../lib/connectionSetup.ts";
import { AutoCompleteToggle } from "../components/Completion.tsx";
import { ConnectionRow } from "../components/ConnectionRow.tsx";
import { NewTaskDefaults } from "../components/NewTaskDefaults.tsx";
import { Button, Checkbox, ChoiceRow, Chip, Detail, Details, ErrorText, Field, InlineError, Input, Muted, Panel, QueryState, type ChipTone } from "../components/ui.tsx";
import { GENERIC_STATUS, statusSlots } from "./status.ts";
import type { ConnectionRowProps, HarnessSlots } from "./types.ts";

export const GENERIC_SLOTS: HarnessSlots = {
  Setup: GenericSetup,
  Row: GenericRow,
  ...statusSlots(GENERIC_STATUS),
  statusKey: (kind) => [...keys.harnessStatus, kind],
  webAddress: false,
};

/** The generic card and its New tasks panel, on the harness scope's machine. */
function GenericSetup({ descriptor }: { descriptor: HarnessDescriptor }) {
  return (
    <>
      <ConnectionCard descriptor={descriptor} />
      <NewTaskDefaults harness={descriptor.kind} />
    </>
  );
}

function GenericRow({ descriptor, machine, onManage, isDefault }: ConnectionRowProps) {
  const status = useHarnessStatus(descriptor.kind);
  const disconnect = useDisconnectHarness(descriptor.kind);
  const s = status.data;
  const tone: ChipTone = s === undefined ? "idle" : s.connected ? "ok" : s.configured ? "bad" : "idle";
  return (
    <ConnectionRow
      name={descriptor.name}
      machine={machine}
      detail={s === undefined ? (status.error?.message ?? "") : (s.reason ?? s.lastError ?? "")}
      chip={{ tone, label: s?.state ?? "checking" }}
      onManage={onManage}
      isDefault={isDefault}
      disconnect={
        s === undefined || !descriptor.setup.disconnect || !s.configured
          ? null
          : {
              prompt: `Disconnect ${descriptor.name}? Tasks and history stay. You can connect again any time. It stays listed as disconnected until you forget it.`,
              busy: disconnect.isPending,
              error: disconnect.error,
              run: (done) => disconnect.mutate(undefined, { onSuccess: done }),
              reset: disconnect.reset,
            }
      }
    />
  );
}

/** The generic card: state, settings, actions and disconnect from the setup schema. */
function ConnectionCard({ descriptor }: { descriptor: HarnessDescriptor }) {
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
  const apply = useApplyHarnessStatus(descriptor.kind);
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
  const apply = useApplyHarnessStatus(kind);
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
