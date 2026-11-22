import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { ExternalProject, HarnessConnection, Project, ProjectBinding } from "../api/types.ts";
import { rebindCandidate, replacedPaths, retiredBindings } from "../lib/harnesses.ts";
import { repoLabel } from "../lib/projects.ts";
import { invalidateWork, keys, useHarnesses, useMachines } from "../api/queries.ts";
import { machineTag, withMachine } from "../lib/machines.ts";
import { useApi } from "../platform/context.tsx";
import { Alert, Button, ConfirmInline, Drawer, EmptyState, ErrorText, Field, Hint, Input, Meta, Muted, Panel, QueryState, Radio, SubHeading, TextLink, cx, rowClass } from "./ui.tsx";

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
  const harnesses = useHarnesses();
  const { machines } = useMachines();
  const name = withMachine(harnesses.name(connectionId), machineTag(machines, harnesses.connection(connectionId)?.machineId ?? 0));
  const harness = harnesses.harness(connectionId);
  // The replaced binding's path is marked, never preselected.
  const samePath = harness === null ? new Set<string>() : replacedPaths(project, harness);
  const external = useExternalProjects(connectionId, true);
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
        <Muted>
          Choose the {name} workspace for {project.title}. New launches use
          this workspace. Existing sessions and launches already in progress keep their original
          workspace.
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
        <QueryState isPending={external.isPending} error={external.error} hasData={external.data !== undefined}>
          <Panel title={`${name} workspaces`} flush>
            {candidates.length === 0 ? (
              <EmptyState inset>No matching workspaces. Open the project in {name} and refresh.</EmptyState>
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
                    className={cx(rowClass, "flex items-start gap-3")}
                  >
                    <Radio
                      name="workspace"
                      checked={selected}
                      disabled={unavailable || bind.isPending}
                      onChange={() => setPicked(candidate)}
                      className="mt-0.5"
                    />
                    <span className="min-w-0">
                      <span className="block text-small font-medium">
                        {candidate.title || candidate.externalId}
                      </span>
                      <Meta className="block break-all">
                        {candidate.workspace}
                        {candidate.repo === "" ? null : ` · ${repoLabel(candidate.repo)}`}
                      </Meta>
                      <span className="block text-small text-muted">
                        {unavailable
                          ? `Bound to Stage project #${candidate.projectId}. Unbind it there first.`
                          : (suggestions[candidate.externalId] ??
                            "Choose the directory you want this project to use.")}
                      </span>
                      {!unavailable && samePath.has(candidate.workspace) ? (
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
            busy={external.isFetching}
            onClick={() => {
              void external.refetch();
            }}
          >
            Refresh workspaces
          </Button>
          <TextLink to="/settings" variant="rest" className="text-small">
            Connection settings
          </TextLink>
        </div>
        {harness !== "t3" ? null : registering ? (
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
            <Muted>Registers the directory in T3. Stage does not create or clone a directory.</Muted>
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

/** A connection's workspace inventory (shared by the drawer and the one-click rebind). */
/** How discovery bound a workspace; manual bindings show no label. */
const autoOriginLabel: Record<ProjectBinding["origin"], string | null> = {
  manual: null,
  imported: "auto · imported",
  path: "auto · same path",
  remote: "auto · same Git remote",
};

function useExternalProjects(connectionId: number, enabled: boolean) {
  const api = useApi();
  return useQuery({
    queryKey: ["external-projects", connectionId],
    queryFn: () => api.externalProjects(connectionId),
    enabled,
    retry: false,
  });
}

export function ProjectWorkspace({ project }: { project: Project }) {
  const api = useApi();
  const qc = useQueryClient();
  const harnesses = useHarnesses();
  const [binding, setBinding] = useState<number | null>(null);
  const [suggestions, setSuggestions] = useState<Record<string, string>>({});
  const [matched, setMatched] = useState("");
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(project.title);
  // Bindings change the project and its workspace lookups (awaited) and what its tasks can start (behind).
  const refresh = async () => {
    void invalidateWork(qc);
    await Promise.all([qc.invalidateQueries({ queryKey: keys.projects }), qc.invalidateQueries({ queryKey: ["external-projects"] })]);
  };
  const update = useMutation({
    // Archive sends the live title, never the draft: the endpoint takes both fields.
    mutationFn: (body: { title: string; archived: boolean }) => api.updateProject(project.id, body),
    onSuccess: async () => {
      setEditing(false);
      await refresh();
    },
  });
  const match = useMutation({
    mutationFn: (id: number) => api.matchWorkspace(project.id, id),
    onSuccess: async (result, id) => {
      if (result.bound) {
        setMatched(`${harnesses.name(id)} bound by shared Git checkout and subproject.`);
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
          <ErrorText error={harnesses.error ?? match.error} />
          {matched ? (
            <p role="status" className="text-small">
              {matched}
            </p>
          ) : null}
          {harnesses.activeList.map((c) => (
            <ConnectionWorkspace
              key={c.id}
              project={project}
              connection={c}
              matching={match.isPending && match.variables === c.id}
              onMatch={() => {
                setMatched("");
                match.mutate(c.id);
              }}
              refresh={refresh}
            />
          ))}
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

/** One active connection's binding: status, repair, unbind, and the trail of bindings on replaced connections. */
function ConnectionWorkspace({
  project,
  connection: c,
  matching,
  onMatch,
  refresh,
}: {
  project: Project;
  connection: HarnessConnection;
  matching: boolean;
  onMatch: () => void;
  refresh: () => Promise<void>;
}) {
  const api = useApi();
  const [confirmUnbind, setConfirmUnbind] = useState(false);
  const { machines } = useMachines();
  const name = withMachine(useHarnesses().kindName(c.harness), machineTag(machines, c.machineId));
  const current = project.bindings.find((b) => b.connectionId === c.id);
  const previous = retiredBindings(project, c.harness);
  // Only an unbound project with a replaced binding looks for the same path on this connection.
  const external = useExternalProjects(c.id, current === undefined && previous.length > 0);
  const candidate = rebindCandidate(project, c, external.data?.projects ?? []);
  const unbind = useMutation({
    mutationFn: () => api.unbindProject(project.id, c.id),
    onSuccess: async () => {
      setConfirmUnbind(false);
      await refresh();
    },
  });
  const rebind = useMutation({
    mutationFn: (target: ExternalProject) => api.bindProject(project.id, target),
    onSuccess: refresh,
  });
  return (
    <section className="flex flex-col gap-2 border-t border-line pt-3">
      <SubHeading title={`${name} workspace`} />
      {current === undefined ? (
        <Muted>Plan tasks here. Bind a workspace before launching.</Muted>
      ) : (
        <>
          <p className="break-all font-mono text-meta">{current.workspace}</p>
          {autoOriginLabel[current.origin] === null ? null : (
            <p className="font-mono text-meta text-muted">{autoOriginLabel[current.origin]}</p>
          )}
          {current.status === "available" ? null : (
            <Alert>
              {current.status === "changed"
                ? `The harness changed this workspace to ${current.observedWorkspace ?? "an unknown path"}. Confirm the workspace before launching.`
                : "This workspace is unavailable. Choose a replacement to launch new work. Existing tasks and history remain here."}
            </Alert>
          )}
        </>
      )}
      {previous.map((b) => (
        <p key={b.id} className="break-all font-mono text-meta text-muted">
          Previously {b.workspace} on replaced {name}
          {b.connectionLabel === "" ? "" : ` '${b.connectionLabel}'`}
        </p>
      ))}
      <div className="flex min-w-0 flex-wrap gap-2">
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
        <Button busy={matching} onClick={onMatch}>
          {current === undefined
            ? "Bind workspace"
            : current.status === "changed"
              ? "Confirm changed path"
              : "Change workspace"}
        </Button>
        {current === undefined ? null : (
          <Button variant="ghost" onClick={() => setConfirmUnbind(true)}>
            Unbind
          </Button>
        )}
      </div>
      <ErrorText error={rebind.error ?? unbind.error} />
      {confirmUnbind ? (
        <ConfirmInline confirmLabel="Unbind workspace" busy={unbind.isPending} onConfirm={() => unbind.mutate()} onCancel={() => setConfirmUnbind(false)}>
          Unbind this workspace? Planning and history remain available. New launches need a binding. Discovery won&apos;t bind this workspace
          again; bind it here to undo.
        </ConfirmInline>
      ) : null}
    </section>
  );
}
