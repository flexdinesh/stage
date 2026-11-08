import { useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useState, type KeyboardEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useNavigate } from "react-router";
import { api } from "../api/client.ts";
import { keys, useChains, useInvalidateWork, useProjects, useTasks } from "../api/queries.ts";
import { THEMES, useTheme } from "../lib/theme.ts";
import { IconSearch } from "./icons.tsx";
import { ChainStateBadge, TaskStateBadge, ThreadStatusBadge } from "./StateBadge.tsx";
import { Kbd, cx } from "./ui.tsx";

interface Item {
  key: string;
  group: string;
  label: string;
  hint?: string;
  badge?: ReactNode;
  run: () => void;
  /** Ctrl+Enter target. */
  t3Url?: string | null;
}

const PAGES: [string, string][] = [
  ["Dashboard", "/"],
  ["Projects", "/projects"],
  ["Chains", "/chains"],
  ["History", "/history"],
  ["Templates", "/templates"],
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
  const [active, setActive] = useState(0);
  const navigate = useNavigate();
  const { setTheme } = useTheme();
  const invalidate = useInvalidateWork();
  const sync = useMutation({ mutationFn: api.sync, onSettled: invalidate });

  const q = query.trim().toLowerCase();
  const debounced = useDebounced(q, 200);
  const tasks = useTasks();
  const projects = useProjects();
  const chains = useChains();
  const threads = useQuery({
    queryKey: [...keys.threads, "palette", debounced],
    queryFn: () => api.threads({ q: debounced, limit: 8, subagents: "exclude" }),
    enabled: debounced.length >= 2,
    refetchInterval: false,
  });

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
          run: go(`/tasks/${t.id}`),
          t3Url: t.t3WebUrl,
        });
      for (const p of (projects.data?.projects ?? []).filter((proj) => has(proj.title, q)).slice(0, 4))
        out.push({ key: `project-${p.id}`, group: "Projects", label: p.title, hint: p.workspaceRoot, run: go(`/projects/${p.id}`) });
      for (const c of (chains.data?.chains ?? []).filter((ch) => has(ch.title, q)).slice(0, 4))
        out.push({
          key: `chain-${c.id}`,
          group: "Chains",
          label: c.title,
          hint: `${c.steps.length} steps`,
          badge: <ChainStateBadge state={c.state} />,
          run: go(`/chains/${c.id}`),
        });
      if (debounced === q)
        for (const th of (threads.data?.threads ?? []).filter((hit) => !taskThreadIds.has(hit.t3ThreadId)))
          out.push({
            key: `thread-${th.t3ThreadId}`,
            group: "Threads",
            label: th.title,
            hint: `${th.projectTitle ?? th.t3ProjectId} · ${th.source}`,
            badge: <ThreadStatusBadge status={th.status} />,
            run: th.taskId === null ? go(`/history?q=${encodeURIComponent(th.title)}`) : go(`/tasks/${th.taskId}`),
            t3Url: th.t3WebUrl,
          });
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
  }, [q, debounced, tasks.data, projects.data, chains.data, threads.data, navigate, onClose, onNewTask, setTheme, sync]);


  function onKey(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      onClose();
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((a) => Math.min(a + 1, items.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => Math.max(a - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      const item = items[active];
      if (item === undefined) return;
      if ((e.ctrlKey || e.metaKey) && typeof item.t3Url === "string") {
        window.open(item.t3Url, "_blank", "noreferrer");
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
        className="absolute inset-0 flex flex-col bg-surface md:inset-auto md:top-[72px] md:left-1/2 md:max-h-[70vh] md:w-[660px] md:-translate-x-1/2 md:border md:border-line-strong md:shadow-[0_16px_48px_rgba(0,0,0,0.28)]"
      >
        <div className="flex h-12 shrink-0 items-center gap-3 border-b border-line px-4">
          <span className="text-muted">
            <IconSearch size={14} />
          </span>
          <input
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setActive(0);
            }}
            autoFocus
            placeholder="Search tasks, projects, chains, threads"
            aria-label="Search"
            role="combobox"
            aria-expanded="true"
            aria-controls="palette-list"
            aria-activedescendant={items[active]?.key}
            className="min-w-0 flex-1 bg-transparent text-[14px] outline-none"
          />
          <button type="button" onClick={onClose} className="text-[12px] text-muted md:hidden">
            Cancel
          </button>
        </div>
        <div id="palette-list" role="listbox" className="min-h-0 flex-1 overflow-y-auto py-1">
          {items.length === 0 ? (
            <p className="px-4 py-6 text-[12.5px] text-muted">
              {threads.isFetching ? "Searching threads…" : `Nothing matches "${query.trim()}".`}
            </p>
          ) : (
            items.map((item, i) => {
              const header = item.group !== lastGroup ? item.group : null;
              lastGroup = item.group;
              return (
                <div key={item.key}>
                  {header === null ? null : <div className="label-caps px-4 pt-2.5 pb-1">{header}</div>}
                  <div
                    id={item.key}
                    role="option"
                    aria-selected={i === active}
                    data-active={i === active}
                    ref={i === active ? (el) => el?.scrollIntoView({ block: "nearest" }) : undefined}
                    onMouseMove={() => setActive(i)}
                    onClick={() => item.run()}
                    className={cx(
                      "flex cursor-pointer items-center gap-2.5 px-4 py-2 text-[13px]",
                      i === active && "bg-accent/7 shadow-[inset_2px_0_var(--accent)]",
                    )}
                  >
                    {item.badge}
                    <span className="min-w-0 truncate text-fg/90">
                      <Highlight text={item.label} q={q} />
                    </span>
                    {item.hint === undefined ? null : (
                      <span className="ml-auto shrink-0 truncate pl-3 font-mono text-[11.5px] text-muted">{item.hint}</span>
                    )}
                  </div>
                </div>
              );
            })
          )}
        </div>
        <div className="hidden shrink-0 gap-4 border-t border-line bg-panel px-4 py-2 font-mono text-[11px] text-muted md:flex">
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
            <Kbd>Esc</Kbd> close
          </span>
        </div>
      </div>
    </div>,
    document.body,
  );
}
