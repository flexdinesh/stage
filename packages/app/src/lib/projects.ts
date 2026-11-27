import type { Harness, ImportSummary, Project, ProjectBinding, Workspace } from "../api/types.ts";
import { relativeTime } from "./format.ts";

/**
 * The Projects page's quiet import note: "12 imported from OpenCode 2h ago", or per harness
 * ("12 imported from OpenCode, 3 from T3 2h ago"). Null when nothing was imported recently.
 * `imports` comes newest first; the time is the latest import. `kindName` names a harness kind (useHarnesses).
 */
export function importNotice(imports: readonly ImportSummary[], kindName: (kind: Harness) => string, now: number = Date.now()): string | null {
  const [latest] = imports;
  if (latest === undefined) return null;
  const sources = imports.map((m, i) => `${i === 0 ? `${m.count} imported from` : `${m.count} from`} ${kindName(m.harness)}`);
  return `${sources.join(", ")} ${relativeTime(latest.at, now)}`;
}

/** Half-open [start, end) span of matched text. */
export interface MatchRange {
  start: number;
  end: number;
}

export interface ProjectMatch {
  project: Project;
  /** Matched span in `project.title`; null when the title did not match (or the query is empty). */
  title: MatchRange | null;
  /** Matched span in workspace or repository metadata; null when neither matched. */
  path: MatchRange | null;
  /** True when only the repo metadata matched. */
  repo: boolean;
}

function find(text: string, q: string): MatchRange | null {
  const start = text.toLowerCase().indexOf(q);
  return start < 0 ? null : { start, end: start + q.length };
}

/** Title prefix, then title substring, then path, then repo only. */
function rank(m: ProjectMatch): number {
  if (m.title === null) return m.repo ? 3 : 2;
  return m.title.start === 0 ? 0 : 1;
}

const DETAIL_SEPARATOR = " · ";

/** A repo for display: GitHub's host is implied, any other host stays. */
export function repoLabel(repo: string): string {
  return repo.startsWith("github.com/") ? repo.slice("github.com/".length) : repo;
}

/** Primary path, then repo labels for project search. */
export function projectDetail(project: Project): string {
  const repos = project.repos.map(repoLabel).join(", ");
  const path = projectWorkspace(project);
  return repos === "" ? path : path + DETAIL_SEPARATOR + repos;
}

function matchDetail(project: Project, q: string): { range: MatchRange | null; repo: boolean } {
  const path = projectWorkspace(project);
  const bindings = currentBindings(project.bindings);
  const inPath = bindings.length === 0 ? null : find(path, q);
  if (inPath !== null) return { range: inPath, repo: false };
  for (const binding of bindings) {
    const match = find(workspaceLocation(binding), q);
    if (match !== null) return { range: match, repo: false };
    const external = find(binding.externalProjectId, q);
    if (external !== null) return { range: external, repo: false };
  }
  const repos = project.repos.map(repoLabel).join(", ");
  const inRepo = find(repos, q);
  if (inRepo === null) return { range: null, repo: false };
  const offset = path.length + DETAIL_SEPARATOR.length;
  return { range: { start: inRepo.start + offset, end: inRepo.end + offset }, repo: true };
}

/** Most recent activity first; projects with no activity last; then title. */
function byRecency(a: Project, b: Project): number {
  if (a.lastActivityAt !== b.lastActivityAt) {
    if (a.lastActivityAt === null) return 1;
    if (b.lastActivityAt === null) return -1;
    const diff = Date.parse(b.lastActivityAt) - Date.parse(a.lastActivityAt);
    if (diff !== 0) return diff;
  }
  return a.title.localeCompare(b.title);
}

/**
 * Projects matching `query` (case-insensitive substring of title, workspace path or repo label, surrounding space ignored).
 * Ranked title prefix, title substring, path, repo only; ties and the empty query order by recent activity.
 */
export function matchProjects(projects: readonly Project[], query: string): ProjectMatch[] {
  const q = query.trim().toLowerCase();
  if (q === "") return projects.toSorted(byRecency).map((project) => ({ project, title: null, path: null, repo: false }));
  return projects
    .map((project) => {
      const detail = matchDetail(project, q);
      return { project, title: find(project.title, q), path: detail.range, repo: detail.repo };
    })
    .filter((m) => m.title !== null || m.path !== null)
    .toSorted((a, b) => rank(a) - rank(b) || byRecency(a.project, b.project));
}

export function currentBindings(bindings: readonly ProjectBinding[]): ProjectBinding[] {
  return bindings.filter((b) => b.connectionActive);
}

export function projectWorkspace(project: Project): string {
  const binding = primaryWorkspace(orderedWorkspaces(project.bindings));
  return binding === undefined ? "Workspace not bound" : workspaceLocation(binding);
}

export const workspaceLocation = (binding: Pick<ProjectBinding, "workspace" | "title" | "externalProjectId">): string =>
  binding.workspace || binding.title || binding.externalProjectId;

export function orderedWorkspaces(bindings: readonly ProjectBinding[]): ProjectBinding[] {
  return bindings.toSorted((a, b) =>
    a.machineId - b.machineId || a.connectionId - b.connectionId ||
    Number(b.primary) - Number(a.primary) ||
    workspaceLocation(a).localeCompare(workspaceLocation(b)) || a.id - b.id,
  );
}

export function primaryWorkspace(ordered: readonly ProjectBinding[], connectionId = 0): ProjectBinding | undefined {
  const scoped = connectionId === 0 ? ordered : ordered.filter((b) => b.connectionId === connectionId);
  return scoped.find((b) => b.connectionActive && b.primary) ??
    scoped.find((b) => b.connectionActive);
}

export function suggestedWorkspaceFor(row: Workspace, projectId: number): boolean {
  return row.projectId === null && !row.declined && row.resolution.candidates.includes(projectId);
}

export function filterWorkspaceInventory(
  rows: readonly Workspace[],
  { projectId, previousId, repository, query }: { projectId: number; previousId: string | undefined; repository: string; query: string },
): Workspace[] {
  const text = query.trim().toLowerCase();
  const inventoryRank = (row: Workspace): number => {
    if (row.externalId === previousId) return 0;
    if (suggestedWorkspaceFor(row, projectId)) return 1;
    return row.resolution.outcome === "join" ? 2 : 3;
  };
  return rows.filter((row) =>
    (repository === "" || row.remote === repository) &&
    `${row.title} ${row.externalId} ${row.workspace} ${row.remote}`.toLowerCase().includes(text),
  ).toSorted((a, b) => inventoryRank(a) - inventoryRank(b) ||
    (a.workspace || a.externalId).localeCompare(b.workspace || b.externalId) || a.externalId.localeCompare(b.externalId));
}

/** The explicit choice or the primary on this connection, including when unavailable. */
export function workspaceBinding(bindings: readonly ProjectBinding[], connectionId: number, bindingId: number | null = null): ProjectBinding | undefined {
  return bindings.find((b) =>
    b.connectionActive && b.connectionId === connectionId && (bindingId === null ? b.primary : b.id === bindingId),
  );
}
