import { useMutation } from "@tanstack/react-query";
import { useRef, useState, type FormEvent } from "react";
import { useHarnessApi } from "../api/harnessScope.tsx";
import { keys, useApplyHarnessStatus, useDisconnectHarness, useHarnessStatus } from "../api/queries.ts";
import type { HarnessDescriptor, HarnessStatus, ReplacedConnection } from "../api/types.ts";
import { clearSecrets, initialValues, settingsBody } from "../lib/connectionSetup.ts";
import { connectionStatusChip } from "../lib/connections.ts";
import { AutoCompleteToggle } from "../components/Completion.tsx";
import { SetupInputs } from "../components/DeclaredActions.tsx";
import { CheckConnection, ConfirmConnectionAction, HarnessFacts, HarnessFix, HarnessStatusChip, ReplacedConnectionNotice } from "../components/HarnessPresentation.tsx";
import { ConnectionRow } from "../components/ConnectionRow.tsx";
import { ConnectionDeclaredActions } from "../components/ConnectionDeclaredActions.tsx";
import { NewTaskDefaults } from "../components/NewTaskDefaults.tsx";
import { Button, ErrorText, Panel, QueryState } from "../components/ui.tsx";
import { GENERIC_STATUS, statusSlots } from "./status.ts";
import type { ConnectionRowProps, HarnessSlots } from "./types.ts";

export const GENERIC_SLOTS: HarnessSlots = {
  Setup: GenericSetup,
  Row: GenericRow,
  ...statusSlots(GENERIC_STATUS),
  statusKey: (kind) => [...keys.harnessStatus, kind],
  handledSetupActions: () => true,
};

function GenericRow({ descriptor, machine, onManage, isDefault, overflow }: ConnectionRowProps) {
  const query = useHarnessStatus(descriptor.kind);
  const disconnect = useDisconnectHarness(descriptor.kind);
  const status = query.data;
  return (
    <ConnectionRow
      name={descriptor.name}
      machine={machine}
      detail={status?.reason ?? status?.lastError ?? query.error?.message ?? status?.facts?.[0]?.value ?? ""}
      chip={status === undefined ? { tone: "idle", label: "Checking" } : connectionStatusChip(status)}
      onManage={onManage}
      isDefault={isDefault}
      overflow={overflow}
      disconnect={!descriptor.setup.disconnect || !status?.configured ? null : {
        prompt: disconnectText(descriptor.name),
        busy: disconnect.isPending,
        error: disconnect.error,
        run: (done) => disconnect.mutate(undefined, { onSuccess: done }),
        reset: disconnect.reset,
      }}
    />
  );
}

const disconnectText = (name: string) => `Disconnect ${name}? Tasks and history stay. You can reconnect or forget this connection later.`;

function GenericSetup({ descriptor }: { descriptor: HarnessDescriptor }) {
  const query = useHarnessStatus(descriptor.kind);
  const status = query.data;
  const api = useHarnessApi();
  const apply = useApplyHarnessStatus(descriptor.kind);
  const check = () => api.harnessStatus(descriptor.kind, { refresh: true }).then(apply);
  return (
    <>
      <Panel title={`${descriptor.name} connection`} actions={status === undefined ? undefined : <HarnessStatusChip status={status} />}>
        <QueryState isPending={query.isPending} error={status === undefined ? query.error : null} hasData={status !== undefined}>
          {status === undefined ? null : <ConnectionBody descriptor={descriptor} status={status} check={check} />}
        </QueryState>
        {status === undefined && query.error !== null ? <CheckConnection run={check} /> : null}
      </Panel>
      <NewTaskDefaults harness={descriptor.kind} />
    </>
  );
}

function ConnectionBody({ descriptor, status, check }: { descriptor: HarnessDescriptor; status: HarnessStatus; check: () => Promise<unknown> }) {
  const disconnect = useDisconnectHarness(descriptor.kind);
  const [replaced, setReplaced] = useState<ReplacedConnection | null>(null);
  const recheck = <CheckConnection run={check} />;
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <ReplacedConnectionNotice replaced={replaced} />
      <HarnessFix fix={status.fix} reason={status.reason ?? status.lastError} />
      <HarnessFacts facts={status.facts} />
      {descriptor.setup.fields.length === 0 ? null : <SettingsForm descriptor={descriptor} status={status} onReplaced={setReplaced} />}
      <ConnectionDeclaredActions descriptor={descriptor} status={status} onReplaced={setReplaced} />
      <AutoCompleteToggle harness={descriptor.kind} className="border-t border-line pt-2" />
      {descriptor.setup.disconnect && status.configured ? (
        <ConfirmConnectionAction action={{
          prompt: disconnectText(descriptor.name),
          busy: disconnect.isPending,
          error: disconnect.error,
          run: (done) => disconnect.mutate(undefined, { onSuccess: done }),
          reset: disconnect.reset,
        }}>{recheck}</ConfirmConnectionAction>
      ) : recheck}
    </div>
  );
}

function SettingsForm({ descriptor, status, onReplaced }: {
  descriptor: HarnessDescriptor;
  status: HarnessStatus;
  onReplaced: (replaced: ReplacedConnection | null) => void;
}) {
  const api = useHarnessApi();
  const apply = useApplyHarnessStatus(descriptor.kind);
  const fields = descriptor.setup.fields;
  const [values, setValues] = useState(() => initialValues(fields, status.fields));
  const [saved, setSaved] = useState(status.fields);
  const inFlight = useRef(false);
  const save = useMutation({
    mutationFn: (dispatch: () => Promise<HarnessStatus>) => dispatch(),
    onSuccess: (reply) => {
      setSaved(reply.fields);
      setValues(initialValues(fields, reply.fields));
      onReplaced(reply.replaced ?? null);
      apply(reply);
    },
    onSettled: () => { inFlight.current = false; },
  });
  const body = settingsBody(fields, saved, values);
  const changed = Object.keys(body).length > 0;
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!changed || inFlight.current) return;
    inFlight.current = true;
    setValues((current) => clearSecrets(fields, current));
    save.mutate(() => {
      const pending = api.configureHarness(descriptor.kind, body);
      for (const field of fields) if (field.kind === "secret") delete body[field.id];
      return pending;
    });
  };
  return (
    <form onSubmit={submit} className="flex min-w-0 flex-col gap-2">
      <SetupInputs fields={fields} status={status} values={values} disabled={save.isPending}
        onChange={(id, value) => setValues((current) => ({ ...current, [id]: value }))} />
      <div><Button small type="submit" busy={save.isPending} disabled={!changed}>Save</Button></div>
      <ErrorText error={save.error} />
    </form>
  );
}
