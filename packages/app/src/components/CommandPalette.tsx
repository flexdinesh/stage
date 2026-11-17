import { useMutation } from "@tanstack/react-query";
import { useMemo, useState, type KeyboardEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useNavigate } from "react-router";
import { useApi, usePlatform } from "../platform/context.tsx";
import { useT3Destination } from "../lib/hostState.ts";
import { useInvalidateWork, useProjects, useTasks, useTrees } from "../api/queries.ts";
import { THEMES, useTheme } from "../lib/theme.ts";
import { taskClosedLabel, treeMeta } from "../lib/trees.ts";
import { IconSearch } from "./icons.tsx";
import { subjectLinks, useCopy, type LinkSubject } from "./StageLinks.tsx";
import { TaskStateBadge } from "./StateBadge.tsx";
import { Chip, Kbd, cx } from "./ui.tsx";

/** A closed item's plain chip (archived, done, tree done) beside its other badge. */
function ClosedBadge({ label, children }: { label: string | null; children?: ReactNode }) {
  if (label === null) return children ?? null;
  return (
    <span className="flex shrink-0 items-center gap-1.5">
      <Chip>{label}</Chip>
      {children}
    </span>
  );
}

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
  ["Trees", "/trees"],
  ["Projects", "/projects"],
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

export function CommandPalette({ onClose, onNewTask }: { onClose: () => void; onNewTask: () => void }) {
  const api = useApi();
  const { links } = usePlatform();
  const destination = useT3Destination();
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
  const tasks = useTasks({ closed: "all" });
  const projects = useProjects();
  const trees = useTrees({ status: "all" });
  const items = useMemo<Item[]>(() => {
    const go = (to: string) => () => {
      onClose();
      void navigate(to);
    };
    const out: Item[] = [];
    if (q !== "") {
      const taskHits = (tasks.data?.tasks ?? []).filter((t) => has(t.title, q)).slice(0, 6);
      for (const t of taskHits)
        out.push({
          key: `task-${t.id}`,
          group: "Tasks",
          label: t.title,
          hint: t.projectTitle,
          badge: <ClosedBadge label={taskClosedLabel(t)}><TaskStateBadge state={t.state} /></ClosedBadge>,
          run: go(`/tasks/${t.id}`),
          t3Url: destination(t),
          link: { kind: "task", id: t.id, title: t.title },
        });
      for (const tr of (trees.data?.trees ?? []).filter((t) => has(t.title, q)).slice(0, 4))
        out.push({
          key: `tree-${tr.id}`,
          group: "Trees",
          label: tr.title,
          hint: treeMeta(tr),
          badge: <ClosedBadge label={tr.status === "open" ? null : tr.status} />,
          run: go(`/trees/${tr.id}`),
        });
      for (const p of (projects.data?.projects ?? []).filter((proj) => has(proj.title, q)).slice(0, 4))
        out.push({ key: `project-${p.id}`, group: "Projects", label: p.title, hint: p.workspaceRoot, run: go(`/projects/${p.id}`) });
    }
    const actions: Item[] = [
      { key: "new", group: "Actions", label: "New task", hint: "N", run: () => {
          onClose();
          onNewTask();
        },
      },
      { key: "sync", group: "Actions", label: "Sync now", hint: "projects and task sessions", run: () => {
          sync.mutate();
          onClose();
        },
      },
      ...PAGES.map(([label, to]): Item => ({ key: `go-${to}`, group: "Go to", label, run: go(to) })),
      ...THEMES.map((t): Item => ({ key: `theme-${t}`, group: "Theme", label: `Theme: ${t}`, run: () => setTheme(t) })),
    ];
    out.push(...(q === "" ? actions : actions.filter((a) => has(a.label, q))));
    return out;
  }, [q, tasks.data, trees.data, projects.data, navigate, onClose, onNewTask, setTheme, sync, destination]);

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
      const { markdown, name } = subjectLinks(link, (path) => links.stageLink(path));
      setCopiedName(name);
      copy(markdown);
    } else if (e.key === "Enter") {
      e.preventDefault();
      const item = items[active];
      if (item === undefined) return;
      if ((e.ctrlKey || e.metaKey) && typeof item.t3Url === "string") {
        links.openApp(item.t3Url);
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
            placeholder="Search tasks, trees, projects"
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
              {`Nothing matches "${query.trim()}".`}
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
            {copyStatus === "copied" ? <span className="text-fg">Copied {copiedName}</span> : copyStatus === "failed" ? <span className="text-bad">Copy failed</span> : null}
          </span>
        </div>
      </div>
    </div>,
    document.body,
  );
}
