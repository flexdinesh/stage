import { useMutation, useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { keys, useDestinations, useInvalidateProjects, useProject } from "../api/queries.ts";
import type { GroupWorkspace, ProjectGroup } from "../api/types.ts";
import { canRunProjectActions, groupWorkspaceBlocked } from "../lib/projectGroups.ts";
import { useApi } from "../platform/context.tsx";
import { DeclaredActions } from "./DeclaredActions.tsx";
import { GroupMembershipDrawer, type MembershipAction } from "./GroupMembershipDrawer.tsx";
import { GroupWorkspaceState, groupWorkspaceLabel, groupWorkspaceLocation } from "./GroupWorkspaceDisplay.tsx";
import { RecordedWorkspaceDisplay, type WorkspaceConnections } from "./WorkspaceDisplay.tsx";
import {
  Button, ConfirmInline, CopyButton, EmptyState, ErrorText, Field, Input, Menu, MenuItem,
  Meta, Panel, QueryState, Row, RowList, TextLink, useMenu,
} from "./ui.tsx";

export function ProjectGroupWorkspaces({ group, connections }: {
  group: ProjectGroup;
  connections: WorkspaceConnections;
}) {
  const api = useApi();
  const refresh = useInvalidateProjects();
  const destinations = useDestinations();
  const [editing, setEditing] = useState<"title" | "archive" | null>(null);
  const [title, setTitle] = useState(group.title);
  const [action, setAction] = useState<MembershipAction | null>(null);
  const closeMembership = useCallback(() => setAction(null), []);
  const [moved, setMoved] = useState<Pick<ProjectGroup, "id" | "title"> | null>(null);
  const feedback = useRef<HTMLParagraphElement>(null);
  const menu = useMenu();
  const menuTrigger = useRef<HTMLButtonElement>(null);
  const archived = group.archivedAt !== null;
  const archiveLabel = archived ? "Restore project" : "Archive project";
  const closeEdit = () => {
    setEditing(null);
    menuTrigger.current?.focus();
  };
  const update = useMutation({
    mutationFn: (input: { title: string; archived: boolean }) => api.updateProject(group.id, input),
    onSuccess: refresh,
  });
  const save = (input: { title: string; archived: boolean }) => update.mutate(input, { onSuccess: closeEdit });
  const showEdit = (edit: "title" | "archive") => {
    update.reset();
    setTitle(group.title);
    setEditing(edit);
  };
  useEffect(() => { if (moved !== null) feedback.current?.focus(); }, [moved]);

  return (
    <>
      <Panel flush title="Workspaces" meta={group.members.length} actions={<>
        <Button ref={menuTrigger} small {...menu.triggerProps}>Project actions</Button>
        <Menu menu={menu} label="Project actions" align="right" className="w-48">
          <MenuItem onSelect={() => showEdit("title")}>Edit title</MenuItem>
          <MenuItem danger={!archived} onSelect={() => showEdit("archive")}>{archiveLabel}</MenuItem>
        </Menu>
      </>}>
        {editing === null ? null : <div className="border-b border-line p-3">
          {editing === "title" ? (
            <form className="flex flex-col gap-2" onSubmit={(event) => {
              event.preventDefault();
              if (!update.isPending && title.trim() && title.trim() !== group.title) save({ title: title.trim(), archived });
            }}>
              <Field label="Project title">{(id) => <Input id={id} autoFocus value={title} onChange={(e) => setTitle(e.target.value)} />}</Field>
              <div className="flex flex-wrap gap-2">
                <Button type="submit" variant="primary" disabled={title.trim() === "" || title.trim() === group.title} busy={update.isPending}>Save title</Button>
                <Button disabled={update.isPending} onClick={closeEdit}>Cancel</Button>
              </div>
              <ErrorText error={update.error} />
            </form>
          ) : (
            <ConfirmInline confirmLabel={archiveLabel} danger={!archived} busy={update.isPending} error={update.error}
              onConfirm={() => save({ title: group.title, archived: !archived })} onCancel={closeEdit}>
              {archived ? <>Restore <strong>{group.title}</strong>?</> : <>Archive <strong>{group.title}</strong>? It will remain available in Project history.</>}
            </ConfirmInline>
          )}
        </div>}
        {moved === null ? null : <p ref={feedback} tabIndex={-1} role="status" className="border-b border-line px-3 py-2 text-small wrap-anywhere">
          Workspace moved to <TextLink to={`/projects/${moved.id}`}>{moved.title}</TextLink>.
        </p>}
        {group.members.length === 0 ? <EmptyState inset>No workspaces in this project. Recorded work stays here.</EmptyState> : (
          <RowList>
            {group.members.map((workspace) => {
              const site = groupWorkspaceLabel(workspace, connections);
              const path = groupWorkspaceLocation(workspace);
              const blocked = groupWorkspaceBlocked(workspace);
              return (
                <Row key={workspace.workspaceId} className="grid min-w-0 gap-2 md:grid-cols-[minmax(0,1fr)_auto] md:items-start">
                  <div className="flex min-w-0 flex-col gap-1">
                    <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                      <span className="wrap-anywhere text-small font-medium">{workspace.title}</span>
                      <GroupWorkspaceState workspace={workspace} connections={connections} />
                    </div>
                    <Meta className="wrap-anywhere">{site}</Meta>
                    <div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                      <code className="min-w-0 wrap-anywhere text-meta">{path}</code>
                      <CopyButton text={path} label="Copy" accessibleLabel={`Copy ${path} on ${site}`} variant="ghost" />
                    </div>
                    {blocked === null ? null : <p className="text-small text-muted wrap-anywhere">{blocked}</p>}
                    {canRunProjectActions(group.id, workspace, destinations.data?.destinations)
                      ? <ProjectHarnessActions projectId={group.id} workspace={workspace} initiallyOpen={blocked !== null} /> : null}
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <Button small aria-label={`Move workspace ${path} on ${site} to another project`}
                      onClick={() => { setMoved(null); setAction({ workspace, separate: false }); }}>Move to project</Button>
                    {group.members.length < 2 ? null : <Button small variant="ghost" aria-label={`Separate workspace ${path} on ${site}`}
                      onClick={() => { setMoved(null); setAction({ workspace, separate: true }); }}>Separate</Button>}
                  </div>
                </Row>
              );
            })}
          </RowList>
        )}
        <RecordedWorkspaces projectId={group.id} connections={connections} />
      </Panel>
      {action === null ? null : <GroupMembershipDrawer action={action} group={group} connections={connections}
        onClose={closeMembership} onApplied={(target) => { setMoved(target); closeMembership(); }} />}
    </>
  );
}

function RecordedWorkspaces({ projectId, connections }: {
  projectId: number;
  connections: WorkspaceConnections;
}) {
  const project = useProject(projectId);
  const bindings = project.data?.bindings ?? [];
  if (bindings.length === 0) return null;
  return (
    <details className="border-t border-line">
      <summary className="cursor-pointer px-3 py-2 text-small text-muted">Recorded workspace bindings</summary>
      <RecordedWorkspaceDisplay bindings={bindings} connections={connections} />
    </details>
  );
}

/** A blocked workspace opens its actions at once: one of them may be the fix (trusting the folder). */
function ProjectHarnessActions({ projectId, workspace, initiallyOpen }: { projectId: number; workspace: GroupWorkspace; initiallyOpen: boolean }) {
  const api = useApi();
  const refresh = useInvalidateProjects();
  const [open, setOpen] = useState(initiallyOpen);
  const scope = { connectionId: workspace.connectionId, workspaceId: workspace.workspaceId };
  const query = useQuery({
    queryKey: [...keys.projectActions, projectId, scope.connectionId, scope.workspaceId],
    queryFn: ({ signal }) => api.projectActions(projectId, scope, signal),
    enabled: open,
    retry: false,
  });
  const actions = query.data ?? [];
  return (
    <>
      <div><Button small variant="ghost" aria-expanded={open} onClick={() => setOpen(!open)}>Harness actions</Button></div>
      {open ? <QueryState isPending={query.isPending} error={query.error} hasData={query.data !== undefined}
        empty={actions.length === 0} emptyText="This workspace has no declared project actions.">
        <DeclaredActions actions={actions} run={async (id, input, confirmed) => {
          const result = await api.runProjectAction(projectId, scope, id, input, confirmed);
          await refresh();
          return result;
        }} />
      </QueryState> : null}
    </>
  );
}
