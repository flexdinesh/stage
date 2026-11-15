import { useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useState, type KeyboardEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useNavigate } from "react-router";
import { api } from "../api/client.ts";
import { keys, useInvalidateWork, useProjects, useTasks } from "../api/queries.ts";
import { matchLabel, mergePaletteSessions, t3SearchNote, type SessionHit } from "../lib/paletteSearch.ts";
import { sessionPath } from "../lib/sessions.ts";
import { THEMES, useTheme } from "../lib/theme.ts";
import { IconSearch } from "./icons.tsx";
import { subjectLinks, useCopy, type LinkSubject } from "./StageLinks.tsx";
import { TaskStateBadge, ThreadStatusBadge } from "./StateBadge.tsx";
import { Kbd, cx } from "./ui.tsx";

interface Item {
  key: string;
  group: string;
  label: string;
  hint?: string;
  /** Why it matched (e.g. "branch", "you said"); stays visible when the hint truncates. */
  tag?: string | null;
  /** Second line: message snippet. */
  detail?: string | null;
  badge?: ReactNode;
  run: () => void;
  /** Ctrl+Enter target. */
  t3Url?: string | null;
  /** Alt+C copies a link to it. */
  link?: LinkSubject;
}

const PAGES: [string, string][] = [
  ["Dashboard", "/"],
  ["Projects", "/projects"],
  ["Sessions", "/sessions"],
  ["History", "/history"],
  ["Snippets", "/snippets"],
  ["Settings", "/settings"],
];

const has = (text: string, q: string) => text.toLowerCase().includes(q);

/** Bold the first case-insensitive occurrence of `q` in `text`. */
function Highlight({ text, q }: { text: string; q: string }) {
  const i = q === "" ? -1 : text.toLowerCase().indexOf(q);
  if (i < 0) return <>{text}</>;
  return (
    <>
      {text.slice(0, i)}
      <b className="font-semibold text-fg">{text.slice(i, i + q.length)}</b>
      {text.slice(i + q.length)}
    </>
  );
}

function useDebounced(value: string, ms: number): string {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

export function CommandPalette({ onClose, onNewTask }: { onClose: () => void; onNewTask: () => void }) {
  const [query, setQuery] = useState("");
  /** Highlighted item by key, so late results never move the highlight; null = first. */
  const [activeKey, setActiveKey] = useState<string | null>(null);
  const navigate = useNavigate();
  const { setTheme } = useTheme();
  const invalidate = useInvalidateWork();
  const sync = useMutation({ mutationFn: api.sync, onSettled: invalidate });
  const [copyStatus, copy] = useCopy();
  const [copiedName, setCopiedName] = useState("");

  const q = query.trim().toLowerCase();
  const debounced = useDebounced(q, 200);
  const tasks = useTasks();
  const projects = useProjects();
  const threads = useQuery({
    queryKey: [...keys.threads, "palette", debounced],
    queryFn: () => api.threads({ q: debounced, limit: 8, subagents: "exclude" }),
    enabled: debounced.length >= 2,
    refetchInterval: false,
  });
  // Message text via T3 (merged server side with title matches).
  const searchQ = useDebounced(q, 250);
  const search = useQuery({
    queryKey: [...keys.threads, "palette-search", searchQ],
    queryFn: () => api.searchThreads(searchQ, { limit: 20 }),
    enabled: searchQ.length >= 2,
    refetchInterval: false,
    retry: false,
  });
  const searchCurrent = searchQ === q && q.length >= 2;
  const note = searchCurrent ? t3SearchNote(search.isError ? "error" : search.data?.t3) : null;

  const items = useMemo<Item[]>(() => {
    const go = (to: string) => () => {
      onClose();
      void navigate(to);
    };
    const out: Item[] = [];
    if (q !== "") {
      const taskHits = (tasks.data?.tasks ?? []).filter((t) => has(t.title, q)).slice(0, 6);
      const taskThreadIds = new Set(taskHits.map((t) => t.t3ThreadId));
      for (const t of taskHits)
        out.push({
          key: `task-${t.id}`,
          group: "Tasks",
          label: t.title,
          hint: t.projectTitle,
          badge: <TaskStateBadge state={t.state} />,
          run: go(t.t3ThreadId === null ? `/tasks/${t.id}` : sessionPath(t.t3ThreadId)),
          t3Url: t.t3DeepLink,
          link: { kind: "task", id: t.id, title: t.title },
        });
      for (const p of (projects.data?.projects ?? []).filter((proj) => has(proj.title, q)).slice(0, 4))
        out.push({ key: `project-${p.id}`, group: "Projects", label: p.title, hint: p.workspaceRoot, run: go(`/projects/${p.id}`) });
      const { sessions, messages } = mergePaletteSessions(
        debounced === q ? (threads.data?.threads ?? []) : [],
        searchCurrent ? (search.data?.results ?? []) : [],
        taskThreadIds,
      );
      const sessionItem = (hit: SessionHit, group: string): Item => {
        const th = hit.thread;
        return {
          key: `${group === "Sessions" ? "thread" : "message"}-${hit.id}`,
          group,
          label: hit.title,
          hint: [hit.projectTitle ?? th?.t3ProjectId, th?.source].filter(Boolean).join(" · "),
          tag: matchLabel(hit.source),
          detail: hit.snippet,
          badge: th === null ? undefined : <ThreadStatusBadge status={th.status} settled={th.settled} />,
          run: go(sessionPath(hit.id)),
          t3Url: hit.t3DeepLink,
          link: { kind: "session", id: hit.id, title: hit.title },
        };
      };
      for (const hit of sessions) out.push(sessionItem(hit, "Sessions"));
      for (const hit of messages) out.push(sessionItem(hit, "In messages"));
    }
    const actions: Item[] = [
      { key: "new", group: "Actions", label: "New task", hint: "N", run: () => {
          onClose();
          onNewTask();
        },
      },
      { key: "sync", group: "Actions", label: "Sync now", hint: "full T3 sync", run: () => {
          sync.mutate();
          onClose();
        },
      },
      ...PAGES.map(([label, to]): Item => ({ key: `go-${to}`, group: "Go to", label, run: go(to) })),
      ...THEMES.map((t): Item => ({ key: `theme-${t}`, group: "Theme", label: `Theme: ${t}`, run: () => setTheme(t) })),
    ];
    out.push(...(q === "" ? actions : actions.filter((a) => has(a.label, q))));
    return out;
  }, [q, debounced, searchCurrent, tasks.data, projects.data, threads.data, search.data, navigate, onClose, onNewTask, setTheme, sync]);

  const found = items.findIndex((item) => item.key === activeKey);
  const active = found < 0 ? 0 : found;
  const move = (delta: number) => {
    const next = items[Math.min(Math.max(active + delta, 0), items.length - 1)];
    if (next !== undefined) setActiveKey(next.key);
  };


  function onKey(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      onClose();
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      move(1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      move(-1);
    } else if (e.altKey && e.code === "KeyC") {
      // e.code: Option+C on macOS types "ç" in e.key.
      e.preventDefault();
      const link = items[active]?.link;
      if (link === undefined) return;
      const { markdown, name } = subjectLinks(link);
      setCopiedName(name);
      copy(markdown);
    } else if (e.key === "Enter") {
      e.preventDefault();
      const item = items[active];
      if (item === undefined) return;
      if ((e.ctrlKey || e.metaKey) && typeof item.t3Url === "string") {
        window.location.assign(item.t3Url);
        onClose();
      } else item.run();
    }
  }

  let lastGroup = "";
  return createPortal(
    <div className="fixed inset-0 z-50" onKeyDown={onKey}>
      <div className="absolute inset-0 bg-scrim" onClick={onClose} />
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        className="absolute inset-0 flex flex-col bg-surface pt-[env(safe-area-inset-top)] pr-[env(safe-area-inset-right)] pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)] md:inset-auto md:p-0 md:top-[72px] md:left-1/2 md:max-h-[70vh] md:w-[660px] md:-translate-x-1/2 md:border md:border-line-strong md:shadow-[0_16px_48px_rgba(0,0,0,0.28)]"
      >
        <div className="flex h-12 shrink-0 items-center gap-3 border-b border-line px-4">
          <span className="text-muted">
            <IconSearch size={14} />
          </span>
          <input
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setActiveKey(null);
            }}
            autoFocus
            placeholder="Search tasks, projects, sessions, messages"
            aria-label="Search"
            role="combobox"
            aria-expanded="true"
            aria-controls="palette-list"
            aria-activedescendant={items[active]?.key}
            className="min-w-0 flex-1 bg-transparent text-input outline-none"
          />
          <button type="button" onClick={onClose} className="-mr-2 h-10 px-2 text-small text-muted md:hidden">
            Cancel
          </button>
        </div>
        <div id="palette-list" role="listbox" className="min-h-0 flex-1 overflow-y-auto py-1">
          {items.length === 0 ? (
            <p className="px-4 py-6 text-small text-muted">
              {threads.isFetching || search.isFetching ? "Searching sessions…" : `Nothing matches "${query.trim()}".`}
            </p>
          ) : (
            items.map((item, i) => {
              const header = item.group !== lastGroup ? item.group : null;
              lastGroup = item.group;
              const hint = item.hint === undefined || item.hint === "" ? null : item.hint;
              const tag = item.tag ?? null;
              return (
                <div key={item.key}>
                  {header === null ? null : <div className="label-caps px-4 pt-2.5 pb-1">{header}</div>}
                  <div
                    id={item.key}
                    role="option"
                    aria-selected={i === active}
                    data-active={i === active}
                    ref={i === active ? (el) => el?.scrollIntoView({ block: "nearest" }) : undefined}
                    onMouseMove={() => setActiveKey(item.key)}
                    onClick={() => item.run()}
                    className={cx(
                      "cursor-pointer px-4 py-2 text-ui",
                      i === active && "bg-accent/7 shadow-[inset_2px_0_var(--accent)]",
                    )}
                  >
                    <div className="flex items-center gap-2.5">
                      {item.badge}
                      <span className="min-w-0 truncate text-fg/90">
                        <Highlight text={item.label} q={q} />
                      </span>
                      {hint === null && tag === null ? null : (
                        <span className="ml-auto flex max-w-[40%] min-w-0 shrink-0 pl-3 font-mono text-meta whitespace-nowrap text-muted md:max-w-none">
                          {hint === null ? null : <span className="truncate">{hint}</span>}
                          {tag === null ? null : <span className="shrink-0">{hint === null ? tag : `\u00a0· ${tag}`}</span>}
                        </span>
                      )}
                    </div>
                    {typeof item.detail !== "string" ? null : (
                      <div className="mt-0.5 truncate font-mono text-meta text-muted">
                        <Highlight text={item.detail} q={q} />
                      </div>
                    )}
                  </div>
                </div>
              );
            })
          )}
          {note === null ? null : <p className="px-4 pt-2 pb-3 font-mono text-meta-sm text-muted md:hidden">{note}</p>}
        </div>
        <div className="hidden shrink-0 gap-4 border-t border-line bg-panel px-4 py-2 font-mono text-meta-sm whitespace-nowrap text-muted md:flex">
          <span>
            <Kbd>↑↓</Kbd> move
          </span>
          <span>
            <Kbd>↵</Kbd> open
          </span>
          <span>
            <Kbd>Ctrl ↵</Kbd> open in T3
          </span>
          <span>
            <Kbd>Alt C</Kbd> copy link
          </span>
          <span>
            <Kbd>Esc</Kbd> close
          </span>
          <span className="ml-auto min-w-0 truncate" aria-live="polite">
            {copyStatus === "copied" ? <span className="text-fg">Copied {copiedName}</span> : copyStatus === "failed" ? <span className="text-bad">Copy failed</span> : note}
          </span>
        </div>
      </div>
    </div>,
    document.body,
  );
}
