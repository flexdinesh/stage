// Add workspace (ADR 0012): asks a connection's harness to add a workspace through one of its declared sources. The
// harness validates, creates and owns it; Stage lists and groups the row it reports. Labels, help and confirmation come
// from the harness's declarations, so nothing here names a harness.
import { useMutation, useQueries } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from "react";
import { connectionCapabilityOptions, useHarnesses, useInvalidateProjects, useMachines } from "../api/queries.ts";
import type { AddedWorkspace, HarnessConnection } from "../api/types.ts";
import type { SetupValues } from "../lib/connectionSetup.ts";
import {
  addConnection, addFailure, addRequest, canSubmit, mintRequestId, nextRequestKey, pickSource, requestSignature,
  sourceControl, sourceFields, type AddConnection, type RequestKey,
} from "../lib/workspaceAdd.ts";
import { useApi } from "../platform/context.tsx";
import { SetupInputs } from "./DeclaredActions.tsx";
import {
  Alert, Button, Checkbox, ChoiceRow, Combobox, Drawer, ErrorText, Field, FieldGroup, Hint, Muted, Segmented, TextLink,
} from "./ui.tsx";

const NO_CONNECTIONS: readonly HarnessConnection[] = [];

/**
 * One add-workspace flow: the capable connections (every active one, or just `fixedConnection`), the chosen source and
 * its input, and the request. `projectId` puts the new workspace in that project; omitted, Stage groups it.
 */
export function useWorkspaceAdd({ fixedConnection, projectId }: { fixedConnection?: number; projectId?: number }) {
  const api = useApi();
  const harnesses = useHarnesses();
  const { machines } = useMachines();
  const refresh = useInvalidateProjects();
  const candidates = fixedConnection === undefined
    ? harnesses.activeList
    : harnesses.activeList.filter((c) => c.id === fixedConnection);
  const reads = useQueries({
    queries: (harnesses.loaded ? candidates : NO_CONNECTIONS).map((c) => ({
      ...connectionCapabilityOptions(api, c.id),
      enabled: c.configured,
    })),
  });
  const options = candidates.map((c, i) => addConnection(c, { data: reads[i]?.data, error: reads[i]?.error }));
  const [chosenConnection, setChosenConnection] = useState<number | null>(fixedConnection ?? null);
  const [chosenSource, setChosenSource] = useState<string | null>(null);
  const [values, setValues] = useState<SetupValues>({});
  const [confirmed, setConfirmed] = useState(false);
  const key = useRef<RequestKey | null>(null);

  const current = options.find((o) => o.connection.id === chosenConnection) ?? options.find((o) => o.available);
  const sources = current?.available === true ? current.sources : [];
  const source = pickSource(sources, chosenSource);
  const machineLabel = (c: HarnessConnection) => machines.find((m) => m.id === c.machineId)?.label || `Machine #${c.machineId}`;
  const connectionName = (c: HarnessConnection) => `${harnesses.kindName(c.harness)} · ${machineLabel(c)}`;

  const mutation = useMutation({
    mutationFn: ({ connectionId, body }: { connectionId: number; body: ReturnType<typeof addRequest> }) => api.addWorkspace(connectionId, body),
    // Destinations, project groups, inventories and capabilities: the new row shows everywhere before success does.
    onSuccess: () => refresh(),
  });
  const ready = current?.available === true && canSubmit(source, values, confirmed);

  const reset = () => {
    mutation.reset();
    setValues({});
    setConfirmed(false);
  };
  return {
    harnesses,
    options,
    current,
    sources,
    source,
    values,
    confirmed,
    ready,
    mutation,
    failure: addFailure(mutation.error),
    /** Still reading whether any connection can add. */
    checking: !harnesses.loaded || options.some((o) => o.checking),
    capable: options.some((o) => o.available),
    machineLabel,
    connectionName,
    harnessName: current === undefined ? "the harness" : harnesses.kindName(current.connection.harness),
    chooseConnection(id: number) {
      setChosenConnection(id);
      setChosenSource(null);
      reset();
    },
    chooseSource(id: string) {
      setChosenSource(id);
      reset();
    },
    setValue(id: string, value: string | boolean) {
      setValues((prev) => ({ ...prev, [id]: value }));
      // A changed target needs agreeing to again (the confirmation is per folder).
      setConfirmed(false);
    },
    setConfirmed,
    /** Sends the request. The same input resends the same key, so Check again after an uncertain outcome is safe. */
    submit(onAdded: (result: AddedWorkspace, connection: HarnessConnection) => void) {
      if (!ready || current === undefined || source === undefined || mutation.isPending) return;
      const draft = addRequest(source, values, { requestId: "", projectId, confirmed });
      key.current = nextRequestKey(key.current, requestSignature(current.connection.id, draft), () => mintRequestId());
      const connection = current.connection;
      mutation.mutate({ connectionId: connection.id, body: { ...draft, requestId: key.current.id } }, {
        onSuccess: (result) => onAdded(result, connection),
      });
    },
  };
}

export type WorkspaceAdd = ReturnType<typeof useWorkspaceAdd>;

function connectionOption(add: WorkspaceAdd, o: AddConnection) {
  const c = o.connection;
  return {
    value: String(c.id),
    label: add.harnesses.kindName(c.harness),
    tag: add.machineLabel(c),
    detail: o.available ? c.label || undefined : o.reason ?? undefined,
    disabled: !o.available,
  };
}

/** Nothing can add a workspace: each connection's reason, and the way to Settings. */
function NoCapableConnection({ add, fixed }: { add: WorkspaceAdd; fixed: boolean }) {
  if (add.options.length === 0) {
    return (
      <Muted>
        No connected harness. Connect one in <TextLink to="/settings/connections" variant="rest">Settings, Connections</TextLink>.
      </Muted>
    );
  }
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <p className="text-small">{fixed ? `${add.harnessName} can’t add workspaces from Stage.` : "No connection can add workspaces from Stage yet."}</p>
      <ul className="flex min-w-0 flex-col divide-y divide-line border-y border-line">
        {add.options.map((o) => (
          <li key={o.connection.id} className="flex min-w-0 flex-col gap-0.5 py-2">
            <span className="text-small font-medium">{add.connectionName(o.connection)}</span>
            <Hint className="wrap-anywhere">{o.reason}</Hint>
          </li>
        ))}
      </ul>
      {fixed ? null : (
        <Muted>Check harness setup in <TextLink to="/settings/connections" variant="rest">Settings, Connections</TextLink>.</Muted>
      )}
    </div>
  );
}

/**
 * The fields, in order: Connection (unless fixed), Source (when the harness declares more than one), the source's
 * input, its confirmation, then the request's progress or failure. The caller places the Add and Cancel buttons.
 */
export function AddWorkspaceFields({ add, fixed, nested = false, onRetry }: {
  add: WorkspaceAdd;
  fixed: boolean;
  /** Inside another form: no native required checks (see SetupInputs). */
  nested?: boolean;
  onRetry: () => void;
}) {
  const pending = add.mutation.isPending;
  const source = add.source;
  const control = sourceControl(add.sources);
  const unavailableSources = add.sources.filter((s) => !s.availability.available);
  const failure = add.failure;
  if (!add.capable) {
    if (add.checking) return <Muted>Checking which connections can add workspaces…</Muted>;
    return <NoCapableConnection add={add} fixed={fixed} />;
  }
  return (
    <div className="flex min-w-0 flex-col gap-3.5">
      {fixed ? null : (
        <Field label="Connection">
          {(id) => (
            <Combobox id={id} listLabel="Connections" autoFocus searchable={false} showTag disabled={pending}
              value={add.current === undefined ? "" : String(add.current.connection.id)}
              options={add.options.map((o) => connectionOption(add, o))}
              onChange={(next) => add.chooseConnection(Number(next))} />
          )}
        </Field>
      )}
      {control === "segmented" ? (
        // Phone: harness labels run long, so the joined buttons become the dropdown below rather than clip.
        <FieldGroup label="Source" start disabled={pending} className="max-md:hidden">
          <Segmented label="Source" value={source?.id ?? ""} onChange={add.chooseSource}
            options={add.sources.map((s) => ({ value: s.id, label: s.label, disabled: !s.availability.available }))} />
        </FieldGroup>
      ) : null}
      {control === "hidden" ? null : (
        <Field label="Source" className={control === "segmented" ? "md:hidden" : ""}>
          {(id) => (
            <Combobox id={id} listLabel="Sources" disabled={pending} value={source?.id ?? ""} onChange={add.chooseSource}
              options={add.sources.map((s) => ({
                value: s.id, label: s.label, disabled: !s.availability.available,
                detail: s.availability.available ? undefined : s.availability.reason,
              }))} />
          )}
        </Field>
      )}
      {source?.help || (control === "segmented" && unavailableSources.length > 0) ? (
        <div className="-mt-2 flex min-w-0 flex-col gap-1">
          {source?.help ? <Hint className="wrap-anywhere">{source.help}</Hint> : null}
          {control === "segmented" ? unavailableSources.map((s) => (
            <Hint key={s.id} className="wrap-anywhere max-md:hidden">{s.label}: {s.availability.reason || "Unavailable."}</Hint>
          )) : null}
        </div>
      ) : null}
      <SetupInputs fields={sourceFields(source)} values={add.values} disabled={pending} nativeRequired={!nested} onChange={add.setValue} />
      {source?.confirm ? (
        <ChoiceRow control={<Checkbox disabled={pending} checked={add.confirmed}
          onChange={(e) => add.setConfirmed(e.target.checked)} />}>
          <span className="wrap-anywhere">{source.confirm}</span>
        </ChoiceRow>
      ) : null}
      <div role="status" className="empty:hidden">
        {pending ? <Hint>Adding in {add.harnessName}… This can take a few minutes.</Hint> : null}
      </div>
      {failure === null || pending ? null : failure.kind === "error" ? <ErrorText error={add.mutation.error} /> : (
        <Alert actions={<Button small onClick={onRetry}>{failure.kind === "uncertain" ? "Check again" : "Try again"}</Button>}>
          {failure.kind === "uncertain"
            ? <>{add.harnessName} didn’t confirm the result. Check again to retry the same request; it won’t add a second workspace.</>
            : <>The workspace was added but not moved into this project. Try again to finish.</>}
          {failure.message ? <span className="mt-1 block text-muted wrap-anywhere">{failure.message}</span> : null}
        </Alert>
      )}
    </div>
  );
}

/**
 * The drawer. `onAdded` hears each success; a caller that shows its own success view passes it as `done`, which
 * replaces the form and footer (otherwise the caller closes the drawer).
 */
export function AddWorkspaceDrawer({ projectId, onClose, onAdded, done = null }: {
  projectId?: number;
  onClose: () => void;
  onAdded: (result: AddedWorkspace, connectionName: string) => void;
  done?: { body: ReactNode; footer: ReactNode } | null;
}) {
  const add = useWorkspaceAdd(projectId === undefined ? {} : { projectId });
  const formId = useId();
  const pending = add.mutation.isPending;
  const submit = (event?: FormEvent) => {
    event?.preventDefault();
    add.submit((result, connection) => onAdded(result, add.connectionName(connection)));
  };
  return (
    <Drawer title="Add workspace" onClose={pending ? () => {} : onClose}
      footer={done !== null ? done.footer : <>
        {add.capable ? (
          <Button type="submit" form={formId} variant="primary" busy={pending} disabled={!add.ready}>Add workspace</Button>
        ) : null}
        <Button disabled={pending} onClick={onClose}>{add.capable ? "Cancel" : "Close"}</Button>
      </>}>
      {done !== null ? done.body : (
        <form id={formId} onSubmit={submit} className="flex min-w-0 flex-col">
          <AddWorkspaceFields add={add} fixed={false} onRetry={() => submit()} />
        </form>
      )}
    </Drawer>
  );
}

/**
 * The task form's inline panel: the form's connection, fixed. Not a nested form (it sits inside the task form), so
 * Enter in a field adds the workspace here instead of saving the task.
 */
export function AddWorkspacePanel({ connectionId, projectId, onAdded, onClose }: {
  connectionId: number;
  projectId: number | undefined;
  onAdded: (result: AddedWorkspace) => void;
  onClose: () => void;
}) {
  const add = useWorkspaceAdd(projectId === undefined ? { fixedConnection: connectionId } : { fixedConnection: connectionId, projectId });
  const headingId = useId();
  const panel = useRef<HTMLDivElement>(null);
  const pending = add.mutation.isPending;
  // Focus the first control once the capabilities answer (the first field, or Close when the harness can't add).
  const focused = useRef(false);
  useEffect(() => {
    if (focused.current || add.checking) return;
    const first = [...(panel.current?.querySelectorAll<HTMLElement>("input:not(:disabled), button:not(:disabled)") ?? [])]
      .find((el) => el.getClientRects().length > 0);
    if (first === undefined) return;
    first.focus();
    focused.current = true;
  });
  const submit = () => add.submit(onAdded);
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape" && !pending) {
      // Close the panel, not the drawer around it.
      event.preventDefault();
      event.stopPropagation();
      onClose();
      return;
    }
    if (event.key !== "Enter" || !(event.target instanceof HTMLInputElement) || event.target.type === "checkbox") return;
    event.preventDefault();
    event.stopPropagation();
    submit();
  };
  return (
    <div ref={panel} role="group" aria-labelledby={headingId} onKeyDown={onKeyDown}
      className="flex min-w-0 flex-col gap-3 border border-line-strong bg-panel p-3">
      <h3 id={headingId} className="text-small font-semibold">Add workspace in {add.harnessName}</h3>
      <AddWorkspaceFields add={add} fixed nested onRetry={submit} />
      <div className="flex flex-wrap gap-2">
        {add.capable ? <Button small variant="primary" busy={pending} disabled={!add.ready} onClick={submit}>Add workspace</Button> : null}
        <Button small disabled={pending} onClick={onClose}>{add.capable ? "Cancel" : "Close"}</Button>
      </div>
    </div>
  );
}
