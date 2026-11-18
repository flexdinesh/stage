import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import type { ExternalProject, Project } from "../api/types.ts";
import { harnessName } from "../lib/tasks.ts";
import { keys, useConnections } from "../api/queries.ts";
import { useApi } from "../platform/context.tsx";
import { Alert, Button, Drawer, ErrorText, Field, Input, Panel, QueryState } from "./ui.tsx";

export function BindWorkspaceDrawer({
  project,
  connectionId,
  suggestions,
  onClose,
}: {
  project: Project;
  connectionId: number;
  suggestions: Record<string, string>;
  onClose: () => void;
}) {
  const api = useApi();
  const qc = useQueryClient();
  const [query, setQuery] = useState("");
  const [picked, setPicked] = useState<ExternalProject | null>(null);
  const [registering, setRegistering] = useState(false);
  const [workspace, setWorkspace] = useState("");
  const [registrationAttempted, setRegistrationAttempted] = useState(false);
  const external = useQuery({
    queryKey: ["external-projects", connectionId],
    queryFn: () => api.externalProjects(connectionId),
    retry: false,
  });
  const bind = useMutation({
    mutationFn: (candidate: ExternalProject) => api.bindProject(project.id, candidate),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: keys.projects });
      onClose();
    },
  });
  const register = useMutation({
    mutationFn: () => api.registerWorkspace({ title: project.title, workspace: workspace.trim() }),
    onSuccess: async (candidate) => {
      setPicked(candidate);
      setRegistering(false);
      await external.refetch();
    },
  });
  const candidates = (external.data?.projects ?? []).filter((candidate) =>
    `${candidate.title} ${candidate.workspace}`.toLowerCase().includes(query.toLowerCase()),
  );
  return (
    <Drawer
      title="Bind workspace"
      onClose={onClose}
      footer={
        <>
          <Button
            variant="primary"
            busy={bind.isPending}
            disabled={picked === null}
            onClick={() => {
              if (picked !== null) bind.mutate(picked);
            }}
          >
            Bind workspace
          </Button>
          <Button onClick={onClose}>Cancel</Button>
        </>
      }
    >
      <div className="flex flex-col gap-3.5">
        <p className="text-small text-muted">
          Choose the {harnessName(connectionId)} workspace for {project.title}. New launches use
          this workspace. Existing sessions and launches already in progress keep their original
          workspace.
        </p>
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
        <QueryState isPending={external.isPending} error={external.error}>
          <Panel title={`${harnessName(connectionId)} workspaces`} flush>
            {candidates.length === 0 ? (
              <p className="p-3 text-small text-muted">
                No matching workspaces. Open the project in {harnessName(connectionId)} and refresh.
              </p>
            ) : (
              candidates.map((candidate) => {
                const unavailable =
                  candidate.projectId !== null && candidate.projectId !== project.id;
                const selected =
                  picked?.externalId === candidate.externalId &&
                  picked.workspace === candidate.workspace;
                return (
                  <label
                    key={`${candidate.externalId}:${candidate.workspace}`}
                    className="flex items-start gap-3 border-b border-line p-3 last:border-b-0"
                  >
                    <input
                      type="radio"
                      name="workspace"
                      checked={selected}
                      disabled={unavailable || bind.isPending}
                      onChange={() => setPicked(candidate)}
                      className="mt-1 accent-fg"
                    />
                    <span className="min-w-0">
                      <span className="block text-small font-medium">
                        {candidate.title || candidate.externalId}
                      </span>
                      <span className="block break-all font-mono text-meta text-muted">
                        {candidate.workspace}
                      </span>
                      <span className="block text-small text-muted">
                        {unavailable
                          ? `Bound to Stage project #${candidate.projectId}. Unbind it there first.`
                          : (suggestions[candidate.externalId] ??
                            "Choose the directory you want this project to use.")}
                      </span>
                    </span>
                  </label>
                );
              })
            )}
          </Panel>
        </QueryState>
        <div className="flex flex-wrap gap-2">
          <Button
            busy={external.isFetching}
            onClick={() => {
              void external.refetch();
            }}
          >
            Refresh workspaces
          </Button>
          <Link to="/settings" className="text-small underline">
            Connection settings
          </Link>
        </div>
        {connectionId !== 1 ? null : registering ? (
          <div className="flex flex-col gap-3 border-t border-line pt-3">
            <Field label="Existing directory on the T3 machine">
              {(id) => (
                <Input
                  id={id}
                  value={workspace}
                  onChange={(event) => {
                    setWorkspace(event.target.value);
                    setRegistrationAttempted(false);
                    register.reset();
                  }}
                  className="font-mono"
                />
              )}
            </Field>
            <p className="text-small text-muted">
              Registers the directory in T3. Stage does not create or clone a directory.
            </p>
            <Button
              busy={register.isPending}
              disabled={workspace.trim() === "" || registrationAttempted}
              onClick={() => {
                setRegistrationAttempted(true);
                register.mutate();
              }}
            >
              Register directory in T3
            </Button>
            <ErrorText error={register.error} />
            {register.isError ? (
              <Alert>
                Registration may have reached T3. Refresh workspaces before trying again.
              </Alert>
            ) : null}
          </div>
        ) : (
          <Button variant="ghost" onClick={() => setRegistering(true)}>
            Register existing directory in T3
          </Button>
        )}
        <ErrorText error={bind.error} />
      </div>
    </Drawer>
  );
}

export function ProjectWorkspace({ project }: { project: Project }) {
  const api = useApi();
  const qc = useQueryClient();
  const connections = useConnections();
  const [binding, setBinding] = useState<number | null>(null);
  const [suggestions, setSuggestions] = useState<Record<string, string>>({});
  const [matched, setMatched] = useState("");
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(project.title);
  const [confirmUnbind, setConfirmUnbind] = useState<number | null>(null);
  const refresh = async () => {
    await qc.invalidateQueries();
  };
  const update = useMutation({
    mutationFn: (archived: boolean) =>
      api.updateProject(project.id, { title: title.trim(), archived }),
    onSuccess: async () => {
      setEditing(false);
      await refresh();
    },
  });
  const unbind = useMutation({
    mutationFn: (id: number) => api.unbindProject(project.id, id),
    onSuccess: async () => {
      setConfirmUnbind(null);
      await refresh();
    },
  });
  const match = useMutation({
    mutationFn: (id: number) => api.matchWorkspace(project.id, id),
    onSuccess: async (result, id) => {
      if (result.bound) {
        setMatched(`${harnessName(id)} bound by shared Git checkout and subproject.`);
        await refresh();
      } else {
        setSuggestions(result.suggestions);
        setBinding(id);
      }
    },
  });
  return (
    <>
      <Panel
        title="Workspace"
        actions={
          <Button small variant="ghost" onClick={() => setEditing(!editing)}>
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
                  onClick={() => update.mutate(project.archivedAt !== null)}
                >
                  Save title
                </Button>
                <Button
                  busy={update.isPending}
                  onClick={() => update.mutate(project.archivedAt === null)}
                >
                  {project.archivedAt === null ? "Archive project" : "Unarchive project"}
                </Button>
              </div>
              <ErrorText error={update.error} />
            </div>
          ) : null}
          <ErrorText error={connections.error ?? match.error} />
          {matched ? (
            <p role="status" className="text-small">
              {matched}
            </p>
          ) : null}
          {connections.data?.connections.map((c) => {
            const current = project.bindings.find((b) => b.connectionId === c.id);
            return (
              <section key={c.id} className="flex flex-col gap-2 border-t border-line pt-3">
                <h3 className="text-small font-medium">{harnessName(c.id)} workspace</h3>
                {current === undefined ? (
                  <p className="text-small text-muted">
                    Plan tasks here. Bind a workspace before launching.
                  </p>
                ) : (
                  <>
                    <p className="break-all font-mono text-meta">{current.workspace}</p>
                    {current.status === "available" ? null : (
                      <Alert>
                        {current.status === "changed"
                          ? `The harness changed this workspace to ${current.observedWorkspace ?? "an unknown path"}. Confirm the workspace before launching.`
                          : "This workspace is unavailable. Choose a replacement to launch new work. Existing tasks and history remain here."}
                      </Alert>
                    )}
                  </>
                )}
                <div className="flex flex-wrap gap-2">
                  <Button
                    busy={match.isPending && match.variables === c.id}
                    onClick={() => {
                      setMatched("");
                      match.mutate(c.id);
                    }}
                  >
                    {" "}
                    {current === undefined
                      ? "Bind workspace"
                      : current.status === "changed"
                        ? "Confirm changed path"
                        : "Change workspace"}
                  </Button>
                  {current === undefined ? null : (
                    <Button variant="ghost" onClick={() => setConfirmUnbind(c.id)}>
                      Unbind
                    </Button>
                  )}
                </div>
                {confirmUnbind === c.id ? (
                  <Alert>
                    Unbind this workspace? Planning and history remain available. New launches need
                    a binding.
                    <div className="mt-2 flex gap-2">
                      <Button busy={unbind.isPending} onClick={() => unbind.mutate(c.id)}>
                        Unbind workspace
                      </Button>
                      <Button onClick={() => setConfirmUnbind(null)}>Cancel</Button>
                    </div>
                  </Alert>
                ) : null}
              </section>
            );
          })}
          <ErrorText error={unbind.error} />
        </div>
      </Panel>
      {binding !== null ? (
        <BindWorkspaceDrawer
          project={project}
          connectionId={binding}
          suggestions={suggestions}
          onClose={() => setBinding(null)}
        />
      ) : null}
    </>
  );
}
