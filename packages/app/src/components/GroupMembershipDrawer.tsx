import { useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState } from "react";
import { ApiError } from "../api/client.ts";
import { useInvalidateProjects, useProjectGroups } from "../api/queries.ts";
import type { GroupEditInput, GroupWorkspace, ProjectGroup } from "../api/types.ts";
import { hostAccessScope } from "../lib/host.ts";
import { requestGroupEdit } from "../lib/projectGroups.ts";
import { useApi, usePlatform } from "../platform/context.tsx";
import { groupStateLabel, groupWorkspaceLabel, groupWorkspaceLocation } from "./GroupWorkspaceDisplay.tsx";
import { Alert, Button, Combobox, Drawer, ErrorText, Field, Meta, Muted, QueryState, TextLink } from "./ui.tsx";
import type { WorkspaceConnections } from "./WorkspaceDisplay.tsx";

export type MembershipAction = { workspace: GroupWorkspace; separate: boolean };

export function GroupMembershipDrawer({ action, group, connections, onClose, onApplied }: {
  action: MembershipAction;
  group: ProjectGroup;
  connections: WorkspaceConnections;
  onClose: () => void;
  onApplied: (target: Pick<ProjectGroup, "id" | "title">) => void;
}) {
  const { workspace, separate } = action;
  const api = useApi();
  const refresh = useInvalidateProjects();
  const host = usePlatform().host?.connection;
  const [scope] = useState(() => hostAccessScope(host?.current() ?? null));
  const inFlight = useRef<AbortController | null>(null);
  const scopeChanged = useRef(false);
  const requestId = useId();
  const projects = useProjectGroups();
  const [target, setTarget] = useState(separate ? null : "");
  const [changed, setChanged] = useState(false);
  const previewHeading = useRef<HTMLHeadingElement>(null);
  const input: GroupEditInput = {
    connectionId: workspace.connectionId,
    workspaceId: workspace.workspaceId,
    targetProjectId: target === null ? null : Number(target),
  };

  useEffect(() => {
    const unlisten = host?.subscribe((state) => {
      if (hostAccessScope(state) === scope) return;
      scopeChanged.current = true;
      inFlight.current?.abort();
      onClose();
    });
    return () => {
      inFlight.current?.abort();
      unlisten?.();
    };
  }, [host, scope, onClose]);

  const preview = useQuery({
    queryKey: ["project-membership-preview", requestId, scope, input],
    queryFn: ({ signal }) => requestGroupEdit(api, host, scope, input, signal),
    enabled: target !== "",
    gcTime: 0,
    refetchOnReconnect: false,
    refetchOnWindowFocus: false,
    retry: false,
  });
  const proposal = preview.isFetching || preview.isError ? undefined : preview.data;
  useEffect(() => {
    if (proposal !== undefined) previewHeading.current?.focus();
  }, [proposal]);

  const confirm = useMutation({
    mutationFn: ({ selection, token, signal }: { selection: GroupEditInput; token: string; signal: AbortSignal }) =>
      requestGroupEdit(api, host, scope, selection, signal, token),
    onError: async (error) => {
      if (!(error instanceof ApiError) || error.code !== "preview_changed") return;
      setChanged(true);
      await Promise.all([preview.refetch(), refresh()]);
    },
    onSuccess: async () => { await refresh(); },
  });
  const apply = () => {
    if (scopeChanged.current || proposal === undefined || proposal.blocked !== null || proposal.previewToken === "" || confirm.isPending) return;
    const controller = new AbortController();
    inFlight.current = controller;
    confirm.mutate({ selection: input, token: proposal.previewToken, signal: controller.signal }, {
      onSuccess: (result) => {
        if (result.applied && result.target !== null) onApplied(result.target);
      },
    });
  };
  const options = (projects.data?.groups ?? [])
    .filter((candidate) => candidate.id !== group.id)
    .toSorted((a, b) => Number(group.relatedProjectIds.includes(b.id)) - Number(group.relatedProjectIds.includes(a.id)))
    .map((candidate) => {
      const first = candidate.members[0];
      const location = first === undefined ? groupStateLabel(candidate) : groupWorkspaceLabel(first, connections);
      const detail = [location, first?.path].filter(Boolean).join(" · ");
      return { value: String(candidate.id), label: `${candidate.title} (#${candidate.id})`, detail };
    });

  return (
    <Drawer title={separate ? "Separate workspace" : "Move workspace to project"}
      onClose={confirm.isPending ? () => {} : onClose}
      footer={<>
        <Button variant="primary" busy={confirm.isPending} disabled={proposal === undefined || proposal.blocked !== null || proposal.previewToken === ""} onClick={apply}>
          {separate ? "Separate workspace" : "Move workspace"}
        </Button>
        <Button data-autofocus={separate ? true : undefined} disabled={confirm.isPending} onClick={onClose}>Cancel</Button>
      </>}>
      <div className="flex min-w-0 flex-col gap-4">
        <div className="flex min-w-0 flex-col gap-1">
          <strong className="wrap-anywhere text-body font-medium">{workspace.title}</strong>
          <Meta className="wrap-anywhere">{groupWorkspaceLabel(workspace, connections)}</Meta>
          <code className="wrap-anywhere text-meta">{groupWorkspaceLocation(workspace)}</code>
        </div>
        {separate ? null : <QueryState isPending={projects.isPending} error={projects.error} hasData={projects.data !== undefined}>
          {options.length === 0 ? <Muted>Create another project before moving this workspace.</Muted> : (
            <Field label="Destination project">
              {(id) => <Combobox id={id} listLabel="Destination projects" autoFocus value={target ?? ""}
                placeholder="Choose a project" options={options} disabled={confirm.isPending} onChange={(value) => {
                  confirm.reset();
                  setChanged(false);
                  setTarget(value);
                }} />}
            </Field>
          )}
        </QueryState>}
        {changed ? <Alert>The grouping changed. Review the updated preview before confirming.</Alert> : null}
        {preview.isError ? <div><Button onClick={() => void preview.refetch()}>Retry preview</Button></div> : null}
        {target === "" ? null : <QueryState isPending={preview.isPending} error={preview.error} hasData={preview.data !== undefined}>
          {proposal === undefined ? null : (
            <div className="flex min-w-0 flex-col gap-2">
              <h3 ref={previewHeading} tabIndex={-1} className="text-title font-semibold">Preview</h3>
              <p className="wrap-anywhere text-body">
                From <TextLink to={`/projects/${proposal.source.id}`}>{proposal.source.title} (#{proposal.source.id})</TextLink> to{" "}
                <strong>{proposal.target === null ? `${proposal.newTitle ?? workspace.title} (new project)` : `${proposal.target.title} (#${proposal.target.id})`}</strong>.
              </p>
              <Muted>Existing tasks, schedules, snippets and history stay in their original projects.</Muted>
              {proposal.blocked === null ? null : <Alert>{proposal.blocked.reason}</Alert>}
            </div>
          )}
        </QueryState>}
        <ErrorText error={confirm.error instanceof ApiError && confirm.error.code === "preview_changed" ? null : confirm.error} />
      </div>
    </Drawer>
  );
}
