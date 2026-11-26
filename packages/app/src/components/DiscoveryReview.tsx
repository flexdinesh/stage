import { useMutation, useQuery } from "@tanstack/react-query";
import { useRef, useState, type FormEvent } from "react";
import { keys, useInvalidateProjects, useProjects } from "../api/queries.ts";
import type { Project, Workspace, WorkspaceReview } from "../api/types.ts";
import { useApi } from "../platform/context.tsx";
import { POLL } from "../lib/polling.ts";
import { repoLabel } from "../lib/projects.ts";
import { MergeProjectsDrawer, ProjectReference, ResolveWorkspacesDrawer } from "./ProjectPreviews.tsx";
import { ProjectCombobox } from "./ProjectSelect.tsx";
import { useWorkspaceConnections, type WorkspaceConnections } from "./WorkspaceDisplay.tsx";
import { Alert, Button, Chip, Drawer, EmptyState, ErrorText, Field, Input, Meta, Muted, Panel, QueryState, TextLink, type ChipTone } from "./ui.tsx";

type ReviewRow = WorkspaceReview["workspaces"][number];
type Reason = ReviewRow["reason"];
const REASON_ORDER: Reason[] = ["new", "suggested", "ambiguous", "declined", "moved"];

const REASONS: Record<Reason, { label: string; detail: string; tone: ChipTone }> = {
  new: { label: "New", detail: "Discovery found no matching Stage project.", tone: "plain" },
  suggested: { label: "Suggested", detail: "Workspace evidence points to a Stage project.", tone: "run" },
  ambiguous: { label: "Ambiguous", detail: "Workspace evidence matches several Stage projects.", tone: "need" },
  declined: { label: "Declined", detail: "Discovery skips this workspace until you undo the decline or bind it.", tone: "idle" },
  moved: { label: "Moved", detail: "Discovery found a possible move and needs you to choose a project.", tone: "need" },
};

export function useDiscoveryReview() {
  const api = useApi();
  return useQuery({ queryKey: [...keys.workspaces, "review"], queryFn: api.workspaceReview, refetchInterval: POLL.slow });
}

export function DiscoveryReview({ review }: { review: ReturnType<typeof useDiscoveryReview> }) {
  const projects = useProjects("all");
  const list = projects.data?.projects ?? [];
  const connections = useWorkspaceConnections();
  const { harnesses, machines, placement } = connections;
  const [resolve, setResolve] = useState(false);
  const [merge, setMerge] = useState<"manual" | [number, number] | null>(null);
  const [message, setMessage] = useState("");
  const fallback = useRef<HTMLHeadingElement>(null);
  const returnFocus = () => fallback.current?.focus({ preventScroll: true });
  const live = harnesses.connectedList.some((c) => placement(c.id).live);
  const reviewData = review.data;
  const pairs = (reviewData?.merges ?? []).flatMap((group) => group.pairs);
  const pairKeys = new Set<string>();
  const uniquePairs: [number, number][] = [];
  for (const [a, b] of pairs) {
    if (pairKeys.has(`${a}/${b}`)) continue;
    pairKeys.add(`${a}/${b}`);
    uniquePairs.push([a, b]);
  }
  const rows = reviewData?.workspaces ?? [];
  return (
    <>
      <Panel title={<span ref={fallback} tabIndex={-1}>Discovery review</span>} flush actions={
        <>
          <Button small disabled={!live} onClick={() => { setMessage(""); setResolve(true); }}>Resolve now</Button>
          <Button small disabled={list.filter((project) => project.archivedAt === null).length < 2} onClick={() => setMerge("manual")}>Merge projects</Button>
        </>
      }>
        <div className="px-3 py-2">
          {message === "" ? null : <div role="status"><Alert tone="plain">{message}</Alert></div>}
          <ErrorText error={harnesses.error ?? machines.error} />
          <ErrorText error={projects.error} />
          {reviewData === undefined ? null : (
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <Meta>{reviewData.attention} need attention</Meta>
              {REASON_ORDER.map((reason) => (
                <Meta key={reason}>{REASONS[reason].label} {reviewData.counts[reason]}</Meta>
              ))}
              <Meta>Merge suggestions {reviewData.counts.merges}</Meta>
            </div>
          )}
        </div>
        <QueryState isPending={review.isPending} error={review.error} hasData={reviewData !== undefined}>
          {rows.length === 0 && uniquePairs.length === 0 && review.error === null ? <EmptyState inset>No workspaces need review.</EmptyState> : null}
          <ul className="divide-y divide-line">
            {rows.map((row) => <WorkspaceReviewRow key={JSON.stringify([row.connectionId, row.externalId, row.workspace])} row={row} projects={list} connections={connections} onDone={returnFocus} />)}
            {uniquePairs.map(([a, b]) => (
              <MergeSuggestion key={`${a}/${b}`} pair={[a, b]} projects={list} onMerge={() => setMerge([a, b])} onDone={returnFocus} />
            ))}
          </ul>
        </QueryState>
        {!harnesses.loaded || live ? null : <p className="border-t border-line px-3 py-2 text-small text-muted">
          {harnesses.connectedList.length === 0 ? "Connect a harness to discover workspaces." : "Workspaces can be resolved when a harness connection is online."}{" "}
          <TextLink to="/settings/connections">Connection settings</TextLink>
        </p>}
      </Panel>
      {resolve ? <ResolveWorkspacesDrawer projects={list} onClose={() => setResolve(false)} onSuccess={setMessage} /> : null}
      {merge === null ? null : <MergeProjectsDrawer {...(merge === "manual" ? {} : { pair: merge })} projects={list.filter((p) => p.archivedAt === null || (merge !== "manual" && merge.includes(p.id)))} onClose={() => setMerge(null)} />}
    </>
  );
}

function WorkspaceReviewRow({ row, projects, connections, onDone }: { row: ReviewRow; projects: readonly Project[]; connections: WorkspaceConnections; onDone: () => void }) {
  const [mode, setMode] = useState<"bind" | "create" | null>(null);
  const api = useApi();
  const refresh = useInvalidateProjects();
  const site = connections.placement(row.connectionId);
  const reason = REASONS[row.reason];
  const location = row.workspace || row.externalId;
  const title = row.title || row.externalId;
  const name = `${title}, ${location} on ${site.machineName}`;
  const owner = row.projectId;
  const mutateDecline = useMutation({
    mutationFn: () => {
      const body = { externalId: row.externalId, workspace: row.workspace };
      return row.declined ? api.undeclineWorkspace(row.connectionId, body) : api.declineWorkspace(row.connectionId, body);
    },
    onSuccess: async () => {
      await refresh();
      onDone();
    },
  });
  return (
    <li className="grid min-w-0 gap-2 border-t border-line px-3 py-3 md:grid-cols-[minmax(0,1fr)_auto] md:items-start">
      <div className="flex min-w-0 flex-col gap-1">
        <div className="flex flex-wrap items-center gap-2">
          <Chip tone={reason.tone} dot>{reason.label}</Chip>
          <span className="wrap-anywhere text-body font-medium">{title}</span>
        </div>
        <Meta className="wrap-anywhere">{site.machineName} · {site.harness}</Meta>
        <Meta className="wrap-anywhere">{location}{row.remote ? ` · ${repoLabel(row.remote)}` : ""}</Meta>
        {site.machineStatus === null ? null : <span><Chip tone="idle">Machine {site.machineStatus.toLowerCase()}</Chip></span>}
        {site.disconnected ? <span><Chip tone="idle">Disconnected</Chip></span> : null}
        <Muted>{reason.detail}</Muted>
        {owner === null ? null : <p className="text-small">Owner <ProjectReference id={owner} projects={projects} /></p>}
        {row.resolution.candidates.length === 0 ? null : <p className="flex flex-wrap gap-x-2 text-small">
          <span>Candidate projects</span>
          {row.resolution.candidates.map((id) => <ProjectReference key={id} id={id} projects={projects} />)}
        </p>}
        <ErrorText error={mutateDecline.error} />
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {owner === null ? (
          <>
            <Button small disabled={!site.live || !row.available} aria-label={`Bind ${name}`} onClick={() => setMode("bind")}>Bind</Button>
            <Button small disabled={!site.live || !row.available} aria-label={`Create project for ${name}`} onClick={() => setMode("create")}>Create project</Button>
          </>
        ) : null}
        {owner === null || row.declined ? <Button small variant="ghost" busy={mutateDecline.isPending} aria-label={`${row.declined ? "Undo decline for" : "Decline"} ${name}`} onClick={() => mutateDecline.mutate()}>
          {row.declined ? "Undo decline" : "Decline"}
        </Button> : null}
      </div>
      {mode === null ? null : (
        <ReviewDecisionDrawer key={mode} mode={mode} workspace={row} projects={projects} live={site.live}
          onClose={() => setMode(null)} onDone={onDone} />
      )}
    </li>
  );
}

function ReviewDecisionDrawer({ mode, workspace, projects, live, onClose, onDone }: {
  mode: "bind" | "create";
  workspace: Workspace;
  projects: readonly Project[];
  live: boolean;
  onClose: () => void;
  onDone: () => void;
}) {
  const [project, setProject] = useState("");
  const [title, setTitle] = useState(workspace.title || workspace.externalId);
  const api = useApi();
  const refresh = useInvalidateProjects();
  const label = mode === "bind" ? "Bind workspace" : "Create project";
  const save = useMutation({
    mutationFn: () => {
      const binding = { connectionId: workspace.connectionId, externalProjectId: workspace.externalId, workspace: workspace.workspace };
      return mode === "bind" ? api.bindProject(Number(project), binding) : api.createProject({ title: title.trim(), bindings: [binding] });
    },
    onSuccess: async () => {
      onClose();
      await refresh();
      onDone();
    },
  });
  const valid = live && (mode === "bind" ? projects.some((p) => String(p.id) === project && p.archivedAt === null) : title.trim() !== "");
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (valid && !save.isPending) save.mutate();
  };
  return (
    <Drawer title={label} onClose={onClose} footer={
      <>
        <Button type="submit" form="review-workspace" variant="primary" disabled={!valid} busy={save.isPending}>{label}</Button>
        <Button onClick={onClose}>Cancel</Button>
      </>
    }>
      <form id="review-workspace" onSubmit={submit} className="flex min-w-0 flex-col gap-3">
        <Meta className="wrap-anywhere">{workspace.workspace || workspace.externalId}</Meta>
        {mode === "bind" ? (
          <Field label="Project">
            {(id) => <ProjectCombobox id={id} value={project} onChange={setProject} projects={projects.filter((p) => p.archivedAt === null)} autoFocus />}
          </Field>
        ) : (
          <Field label="Project title">
            {(id) => <Input id={id} data-autofocus value={title} onChange={(event) => setTitle(event.target.value)} />}
          </Field>
        )}
        {live ? null : <Muted>The connection is unavailable.</Muted>}
        <ErrorText error={save.error} />
      </form>
    </Drawer>
  );
}

function MergeSuggestion({ pair: [a, b], projects, onMerge, onDone }: {
  pair: [number, number]; projects: readonly Project[]; onMerge: () => void; onDone: () => void;
}) {
  const api = useApi();
  const refresh = useInvalidateProjects();
  const dismiss = useMutation({
    mutationFn: () => api.dismissProjectMerge(a, b),
    onSuccess: async () => {
      await refresh();
      onDone();
    },
  });
  return (
    <li className="flex min-w-0 flex-wrap items-start gap-3 border-t border-line px-3 py-3">
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <span><Chip tone="need" dot>Merge suggested</Chip></span>
        <p className="flex flex-wrap gap-x-2 text-small"><ProjectReference id={a} projects={projects} /> <span>and</span> <ProjectReference id={b} projects={projects} /></p>
        <ErrorText error={dismiss.error} />
      </div>
      <div className="flex flex-wrap gap-2">
        <Button small onClick={onMerge} aria-label={`Review merge for projects ${a} and ${b}`}>Review merge</Button>
        <Button small variant="ghost" busy={dismiss.isPending} onClick={() => dismiss.mutate()} aria-label={`Keep projects ${a} and ${b} separate`}>Keep separate</Button>
      </div>
    </li>
  );
}
