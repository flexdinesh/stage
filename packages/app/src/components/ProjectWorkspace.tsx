import { useMutation, useQuery } from "@tanstack/react-query";
import { useRef, useState, type FormEvent } from "react";
import { useNavigate } from "react-router";
import { ApiError } from "../api/client.ts";
import { keys, useInvalidateProjects, useProjects } from "../api/queries.ts";
import type { Project, ProjectBinding, Workspace, WorkspaceRef } from "../api/types.ts";
import { replacedPaths } from "../lib/harnesses.ts";
import { orderedWorkspaces, repoLabel, workspaceLocation } from "../lib/projects.ts";
import { useApi } from "../platform/context.tsx";
import { DeclaredActions } from "./DeclaredActions.tsx";
import { ProjectReference, ResolveWorkspacesDrawer } from "./ProjectPreviews.tsx";
import { WorkspaceDisplay, useWorkspaceConnections } from "./WorkspaceDisplay.tsx";
import {
  Alert, Button, Chip, Combobox, ConfirmInline, Drawer, EmptyState, ErrorText, Field, Input, Menu, MenuItem,
  Meta, Muted, Panel, QueryState, Radio, TextLink, useMenu,
} from "./ui.tsx";

type BindTarget = { connectionId: number; replaces?: number };

export function ProjectWorkspace({ project }: { project: Project }) {
  const api = useApi();
  const refresh = useInvalidateProjects();
  const connections = useWorkspaceConnections();
  const { harnesses, placement } = connections;
  const [binding, setBinding] = useState<BindTarget | null>(null);
  const [split, setSplit] = useState<ProjectBinding | null>(null);
  const [editing, setEditing] = useState<"title" | "archive" | null>(null);
  const [actions, setActions] = useState<number | null>(null);
  const [resolve, setResolve] = useState(false);
  const projects = useProjects("all", { enabled: resolve });
  const [message, setMessage] = useState("");
  const [title, setTitle] = useState(project.title);
  const menu = useMenu();
  const menuTrigger = useRef<HTMLButtonElement>(null);
  const closeEdit = () => {
    setEditing(null);
    menuTrigger.current?.focus({ preventScroll: true });
  };
  const live = harnesses.connectedList.filter((c) => placement(c.id).live);
  const archived = project.archivedAt !== null;
  const archiveLabel = archived ? "Unarchive project" : "Archive project";
  const update = useMutation({
    mutationFn: (body: { title: string; archived: boolean }) => api.updateProject(project.id, body),
    onSuccess: async () => {
      await refresh();
      closeEdit();
    },
  });
  const showEdit = (mode: "title" | "archive") => {
    menu.hide();
    update.reset();
    setTitle(project.title);
    setEditing(mode);
  };

  return (
    <>
      <Panel title="Workspaces" meta={project.bindings.length} flush actions={
        <>
          {archived ? null : <>
            <Button small disabled={live.length === 0} onClick={() => setBinding({ connectionId: live[0]?.id ?? 0 })}>Bind workspace</Button>
            <Button small disabled={live.length === 0} onClick={() => { setMessage(""); setResolve(true); }}>Resolve now</Button>
          </>}
          <Button ref={menuTrigger} small {...menu.triggerProps}>Project actions</Button>
          <Menu menu={menu} label="Project actions" align="right" className="w-48">
            <MenuItem onSelect={() => showEdit("title")}>Edit title</MenuItem>
            <MenuItem danger={!archived} onSelect={() => showEdit("archive")}>{archiveLabel}</MenuItem>
          </Menu>
        </>
      }>
        {editing === null ? null : <div className="border-b border-line p-3">
          {editing === "title" ? (
            <form className="flex flex-col gap-2" onSubmit={(e) => {
              e.preventDefault();
              if (title.trim() && !update.isPending) update.mutate({ title: title.trim(), archived });
            }}>
              <Field label="Project title">{(id) => <Input id={id} autoFocus value={title} onChange={(e) => setTitle(e.target.value)} />}</Field>
              <div className="flex flex-wrap gap-2">
                <Button type="submit" variant="primary" disabled={title.trim() === "" || title.trim() === project.title} busy={update.isPending}>Save title</Button>
                <Button disabled={update.isPending} onClick={closeEdit}>Cancel</Button>
              </div>
              <ErrorText error={update.error} />
            </form>
          ) : (
            <ConfirmInline confirmLabel={archiveLabel} danger={!archived} busy={update.isPending} error={update.error}
              onConfirm={() => update.mutate({ title: project.title, archived: !archived })} onCancel={closeEdit}>
              {archived ? <>Restore <strong>{project.title}</strong> to active projects?</> :
                <>Archive <strong>{project.title}</strong>? Its workspaces, tasks and history stay with it.</>}
            </ConfirmInline>
          )}
        </div>}
        {message === "" ? null : <p role="status" className="border-b border-line px-3 py-2 text-small">{message}</p>}
        <WorkspaceDisplay bindings={project.bindings} connections={connections} renderActions={(workspace, site) => site.retired || archived ? null : (
          <WorkspaceActions project={project} binding={workspace} live={site.live}
            onReplace={() => setBinding({ connectionId: workspace.connectionId, replaces: workspace.id })}
            onSplit={() => setSplit(workspace)}
            actionsOpen={actions === workspace.id}
            toggleActions={() => setActions(actions === workspace.id ? null : workspace.id)} />
        )} />
        {harnesses.loaded && live.length === 0 ? <div className="border-t border-line px-3 py-2 text-small">
          <TextLink to="/settings/connections">Connection settings</TextLink>
        </div> : null}
      </Panel>
      {binding === null ? null : <BindWorkspaceDrawer project={project} target={binding} onTarget={setBinding}
        onClose={() => setBinding(null)} onSuccess={setMessage} />}
      {split === null ? null : <SplitWorkspaceDrawer project={project} binding={split} onClose={() => setSplit(null)} />}
      {resolve ? <ResolveWorkspacesDrawer projects={projects.data?.projects ?? [project]} onClose={() => setResolve(false)} onSuccess={setMessage} /> : null}
    </>
  );
}

function WorkspaceActions({ project, binding, live, onReplace, onSplit, actionsOpen, toggleActions }: {
  project: Project; binding: ProjectBinding; live: boolean; onReplace: () => void; onSplit: () => void;
  actionsOpen: boolean; toggleActions: () => void;
}) {
  const api = useApi();
  const refresh = useInvalidateProjects();
  const [confirm, setConfirm] = useState(false);
  const unbindTrigger = useRef<HTMLButtonElement>(null);
  const name = `${workspaceLocation(binding)} (workspace #${binding.id})`;
  const primary = useMutation({ mutationFn: () => api.setPrimaryBinding(project.id, binding.id), onSuccess: refresh });
  const unbind = useMutation({
    mutationFn: () => api.unbindProject(project.id, binding.id),
    onSuccess: async () => { setConfirm(false); await refresh(); },
  });
  const canRun = live && binding.status === "available";
  return (
    <>
      {binding.status === "changed" ? <Alert>
        The harness reports a changed path. Confirm {binding.observedWorkspace || "the workspace without a path"} before using it.
      </Alert> : null}
      <div className="flex flex-wrap gap-2">
        {binding.status !== "available" ? <Button small disabled={!live} aria-label={`Replace ${name}`} onClick={onReplace}>
          {binding.status === "changed" ? "Confirm changed path" : "Replace workspace"}
        </Button> : null}
        {!binding.primary && binding.status === "available" ? <Button small aria-label={`Make ${name} primary`} busy={primary.isPending} onClick={() => primary.mutate()}>Make primary</Button> : null}
        <Button small variant="ghost" aria-label={`Split ${name}`} onClick={onSplit}>Split</Button>
        <Button ref={unbindTrigger} small variant="ghost" aria-label={`Unbind ${name}`} aria-expanded={confirm} onClick={() => {
          unbind.reset();
          setConfirm(true);
        }}>Unbind</Button>
        {canRun ? <Button small variant="ghost" aria-label={`Harness actions for ${name}`} aria-expanded={actionsOpen} onClick={toggleActions}>Harness actions</Button> : null}
      </div>
      <ErrorText error={primary.error} />
      {confirm ? <ConfirmInline danger confirmLabel="Unbind workspace" busy={unbind.isPending} error={unbind.error}
        onConfirm={() => unbind.mutate()} onCancel={() => { setConfirm(false); unbindTrigger.current?.focus(); }} stackActionsOnPhone>
        Unbind <span className="wrap-anywhere">{workspaceLocation(binding)}</span>? Existing tasks and history stay. Discovery records a decline for this workspace.
      </ConfirmInline> : null}
      {actionsOpen && canRun ? <ProjectHarnessActions binding={binding} /> : null}
    </>
  );
}

function ProjectHarnessActions({ binding }: { binding: ProjectBinding }) {
  const api = useApi();
  const refresh = useInvalidateProjects();
  const scope = { connectionId: binding.connectionId, bindingId: binding.id };
  const query = useQuery({
    queryKey: [...keys.projectActions, binding.projectId, scope.connectionId, scope.bindingId, binding.revision],
    queryFn: () => api.projectActions(binding.projectId, scope),
    retry: false,
  });
  const actions = query.data ?? [];
  return (
    <div className="border-t border-line pt-2">
      <QueryState isPending={query.isPending} error={query.error} hasData={query.data !== undefined}
        empty={actions.length === 0} emptyText="This workspace has no declared project actions.">
        <DeclaredActions actions={actions} run={async (id, input, confirmed) => {
          const result = await api.runProjectAction(binding.projectId, scope, id, input, confirmed);
          await refresh();
          return result;
        }} />
      </QueryState>
    </div>
  );
}

function SplitWorkspaceDrawer({ project, binding, onClose }: { project: Project; binding: ProjectBinding; onClose: () => void }) {
  const [title, setTitle] = useState(binding.title);
  const api = useApi();
  const refresh = useInvalidateProjects();
  const navigate = useNavigate();
  const split = useMutation({
    mutationFn: () => api.splitProject(project.id, { bindingId: binding.id, title: title.trim() }),
    onSuccess: async ({ project: created }) => {
      await refresh();
      onClose();
      void navigate(`/projects/${created.id}`);
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!split.isPending) split.mutate();
  };
  return (
    <Drawer title="Split workspace" onClose={onClose} footer={
      <>
        <Button type="submit" form="split-workspace" variant="primary" busy={split.isPending}>Split into new project</Button>
        <Button data-autofocus disabled={split.isPending} onClick={onClose}>Cancel</Button>
      </>
    }>
      <form id="split-workspace" onSubmit={submit} className="flex flex-col gap-3">
        <Muted><span className="wrap-anywhere">{workspaceLocation(binding)}</span> moves to a new Stage project. Other workspaces, tasks, schedules, snippets and history stay with <strong>{project.title}</strong>.</Muted>
        <Field label="New project title">{(id) => <Input id={id} value={title} onChange={(e) => setTitle(e.target.value)} />}</Field>
        <ErrorText error={split.error} />
      </form>
    </Drawer>
  );
}

function BindWorkspaceDrawer({ project, target, onTarget, onClose, onSuccess }: {
  project: Project; target: BindTarget; onTarget: (target: BindTarget) => void; onClose: () => void; onSuccess: (message: string) => void;
}) {
  const { harnesses, placement } = useWorkspaceConnections();
  const site = placement(target.connectionId);
  return (
    <Drawer title={target.replaces === undefined ? "Bind workspace" : "Replace workspace"} onClose={onClose}
      footer={<Button onClick={onClose}>Cancel</Button>}>
      <div className="flex min-w-0 flex-col gap-3">
        {target.replaces === undefined ? <Field label="Connection">
          {(id) => <Combobox id={id} listLabel="Connections" value={String(target.connectionId)}
            onChange={(value) => onTarget({ connectionId: Number(value) })}
            options={harnesses.connectedList.map((connection) => {
              const option = placement(connection.id);
              return { value: String(connection.id), label: `${option.harness} · ${option.machineName}`, disabled: !option.live };
            })} />}
        </Field> : <Meta className="wrap-anywhere">{site.harness} · {site.machineName}</Meta>}
        {site.hint ? <Muted>{site.hint}</Muted> : null}
        {!site.live ? <Alert>The connection is unavailable.</Alert> : (
          <BindingInventory key={target.connectionId} project={project} target={target} onSaved={() => {
            onSuccess(target.replaces === undefined ? "Workspace bound." : "Workspace replaced.");
            onClose();
          }} />
        )}
      </div>
    </Drawer>
  );
}

function BindingInventory({ project, target, onSaved }: { project: Project; target: BindTarget; onSaved: () => void }) {
  const api = useApi();
  const refresh = useInvalidateProjects();
  const projects = useProjects("all");
  const [query, setQuery] = useState("");
  const [repository, setRepository] = useState("");
  const [selection, setSelection] = useState<WorkspaceRef | null>(null);
  const inventory = useQuery({
    queryKey: [...keys.workspaces, target.connectionId],
    queryFn: () => api.workspaces(target.connectionId),
    retry: false,
  });
  const candidates = inventory.data?.workspaces ?? [];
  const connectionKind = useWorkspaceConnections().harnesses.harness(target.connectionId);
  const previousPaths = connectionKind === null ? new Set<string>() : replacedPaths(project, connectionKind);
  const current = orderedWorkspaces(project.bindings).find((b) => b.id === target.replaces);
  const isSuggestion = (row: Workspace) => row.projectId === null && (row.resolution.candidates.includes(project.id) ||
    (row.workspace !== "" && previousPaths.has(row.workspace)) || (current !== undefined && row.externalId === current.externalProjectId));
  const repositories = [...new Set(candidates.map((candidate) => candidate.remote).filter(Boolean))].toSorted();
  const matches = candidates.filter((row) =>
    (repository === "" || row.remote === repository) &&
    `${row.title} ${row.externalId} ${row.workspace} ${row.remote}`.toLowerCase().includes(query.trim().toLowerCase()),
  ).toSorted((a, b) => Number(isSuggestion(b)) - Number(isSuggestion(a)) ||
    Number(b.resolution.outcome === "join") - Number(a.resolution.outcome === "join") ||
    (a.workspace || a.externalId).localeCompare(b.workspace || b.externalId) || a.externalId.localeCompare(b.externalId));
  const picked = matches.find((row) => row.projectId === null && row.available &&
    selection?.externalId === row.externalId && selection.workspace === row.workspace);
  const bind = useMutation({
    mutationFn: (row: Workspace) => api.bindProject(project.id, {
      connectionId: target.connectionId, externalProjectId: row.externalId, workspace: row.workspace,
      ...(target.replaces === undefined ? {} : { replaces: target.replaces }),
    }),
    onSuccess: async () => { await refresh(); onSaved(); },
    onError: (error) => {
      if (error instanceof ApiError && error.code === "workspace_bound") void inventory.refetch();
    },
  });
  return (
    <>
      <div className="grid gap-2 md:grid-cols-2">
        <Field label="Find workspace">{(id) => <Input id={id} data-autofocus value={query} onChange={(e) => setQuery(e.target.value)} />}</Field>
        {repositories.length === 0 ? null : <Field label="Repository">
          {(id) => <Combobox id={id} listLabel="Repositories" value={repository} onChange={setRepository}
            options={[{ value: "", label: "All repositories" }, ...repositories.map((remote) => ({ value: remote, label: repoLabel(remote) }))]} />}
        </Field>}
      </div>
      <QueryState isPending={inventory.isPending} error={inventory.error} hasData={inventory.data !== undefined}>
        <Panel title="Workspace inventory" flush>
          {matches.length === 0 ? <EmptyState inset>{candidates.length === 0 ? "The harness reported no available workspaces." : "No workspaces match the current filters."}</EmptyState> : (
            <ul className="divide-y divide-line">
              {matches.map((row) => {
                const title = row.title || row.externalId;
                const location = row.workspace || row.externalId;
                return <li key={JSON.stringify([row.externalId, row.workspace])} className="flex min-w-0 gap-2 px-3 py-2">
                  <Radio className="mt-0.5" name="workspace" aria-label={`${title}, ${location}`}
                    disabled={row.projectId !== null || !row.available || bind.isPending} checked={picked === row} onChange={() => setSelection(row)} />
                  <div className="flex min-w-0 flex-col items-start gap-1 text-small">
                    <span className="wrap-anywhere font-medium">{title}</span>
                    <Meta className="wrap-anywhere">{location}{row.remote ? ` · ${repoLabel(row.remote)}` : ""}</Meta>
                    {isSuggestion(row) ? <Chip tone="run">Suggested for this project</Chip> : row.resolution.outcome === "join" ? <Chip tone="run">Discovery match</Chip> : null}
                    {row.declined ? <Chip tone="idle">Declined</Chip> : null}
                    {row.projectId === null ? null : <p>Bound to <ProjectReference id={row.projectId} projects={projects.data?.projects ?? []} /></p>}
                    {row.resolution.candidates.length === 0 ? null : <p className="flex flex-wrap gap-x-2">
                      <span>Candidate projects</span>
                      {row.resolution.candidates.map((id) => <ProjectReference key={id} id={id} projects={projects.data?.projects ?? []} />)}
                    </p>}
                  </div>
                </li>;
              })}
            </ul>
          )}
        </Panel>
      </QueryState>
      <div className="flex flex-wrap gap-2">
        <Button variant="primary" disabled={picked === undefined || inventory.isFetching} busy={bind.isPending}
          onClick={() => { if (picked !== undefined) bind.mutate(picked); }}>
          {target.replaces === undefined ? "Bind workspace" : "Replace workspace"}
        </Button>
        <Button busy={inventory.isFetching} onClick={() => { void inventory.refetch(); }}>Refresh workspaces</Button>
      </div>
      <ErrorText error={bind.error} />
    </>
  );
}
