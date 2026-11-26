import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { HarnessConnection, Project, ProjectBinding, Workspace, WorkspaceRef } from "../api/types.ts";
import { rebindCandidate, replacedPaths, retiredBindings } from "../lib/harnesses.ts";
import { repoLabel } from "../lib/projects.ts";
import { invalidateWork, keys, useHarnesses, useMachines } from "../api/queries.ts";
import { machineTag, withMachine } from "../lib/machines.ts";
import { useApi } from "../platform/context.tsx";
import { Alert, Button, ConfirmInline, Drawer, EmptyState, ErrorText, Field, Hint, Input, Meta, Muted, Panel, QueryState, Radio, SubHeading, TextLink, cx, rowClass } from "./ui.tsx";

function BindWorkspaceDrawer({
  project,
  connectionId,
  current,
  refresh,
  onClose,
}: {
  project: Project;
  connectionId: number;
  current: ProjectBinding | undefined;
  refresh: () => Promise<void>;
  onClose: () => void;
}) {
  const api = useApi();
  const [query, setQuery] = useState("");
  const [selection, setSelection] = useState<WorkspaceRef | null>(null);
  const harnesses = useHarnesses();
  const { machines } = useMachines();
  const name = withMachine(harnesses.name(connectionId), machineTag(machines, harnesses.connection(connectionId)?.machineId ?? 0));
  const harness = harnesses.harness(connectionId);
  // The replaced binding's path is marked, never preselected.
  const samePath = harness === null ? new Set<string>() : replacedPaths(project, harness);
  const inventory = useWorkspaces(connectionId, true);
  const candidates = inventory.data?.workspaces ?? [];
  const replaces = current?.status === "available" ? undefined : current?.id;
  const changePrimary = current?.primary === true && replaces === undefined;
  const confirmLabel = changePrimary ? "Use as primary" : replaces === undefined ? "Bind workspace" : "Replace workspace";
  const canPick = (candidate: Workspace) => candidate.available && (
    candidate.projectId === null ||
    (changePrimary && candidate.projectId === project.id && !candidate.primary && candidate.bindingId !== null)
  );
  const picked = candidates.find((candidate) =>
    selection?.externalId === candidate.externalId && selection.workspace === candidate.workspace && canPick(candidate),
  );
  const bind = useMutation({
    mutationFn: (candidate: Workspace) =>
      candidate.bindingId !== null
        ? api.setPrimaryBinding(project.id, candidate.bindingId)
        : api.bindProject(project.id, {
            connectionId,
            externalProjectId: candidate.externalId,
            workspace: candidate.workspace,
            ...(replaces === undefined ? {} : { replaces }),
            ...(changePrimary ? { primary: true } : {}),
          }),
    onSuccess: async () => {
      await refresh();
      onClose();
    },
  });
  const matches = candidates.filter((candidate) =>
    `${candidate.title} ${candidate.externalId} ${candidate.workspace}`.toLowerCase().includes(query.toLowerCase()),
  );
  return (
    <Drawer
      title={changePrimary ? "Change primary workspace" : confirmLabel}
      onClose={onClose}
      footer={
        <>
          <Button
            variant="primary"
            busy={bind.isPending}
            disabled={picked === undefined || inventory.isFetching}
            onClick={() => {
              if (picked !== undefined) bind.mutate(picked);
            }}
          >
            {confirmLabel}
          </Button>
          <Button onClick={onClose}>Cancel</Button>
        </>
      }
    >
      <div className="flex flex-col gap-3.5">
        <Muted>
          {changePrimary
            ? `Choose the default ${name} workspace for new launches on ${project.title}.`
            : `Choose a ${name} workspace for ${project.title}. Existing sessions and launches in progress keep their workspace.`}
        </Muted>
        <Field label="Find workspace">
          {(id) => (
            <Input
              id={id}
              data-autofocus
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
          )}
        </Field>
        <QueryState isPending={inventory.isPending} error={inventory.error} hasData={inventory.data !== undefined}>
          <Panel title={`${name} workspaces`} flush>
            {matches.length === 0 ? (
              <EmptyState inset>No matching workspaces. Open the project in {name} and refresh.</EmptyState>
            ) : (
              matches.map((candidate) => {
                const owner = candidate.projectId;
                const unavailable = !canPick(candidate);
                const suggestion = unavailable ? null : workspaceSuggestion(candidate.resolution, project.id);
                const title = candidate.title || candidate.externalId;
                const path = candidate.workspace || (title === candidate.externalId ? "" : candidate.externalId);
                const detail = [path, repoLabel(candidate.remote)].filter(Boolean).join(" · ");
                return (
                  <label
                    key={JSON.stringify([candidate.externalId, candidate.workspace])}
                    className={cx(rowClass, "flex items-start gap-3")}
                  >
                    <Radio
                      name="workspace"
                      checked={picked === candidate}
                      disabled={unavailable || bind.isPending}
                      onChange={() => setSelection(candidate)}
                      className="mt-0.5"
                    />
                    <span className="min-w-0">
                      <span className="block break-all text-small font-medium">{title}</span>
                      {detail === "" ? null : <Meta className="block break-all">{detail}</Meta>}
                      {owner !== null ? (
                        <Hint inline className="block">
                          {owner === project.id ? "Already bound to this project" : `Bound to Stage project #${owner}`}
                        </Hint>
                      ) : null}
                      {candidate.available ? null : <Hint inline className="block">Workspace unavailable</Hint>}
                      {suggestion ? <Hint inline className="block">{suggestion}</Hint> : null}
                      {!unavailable && candidate.workspace !== "" && samePath.has(candidate.workspace) ? (
                        <Hint inline className="block">Same path as the replaced binding</Hint>
                      ) : null}
                    </span>
                  </label>
                );
              })
            )}
          </Panel>
        </QueryState>
        <div className="flex flex-wrap gap-2">
          <Button
            busy={inventory.isFetching}
            onClick={() => {
              void inventory.refetch();
            }}
          >
            Refresh workspaces
          </Button>
          <TextLink to="/settings/connections" variant="rest" className="text-small">
            Connection settings
          </TextLink>
        </div>
        <ErrorText error={bind.error} />
      </div>
    </Drawer>
  );
}

/** How discovery bound a workspace; manual bindings show no label. */
const bindingRuleLabel: Record<ProjectBinding["rule"], string | null> = {
  manual: null,
  new: "auto · imported",
  local: "auto · same local workspace",
  remote: "auto · same Git remote",
  moved: "auto · moved workspace",
};

const suggestionLabel: Partial<Record<Workspace["resolution"]["rule"], string>> = {
  local: "Suggested for this project by local workspace",
  remote: "Suggested for this project by Git remote and subpath",
  moved: "Possible move from this project",
};

function workspaceSuggestion(resolution: Workspace["resolution"], projectId: number): string | null {
  if (resolution.candidates.includes(projectId)) return suggestionLabel[resolution.rule] ?? null;
  if (resolution.outcome !== "join") return null;
  switch (resolution.rule) {
    case "local": return "Discovery matched a local workspace";
    case "remote": return "Discovery matched the Git remote and subpath";
    case "moved": return "Discovery matched a moved workspace";
    default: return null;
  }
}

function useWorkspaces(connectionId: number, enabled: boolean) {
  const api = useApi();
  return useQuery({
    queryKey: [...keys.workspaces, connectionId],
    queryFn: () => api.workspaces(connectionId),
    enabled,
    retry: false,
  });
}

export function ProjectWorkspace({ project }: { project: Project }) {
  const api = useApi();
  const qc = useQueryClient();
  const harnesses = useHarnesses();
  const [binding, setBinding] = useState<{ connectionId: number; current: ProjectBinding | undefined } | null>(null);
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(project.title);
  // Bindings change the project and its workspace lookups (awaited) and what its tasks can start (behind).
  const refresh = async () => {
    void invalidateWork(qc);
    await Promise.all([
      qc.invalidateQueries({ queryKey: keys.projects }),
      qc.invalidateQueries({ queryKey: keys.workspaces }),
    ]);
  };
  const update = useMutation({
    // Archive sends the live title, never the draft: the endpoint takes both fields.
    mutationFn: (body: { title: string; archived: boolean }) => api.updateProject(project.id, body),
    onSuccess: async () => {
      setEditing(false);
      await refresh();
    },
  });
  return (
    <>
      <Panel
        title="Workspace"
        actions={
          <Button
            small
            variant="ghost"
            onClick={() => {
              // The draft starts from the current title each time editing opens.
              if (!editing) setTitle(project.title);
              setEditing(!editing);
            }}
          >
            Edit project
          </Button>
        }
      >
        <div className="flex flex-col gap-3">
          {editing ? (
            <div className="flex flex-col gap-2">
              <Field label="Project title">
                {(id) => (
                  <Input id={id} value={title} onChange={(event) => setTitle(event.target.value)} />
                )}
              </Field>
              <div className="flex flex-wrap gap-2">
                <Button
                  disabled={title.trim() === ""}
                  busy={update.isPending}
                  onClick={() => update.mutate({ title: title.trim(), archived: project.archivedAt !== null })}
                >
                  Save title
                </Button>
                <Button
                  busy={update.isPending}
                  onClick={() => update.mutate({ title: project.title, archived: project.archivedAt === null })}
                >
                  {project.archivedAt === null ? "Archive project" : "Unarchive project"}
                </Button>
              </div>
              <ErrorText error={update.error} />
            </div>
          ) : null}
          <ErrorText error={harnesses.error} />
          {harnesses.connectedList.map((c) => (
            <ConnectionWorkspace
              key={c.id}
              project={project}
              connection={c}
              onMatch={(current) => setBinding({ connectionId: c.id, current })}
              refresh={refresh}
            />
          ))}
        </div>
      </Panel>
      {binding !== null ? (
        <BindWorkspaceDrawer
          project={project}
          connectionId={binding.connectionId}
          current={binding.current}
          refresh={refresh}
          onClose={() => setBinding(null)}
        />
      ) : null}
    </>
  );
}

/** A connection's current workspaces and its bindings on replaced connections. */
function ConnectionWorkspace({
  project,
  connection: c,
  onMatch,
  refresh,
}: {
  project: Project;
  connection: HarnessConnection;
  onMatch: (current?: ProjectBinding) => void;
  refresh: () => Promise<void>;
}) {
  const api = useApi();
  const [confirmUnbind, setConfirmUnbind] = useState<number | null>(null);
  const { machines } = useMachines();
  const name = withMachine(useHarnesses().kindName(c.harness), machineTag(machines, c.machineId));
  const current = project.bindings.filter((b) => b.connectionActive && b.connectionId === c.id)
    .toSorted((a, b) => Number(b.primary) - Number(a.primary));
  const previous = retiredBindings(project, c.harness);
  // Only an unbound project with a replaced binding looks for the same path on this connection.
  const inventory = useWorkspaces(c.id, current.length === 0 && previous.length > 0);
  const candidate = rebindCandidate(project, c, inventory.data?.workspaces ?? []);
  const unbind = useMutation({
    mutationFn: (bindingId: number) => api.unbindProject(project.id, bindingId),
    onSuccess: async () => {
      setConfirmUnbind(null);
      await refresh();
    },
  });
  const rebind = useMutation({
    mutationFn: (target: Workspace) => api.bindProject(project.id, {
      connectionId: c.id,
      externalProjectId: target.externalId,
      workspace: target.workspace,
    }),
    onSuccess: refresh,
  });
  return (
    <section className="flex flex-col gap-2 border-t border-line pt-3">
      <SubHeading title={`${name} workspace`} />
      {current.length === 0 ? (
        <Muted>Plan tasks here. Bind a workspace before launching.</Muted>
      ) : current.map((b) => (
        <div className="flex flex-col gap-2" key={b.id}>
          <p className="break-all font-mono text-meta">{b.workspace || b.title || b.externalProjectId}</p>
          {current.length > 1 && b.primary ? <Meta>Primary workspace</Meta> : null}
          {bindingRuleLabel[b.rule] === null ? null : (
            <Meta>{bindingRuleLabel[b.rule]}</Meta>
          )}
          {b.status === "available" ? null : (
            <Alert>
              {b.status === "changed"
                ? `The harness changed this workspace to ${b.observedWorkspace || "a workspace without a path"}. Confirm the workspace to use it again.`
                : "This workspace is unavailable. Choose a replacement to use it again. Existing tasks and history remain here."}
            </Alert>
          )}
          <div className="flex min-w-0 flex-wrap gap-2">
            {b.primary || b.status !== "available" ? (
              <Button disabled={unbind.isPending} onClick={() => onMatch(b)}>
                {b.status === "changed" ? "Confirm changed path" : b.status === "missing" ? "Replace workspace" : "Change workspace"}
              </Button>
            ) : null}
            <Button variant="ghost" disabled={unbind.isPending} onClick={() => {
              unbind.reset();
              setConfirmUnbind(b.id);
            }}>
              Unbind
            </Button>
          </div>
          {confirmUnbind === b.id ? (
            <ConfirmInline confirmLabel="Unbind workspace" busy={unbind.isPending} error={unbind.error} stackActionsOnPhone onConfirm={() => unbind.mutate(b.id)} onCancel={() => setConfirmUnbind(null)}>
              Unbind <span className="break-all">{b.workspace || b.title || b.externalProjectId}</span>? Stage keeps existing tasks and history. Discovery skips this workspace until you bind it again.
            </ConfirmInline>
          ) : null}
        </div>
      ))}
      {previous.map((b) => (
        <p key={b.id} className="break-all font-mono text-meta text-muted">
          Previously {b.workspace || b.title || b.externalProjectId} on replaced {name}
          {b.connectionLabel === "" ? "" : ` '${b.connectionLabel}'`}
        </p>
      ))}
      {current.length === 0 ? <div className="flex min-w-0 flex-wrap gap-2">
        {candidate === null ? null : (
          <Button
            variant="primary"
            className="max-w-full"
            title={`Bind ${candidate.workspace}`}
            busy={rebind.isPending}
            onClick={() => rebind.mutate(candidate)}
          >
            <span className="min-w-0 truncate">Bind {candidate.workspace}</span>
          </Button>
        )}
        <Button onClick={() => onMatch()}>Bind workspace</Button>
      </div> : null}
      <ErrorText error={rebind.error} />
    </section>
  );
}
