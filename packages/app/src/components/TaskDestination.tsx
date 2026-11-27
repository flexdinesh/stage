import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { keys, useHarnesses, useMachines } from "../api/queries.ts";
import type { HarnessConnection, Workspace } from "../api/types.ts";
import { POLL } from "../lib/polling.ts";
import { availableTaskWorkspaces, suggestedTaskWorkspace } from "../lib/taskForm.ts";
import { useApi } from "../platform/context.tsx";
import { Button, Combobox, ErrorText, Field, Hint, TextLink, type ComboboxOption } from "./ui.tsx";

interface WorkspaceChoice {
  id: string;
  label: string;
  detail: string;
}

function workspaceChoice(row: Workspace & { workspaceId: string }): WorkspaceChoice {
  const detail = row.workspace || row.externalId;
  return { id: row.workspaceId, label: row.title || detail, detail };
}

export function useTaskDestination({ initialConnection, initialWorkspace, allowPrefill, projectId, enabled }: {
  initialConnection: number | null;
  initialWorkspace: WorkspaceChoice | null;
  allowPrefill: boolean;
  projectId: number | undefined;
  enabled: boolean;
}) {
  const api = useApi();
  const client = useQueryClient();
  const harnesses = useHarnesses();
  const { machines } = useMachines();
  const [draft, setDraft] = useState<{
    connection: number | null;
    workspace: WorkspaceChoice | null | undefined;
  }>(() => ({ connection: initialConnection, workspace: allowPrefill ? undefined : initialWorkspace }));
  const connection = draft.connection ?? 0;
  const picked = harnesses.connection(connection);
  const selectable = harnesses.connectedList;
  const sole = selectable.length === 1 ? selectable[0] : undefined;
  if (draft.connection === null && allowPrefill && harnesses.loaded && sole !== undefined) {
    setDraft({ connection: sole.id, workspace: undefined });
  }
  const queryKey = [...keys.workspaces, connection, "task-form"];
  const inventory = useQuery({
    queryKey,
    queryFn: ({ signal }) => api.workspaces(connection, false, signal),
    enabled: enabled && picked?.active === true,
    retry: false,
    refetchInterval: (q) => {
      const state = q.state.data?.inventory?.state;
      return state === "pending" || state === "refreshing" ? POLL.live : POLL.slow;
    },
  });
  const refresh = useMutation({ mutationFn: (id: number) => api.workspaces(id) });
  const refreshing = refresh.isPending;
  const state = inventory.data?.inventory?.state;
  const ready = !refreshing && !refresh.isError && !inventory.isError && picked?.active === true && picked.configured && state === "ready";
  const rows = ready ? availableTaskWorkspaces(inventory.data, connection, projectId) : [];
  const prefill = draft.workspace !== undefined ? undefined : suggestedTaskWorkspace(rows, projectId);
  if (prefill !== undefined) setDraft({ connection, workspace: workspaceChoice(prefill) });

  const saved = draft.workspace;
  const selected = rows.find((row) => row.workspaceId === (saved?.id ?? prefill?.workspaceId));
  const option = (row: Workspace & { workspaceId: string }): ComboboxOption => {
    const choice = workspaceChoice(row);
    return { value: choice.id, label: choice.label, detail: choice.detail === choice.label ? undefined : choice.detail };
  };
  const workspaceOptions = rows.map(option);
  if (saved != null && selected === undefined) {
    const lastKnown = inventory.data?.workspaces.find((row) => row.connectionId === connection && row.workspaceId === saved.id);
    workspaceOptions.push({ value: saved.id, label: lastKnown?.title || saved.label, detail: saved.detail, disabled: true });
  }

  const machineLabel = (c: HarnessConnection) => machines.find((m) => m.id === c.machineId)?.label || `Machine #${c.machineId}`;
  const describe = (c: HarnessConnection): ComboboxOption => ({
    value: String(c.id), label: harnesses.kindName(c.harness), tag: machineLabel(c), detail: c.label || undefined,
    chip: c.id === initialConnection && allowPrefill ? "default" : !c.configured ? "disconnected" : undefined,
  });
  const connectionOptions = harnesses.activeList.map(describe);
  if (connection > 0 && picked?.active !== true) {
    connectionOptions.push(picked === undefined
      ? { value: String(connection), label: harnesses.loaded ? "Saved connection" : "Loading connection…", disabled: true }
      : { ...describe(picked), chip: "retired", disabled: true });
  }

  const loading = connection > 0 && (!harnesses.loaded || refreshing || (picked?.active === true && picked.configured && (inventory.isPending || state === "pending" || state === "refreshing")));
  let status: string | null = null;
  if (loading) status = "Discovering workspaces…";
  else if (connection > 0 && (picked?.active !== true || !picked.configured || state === "unavailable" || state === "retired")) {
    status = saved == null ? "Connection unavailable." : "Connection unavailable. The saved workspace needs verification.";
  } else if (inventory.isError || refresh.isError || state === "stale") {
    status = "Couldn’t verify the workspace inventory. Refresh workspaces to try again.";
  } else if (ready && saved != null && selected === undefined) {
    status = "The selected workspace is no longer available. Choose a workspace to run this work.";
  } else if (ready && rows.length === 0) {
    status = inventory.data?.workspaces.length === 0 ? "This connection has no known workspaces." : "This connection has no eligible workspaces.";
  }

  return {
    harnesses, picked, connection, connectionOptions, workspaceOptions, selected, status, loading,
    workspaceId: saved?.id ?? prefill?.workspaceId ?? null,
    canRefresh: picked?.active === true,
    refreshing,
    canPlan: picked?.active === true,
    workspacePlaceholder: connection === 0 ? "Choose a connection first" : loading ? "Discovering workspaces…" : "Choose a workspace…",
    connectionName: picked === undefined ? harnesses.name(connection) : `${harnesses.kindName(picked.harness)} · ${machineLabel(picked)}`,
    chooseConnection(id: number) {
      refresh.reset();
      setDraft({ connection: id, workspace: allowPrefill ? undefined : null });
    },
    chooseWorkspace(id: string) {
      const row = rows.find((w) => w.workspaceId === id);
      setDraft({ connection, workspace: row === undefined ? null : workspaceChoice(row) });
    },
    refresh() {
      refresh.mutate(connection, { onSuccess: (data) => {
        client.setQueryData(queryKey, data);
        void client.invalidateQueries({ queryKey: [...keys.projects, "destinations"] });
        void client.invalidateQueries({ queryKey: ["capabilities", connection] });
      } });
    },
  };
}

export function TaskDestinationFields({ destination: d, readOnly, canClear, onConnection, onWorkspace }: {
  destination: ReturnType<typeof useTaskDestination>;
  readOnly: boolean;
  canClear: boolean;
  onConnection: (id: number) => void;
  onWorkspace: (id: string) => void;
}) {
  return (
    <div className="flex flex-col gap-2">
      <div className="grid min-w-0 grid-cols-1 gap-3 md:grid-cols-2">
        <Field label="Connection">
          {(id) => <Combobox id={id} value={d.connection === 0 ? "" : String(d.connection)} options={d.connectionOptions}
            onChange={(next) => onConnection(Number(next))} listLabel="Connections" placeholder="Choose a connection…"
            searchable={false} showTag autoFocus={!readOnly} disabled={readOnly || d.connectionOptions.length === 0} />}
        </Field>
        <Field label="Workspace" hint={d.selected === undefined ? undefined : (
          <span className="font-mono wrap-anywhere">{d.selected.workspace || d.selected.externalId}</span>
        )}>
          {(id) => <Combobox key={d.connection} id={id} value={d.workspaceId ?? ""} options={d.workspaceOptions}
            onChange={onWorkspace} listLabel="Workspaces" placeholder={d.workspacePlaceholder} searchable
            disabled={readOnly || d.selected === undefined && d.workspaceOptions.every((o) => o.disabled === true)} />}
        </Field>
      </div>
      {readOnly ? null : (
        <div className="flex flex-wrap items-center justify-end gap-x-3 gap-y-1">
          <div role="status" className="min-w-0 grow">{d.status === null ? null : <Hint>{d.status}</Hint>}</div>
          {d.canRefresh ? <Button small variant="ghost" busy={d.refreshing} onClick={d.refresh}>Refresh workspaces</Button> : null}
          {canClear && d.workspaceId !== null ? <Button small variant="ghost" onClick={() => onWorkspace("")}>Clear workspace</Button> : null}
          {d.connectionOptions.length === 0 && d.harnesses.loaded ? <Hint>Connect a harness in <TextLink to="/settings/connections" variant="rest">Settings</TextLink>.</Hint> : null}
        </div>
      )}
      <ErrorText error={d.harnesses.error} />
    </div>
  );
}
