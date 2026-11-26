import { useMutation } from "@tanstack/react-query";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useNavigate } from "react-router";
import { ApiError } from "../api/client.ts";
import { useInvalidateProjects } from "../api/queries.ts";
import type { Project, ProjectMergeResult, WorkspaceResolveResult } from "../api/types.ts";
import { useApi } from "../platform/context.tsx";
import { useWorkspaceConnections } from "./WorkspaceDisplay.tsx";
import { Alert, Button, Combobox, Drawer, ErrorText, Field, Meta, Muted, TextLink } from "./ui.tsx";

export function ProjectReference({ id, projects }: { id: number; projects: readonly Project[] }) {
  const name = projects.find((project) => project.id === id)?.title;
  return <TextLink to={`/projects/${id}`} className="wrap-anywhere">{name === undefined ? `Project #${id}` : `${name} (#${id})`}</TextLink>;
}

function PreviewDrawer<T>({
  title, onClose, read, apply, confirmation, canApply, render, onApplied, renderContext,
}: {
  title: string;
  onClose: () => void;
  read: () => Promise<T>;
  apply: (preview: T) => Promise<T>;
  confirmation: (preview: T) => string;
  canApply: (preview: T) => boolean;
  render: (preview: T) => ReactNode;
  onApplied: (result: T) => Promise<void>;
  renderContext: (busy: boolean) => ReactNode;
}) {
  const [preview, setPreview] = useState<T | null>(null);
  const [changed, setChanged] = useState(false);
  const previewPanel = useRef<HTMLDivElement>(null);
  const readPreview = useMutation({
    mutationFn: read,
    onSuccess: setPreview,
  });
  const confirm = useMutation({
    mutationFn: apply,
    onSuccess: onApplied,
    onError: (error) => {
      if (!(error instanceof ApiError) || error.code !== "preview_changed") return;
      setPreview(null);
      setChanged(true);
      readPreview.mutate();
    },
  });
  useEffect(() => { if (preview !== null) previewPanel.current?.focus({ preventScroll: true }); }, [preview]);

  return (
    <Drawer title={title} onClose={confirm.isPending ? () => {} : onClose} footer={
      <>
        {preview === null ? (
          <Button variant="primary" busy={readPreview.isPending} onClick={() => readPreview.mutate()}>Preview</Button>
        ) : (
          <Button variant="primary" className="h-auto min-h-control max-w-full py-1 text-left whitespace-normal wrap-anywhere"
            busy={confirm.isPending} disabled={!canApply(preview)} onClick={() => confirm.mutate(preview)}>
            {confirmation(preview)}
          </Button>
        )}
        <Button data-autofocus disabled={confirm.isPending} onClick={onClose}>Cancel</Button>
      </>
    }>
      <div className="flex min-w-0 flex-col gap-4">
        {renderContext(readPreview.isPending || confirm.isPending)}
        {readPreview.isPending ? <Muted>Loading preview…</Muted> : null}
        <ErrorText error={readPreview.error} />
        <ErrorText error={changed && confirm.error instanceof ApiError && confirm.error.code === "preview_changed" ? null : confirm.error} />
        {preview === null ? null : (
          <div ref={previewPanel} tabIndex={-1} aria-label={changed ? "Updated preview" : "Preview"} className="flex min-w-0 flex-col gap-2">
            {changed ? <Alert>The preview changed. Review the current plan before confirming.</Alert> : null}
            {render(preview)}
          </div>
        )}
      </div>
    </Drawer>
  );
}

export function ResolveWorkspacesDrawer({ initialConnection, projects, onClose, onSuccess }: {
  initialConnection?: number;
  projects: readonly Project[];
  onClose: () => void;
  onSuccess: (message: string) => void;
}) {
  const { harnesses, placement } = useWorkspaceConnections();
  const [connection, setConnection] = useState(initialConnection ?? harnesses.connectedList.find((c) => placement(c.id).live)?.id ?? 0);
  const api = useApi();
  const refresh = useInvalidateProjects();
  const site = placement(connection);
  if (!site.live) {
    return (
      <Drawer title="Resolve workspaces" onClose={onClose}>
        <Muted>{connection === 0 ? "No online harness connections are available." : `${site.harness} on ${site.machineName} is unavailable.`}</Muted>
        <TextLink to="/settings/connections">Connection settings</TextLink>
      </Drawer>
    );
  }
  return (
    <PreviewDrawer<WorkspaceResolveResult>
      key={connection}
      title="Resolve workspaces"
      renderContext={(busy) =>
        <>
          {initialConnection === undefined ? (
            <Field label="Connection">
              {(id) => <Combobox id={id} listLabel="Connections" disabled={busy} value={String(connection)} onChange={(value) => setConnection(Number(value))}
                options={harnesses.connectedList.filter((c) => placement(c.id).live).map((c) => {
                  const target = placement(c.id);
                  return { value: String(c.id), label: `${target.harness} · ${target.machineName}` };
                })} />}
            </Field>
          ) : <p className="wrap-anywhere text-small">{site.harness} · {site.machineName}</p>}
          {site.hint ? <Muted>{site.hint}</Muted> : null}
          <Muted>Preview which workspaces Stage will add, join or move. Workspaces that need a decision stay in Discovery review.</Muted>
        </>
      }
      read={() => api.resolveWorkspaces(connection, { apply: false })}
      apply={(preview) => api.resolveWorkspaces(connection, { apply: true, previewToken: preview.previewToken })}
      confirmation={() => "Apply workspace changes"}
      canApply={(preview) => preview.placements.some((row) => row.acts)}
      render={(preview) => (
        <div className="flex flex-col gap-2 border border-line">
          <p className="bg-panel px-3 py-2 text-small">
            {preview.created} new · {preview.joined} joins · {preview.moved} moves · {preview.review} for review
          </p>
          {preview.placements.length === 0 ? <p className="px-3 pb-2 text-small text-muted">No workspace changes.</p> : null}
          <ul className="divide-y divide-line">
            {preview.placements.map((row) => (
              <li key={JSON.stringify([row.workspace.externalId, row.workspace.workspace])} className="flex flex-col gap-1 px-3 py-2 text-small">
                <span className="wrap-anywhere font-medium">{row.workspace.title || row.workspace.externalId}</span>
                <Meta className="wrap-anywhere">{row.workspace.workspace || row.workspace.externalId}</Meta>
                {row.acts ? (
                  row.projectId === null ? <span>Create {row.newProjectTitle}</span> :
                    <span>{row.rule === "moved" ? "Move to " : "Bind to "}<ProjectReference id={row.projectId} projects={projects} /></span>
                ) : (
                  <span>{row.outcome === "bound" ? "Already bound" : row.reason ? `Review · ${row.reason}` : "No change"}</span>
                )}
                {row.candidates.length === 0 ? null : <span className="flex flex-wrap gap-x-2">Candidates {row.candidates.map((id) => <ProjectReference key={id} id={id} projects={projects} />)}</span>}
              </li>
            ))}
          </ul>
        </div>
      )}
      onClose={onClose}
      onApplied={async () => {
        await refresh();
        onSuccess("Workspace changes applied.");
        onClose();
      }}
    />
  );
}

export function MergeProjectsDrawer({ pair, projects, onClose }: {
  pair?: readonly [number, number];
  projects: readonly Project[];
  onClose: () => void;
}) {
  const [into, setInto] = useState(pair?.[0] ?? 0);
  const [other, setOther] = useState(pair?.[1] ?? 0);
  const from = pair === undefined ? other : pair.find((id) => id !== into) ?? 0;
  const api = useApi();
  const navigate = useNavigate();
  const refresh = useInvalidateProjects();
  const options = projects.filter((p) => pair?.includes(p.id) ?? true).map((p) => ({ value: String(p.id), label: `${p.title} (#${p.id})` }));
  const choices = (busy: boolean) => (
    <>
      <Field label="Project to keep">
        {(id) => <Combobox id={id} listLabel="Projects to keep" disabled={busy} placeholder="Select project" value={String(into)} options={options} onChange={(value) => {
          const next = Number(value);
          setInto(next);
          if (other === next) setOther(0);
        }} />}
      </Field>
      {pair === undefined ? <Field label="Project to merge into it">
        {(id) => <Combobox id={id} listLabel="Projects to merge" disabled={busy} placeholder="Select project" value={String(from)}
          options={options.filter((p) => p.value !== String(into))} onChange={(value) => setOther(Number(value))} />}
      </Field> : <p className="text-small">Merge <ProjectReference id={from} projects={projects} /> into the project to keep.</p>}
    </>
  );
  if (from === 0 || into === 0 || from === into) {
    return <Drawer title="Merge projects" onClose={onClose} footer={<Button onClick={onClose}>Cancel</Button>}>
      <div className="flex flex-col gap-4">{choices(false)}</div>
    </Drawer>;
  }
  return (
    <PreviewDrawer<ProjectMergeResult>
      key={`${into}/${from}`}
      title="Merge projects"
      renderContext={choices}
      read={() => api.mergeProject(into, { from })}
      apply={(preview) => api.mergeProject(into, { from, confirm: true, previewToken: preview.previewToken })}
      confirmation={(preview) => `Merge into ${preview.project.title}`}
      canApply={(preview) => preview.blocked === null}
      render={(preview) => (
        <div className="flex min-w-0 flex-col gap-2">
          <p className="wrap-anywhere text-small"><strong>{preview.project.title}</strong> stays. Links to <strong>{preview.from.title}</strong> will open it.</p>
          <dl className="grid grid-cols-2 gap-px border border-line bg-line md:grid-cols-4">
            {Object.entries(preview.moves).map(([key, count]) => (
              <div key={key} className="flex flex-col-reverse gap-1 bg-panel px-3 py-2">
                <dt><Meta>{key}</Meta></dt><dd className="font-mono text-title">{count}</dd>
              </div>
            ))}
          </dl>
          {preview.blocked === null ? null : <Alert tone="bad">{preview.blocked.reason}</Alert>}
        </div>
      )}
      onClose={onClose}
      onApplied={async (result) => {
        await refresh();
        onClose();
        void navigate(`/projects/${result.project.id}`);
      }}
    />
  );
}
