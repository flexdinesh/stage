import type { Project } from "../api/types.ts";

/** Half-open [start, end) span of matched text. */
export interface MatchRange {
  start: number;
  end: number;
}

export interface ProjectMatch {
  project: Project;
  /** Matched span in `project.title`; null when the title did not match (or the query is empty). */
  title: MatchRange | null;
  /** Matched span in `project.workspaceRoot`; null when the path did not match (or the query is empty). */
  path: MatchRange | null;
}

function find(text: string, q: string): MatchRange | null {
  const start = text.toLowerCase().indexOf(q);
  return start < 0 ? null : { start, end: start + q.length };
}

/** Title prefix, then title substring, then path only. */
function rank(m: ProjectMatch): number {
  if (m.title === null) return 2;
  return m.title.start === 0 ? 0 : 1;
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
 * Projects matching `query` (case-insensitive substring of title or workspace path, surrounding space ignored).
 * Ranked title prefix, title substring, path only; ties and the empty query order by recent activity.
 */
export function matchProjects(projects: readonly Project[], query: string): ProjectMatch[] {
  const q = query.trim().toLowerCase();
  if (q === "") return projects.toSorted(byRecency).map((project) => ({ project, title: null, path: null }));
  return projects
    .map((project) => ({ project, title: find(project.title, q), path: find(projectWorkspace(project), q) }))
    .filter((m) => m.title !== null || m.path !== null)
    .toSorted((a, b) => rank(a) - rank(b) || byRecency(a.project, b.project));
}

export function projectWorkspace(project: Project): string {
 return project.bindings.map((binding) => binding.workspace).join(" · ") || "Workspace not bound";
}
