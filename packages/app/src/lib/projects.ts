import type { Harness, ImportSummary, Project, ProjectBinding, ProjectGroup } from "../api/types.ts";
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

/** A repo for display: GitHub's host is implied, any other host stays. */
export function repoLabel(repo: string): string {
  return repo.startsWith("github.com/") ? repo.slice("github.com/".length) : repo;
}

function matchDetail(project: Project, group: ProjectGroup | undefined, q: string): { range: MatchRange | null; repo: boolean } {
  for (const workspace of group?.members ?? []) {
    if (workspace.catalogState === "retired") continue;
    const match = find(workspace.path, q) ?? find(workspace.title, q);
    if (match !== null) return { range: match, repo: false };
  }
  const repo = find(project.repos.map(repoLabel).join(", "), q);
  return { range: repo, repo: repo !== null };
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
export function matchProjects(projects: readonly Project[], groups: ReadonlyMap<number, ProjectGroup>, query: string): ProjectMatch[] {
  const q = query.trim().toLowerCase();
  if (q === "") return projects.toSorted(byRecency).map((project) => ({ project, title: null, path: null, repo: false }));
  return projects
    .map((project) => {
      const detail = matchDetail(project, groups.get(project.id), q);
      return { project, title: find(project.title, q), path: detail.range, repo: detail.repo };
    })
    .filter((m) => m.title !== null || m.path !== null)
    .toSorted((a, b) => rank(a) - rank(b) || byRecency(a.project, b.project));
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
