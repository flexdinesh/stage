import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { useSearchParams } from "react-router";
import { useProjects } from "../api/queries.ts";
import type { Project } from "../api/types.ts";
import { parsePinned, parseScope, togglePin } from "../lib/scope.ts";
import { usePlatform } from "../platform/context.tsx";

/** Per-device project scope (sidebar selection) and pinned projects, in preference storage. */
const SCOPE_KEY = "stage-scope";
const PINNED_KEY = "stage-pinned-projects";
/** Older links (`/?project=3`, `/schedules?project=3`) set the scope. */
const PROJECT_PARAM = "project";

export interface Scope {
  /** The scoped project id; null for All projects. */
  projectId: number | null;
  /** The scoped project once projects load. */
  project: Project | null;
  setProjectId: (id: number | null) => void;
  pinned: readonly number[];
  togglePinned: (id: number) => void;
  /** API filter for the scope (`{ project }`, or none). */
  query: { project?: number };
}

const ScopeContext = createContext<Scope>({
  projectId: null,
  project: null,
  setProjectId: () => {},
  pinned: [],
  togglePinned: () => {},
  query: {},
});

export function ScopeProvider({ children }: { children: ReactNode }) {
  const { storage } = usePlatform();
  const [stored, setStored] = useState(() => parseScope(storage.get(SCOPE_KEY)));
  const [pinned, setPinned] = useState(() => parsePinned(storage.get(PINNED_KEY)));
  const [params, setParams] = useSearchParams();
  const projects = useProjects();

  const setProjectId = useCallback(
    (id: number | null) => {
      setStored(id);
      storage.set(SCOPE_KEY, id === null ? null : String(id));
    },
    [storage],
  );

  // A `?project=` link sets the scope once; the effect then drops the param so Back or a reload does not reapply it.
  const linked = params.get(PROJECT_PARAM);
  const [linkSeen, setLinkSeen] = useState<string | null>(null);
  if (linked !== linkSeen) {
    setLinkSeen(linked);
    if (linked !== null) setStored(parseScope(linked));
  }
  useEffect(() => {
    if (linked === null) return;
    const id = parseScope(linked);
    storage.set(SCOPE_KEY, id === null ? null : String(id));
    setParams(
      (current) => {
        const next = new URLSearchParams(current);
        next.delete(PROJECT_PARAM);
        return next;
      },
      { replace: true },
    );
  }, [linked, setParams, storage]);

  const togglePinned = useCallback(
    (id: number) =>
      setPinned((current) => {
        const next = togglePin(current, id);
        storage.set(PINNED_KEY, JSON.stringify(next));
        return next;
      }),
    [storage],
  );

  // A scope whose project is gone (deleted, archived) reads as All projects once the list loads.
  const list = projects.data?.projects;
  const project = stored === null ? null : (list?.find((p) => p.id === stored) ?? null);
  const projectId = list === undefined || project !== null ? stored : null;

  const value = useMemo<Scope>(
    () => ({
      projectId,
      project,
      setProjectId,
      pinned,
      togglePinned,
      query: projectId === null ? {} : { project: projectId },
    }),
    [projectId, project, setProjectId, pinned, togglePinned],
  );
  return <ScopeContext value={value}>{children}</ScopeContext>;
}

export const useScope = () => useContext(ScopeContext);
