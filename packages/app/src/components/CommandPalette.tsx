import { projectWorkspace } from "../lib/projects.ts";
import { useMutation } from "@tanstack/react-query";
import { useMemo, useState, type KeyboardEvent, type ReactNode } from "react";
import { useNavigate } from "react-router";
import { useApi, usePlatform } from "../platform/context.tsx";
import { useSessionDestination } from "../lib/hostState.ts";
import { useInvalidateWork, useProjects, useTasks, useTrees } from "../api/queries.ts";
import { THEMES, useTheme } from "../lib/theme.ts";
import { paletteHits } from "../lib/palette.ts";
import { taskClosedLabel, treeMeta } from "../lib/trees.ts";
import { IconSearch } from "./icons.tsx";
import { subjectLinks, type LinkSubject } from "./StageLinks.tsx";
import { NodeChip } from "./trees/TreeNode.tsx";
import { Button, Chip, EmptyState, Highlight, KeyHints, ModalLayer, activeRowClass, cx, rovingIndex, useCopy } from "./ui.tsx";

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
  /** A task or tree's #id before the label; `matched` when the query named it. */
  idTag?: { id: number; matched: boolean };
  hint?: string;
  /** Why it matched (e.g. "branch", "you said"); stays visible when the hint truncates. */
  tag?: string | null;
  /** Second line: message snippet. */
  detail?: string | null;
  badge?: ReactNode;
  run: () => void;
  /** Ctrl+Enter target. */
  sessionUrl?: string | null;
  /** Alt+C copies a link to it. */
  link?: LinkSubject;
}

const PAGES: [string, string][] = [
  ["Dashboard", "/"],
  ["Tasks", "/tasks"],
  ["Schedules", "/schedules"],
  ["Projects", "/projects"],
  ["Snippets", "/snippets"],
  ["Settings", "/settings"],
  ["Connections", "/settings/connections"],
  ["Notifications", "/settings/notifications"],
];

const has = (text: string, q: string) => text.toLowerCase().includes(q);

/** The first case-insensitive occurrence of `q` in `text`, for `Highlight`. */
function matchOf(text: string, q: string): { start: number; end: number } | null {
  const i = q === "" ? -1 : text.toLowerCase().indexOf(q);
  return i < 0 ? null : { start: i, end: i + q.length };
}

export function CommandPalette({ onClose, onNewTask }: { onClose: () => void; onNewTask: () => void }) {
  const api = useApi();
  const { links } = usePlatform();
  const destination = useSessionDestination();
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
  const tasks = useTasks({ closed: "all" }, { live: false });
  const projects = useProjects();
  const trees = useTrees({ status: "all" }, { live: false });
  const items = useMemo<Item[]>(() => {
    const go = (to: string) => () => {
      onClose();
      void navigate(to);
    };
    const out: Item[] = [];
    if (q !== "") {
      for (const { item: t, byId } of paletteHits(tasks.data?.tasks ?? [], q, 6))
        out.push({
          key: `task-${t.id}`,
          group: "Tasks",
          label: t.title,
          idTag: { id: t.id, matched: byId },
          hint: t.projectTitle,
          badge: <ClosedBadge label={taskClosedLabel(t)}><NodeChip task={t} /></ClosedBadge>,
          run: go(`/tasks/${t.id}`),
          sessionUrl: destination(t),
          link: { kind: "task", id: t.id, title: t.title },
        });
      for (const { item: tr, byId } of paletteHits(trees.data?.trees ?? [], q, 4))
        out.push({
          key: `tree-${tr.id}`,
          group: "Trees",
          label: tr.title,
          idTag: { id: tr.id, matched: byId },
          hint: treeMeta(tr),
          badge: <ClosedBadge label={tr.status === "open" ? null : tr.status} />,
          run: go(`/trees/${tr.id}`),
          link: { kind: "tree", id: tr.id, title: tr.title },
        });
      for (const p of (projects.data?.projects ?? []).filter((proj) => has(proj.title, q)).slice(0, 4))
        out.push({ key: `project-${p.id}`, group: "Projects", label: p.title, hint: projectWorkspace(p), run: go(`/projects/${p.id}`) });
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

  function onKey(e: KeyboardEvent) {
    const step = e.key === "ArrowDown" || e.key === "ArrowUp" ? rovingIndex(e.key, active, items.length) : null;
    if (step !== null) {
      e.preventDefault();
      const next = items[step];
      if (next !== undefined) setActiveKey(next.key);
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
      if ((e.ctrlKey || e.metaKey) && typeof item.sessionUrl === "string") {
        if (/^https?:\/\//i.test(item.sessionUrl)) links.openExternal(item.sessionUrl);
        else links.openApp(item.sessionUrl);
        onClose();
      } else item.run();
    }
  }

  let lastGroup = "";
  return (
    <ModalLayer
      label="Command palette"
      onClose={onClose}
      className="inset-0 flex flex-col bg-surface pt-[var(--inset-top)] pr-[env(safe-area-inset-right)] pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)] md:inset-auto md:p-0 md:top-[72px] md:left-1/2 md:max-h-[70vh] md:w-[660px] md:-translate-x-1/2 md:border md:border-line-strong md:shadow-[0_16px_48px_rgba(0,0,0,0.28)]"
    >
      <div className="flex min-h-0 flex-1 flex-col" onKeyDown={onKey}>
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
            data-autofocus
            placeholder="Search tasks, trees, projects, #id"
            aria-label="Search"
            role="combobox"
            aria-expanded="true"
            aria-controls="palette-list"
            aria-activedescendant={items[active]?.key}
            className="min-w-0 flex-1 bg-transparent text-input outline-none"
          />
          <Button variant="ghost" onClick={onClose} className="-mr-2 md:hidden">
            Cancel
          </Button>
        </div>
        <div id="palette-list" role="listbox" className="min-h-0 flex-1 overflow-y-auto py-1">
          {items.length === 0 ? (
            <EmptyState className="px-4 py-6">{`Nothing matches "${query.trim()}".`}</EmptyState>
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
                      i === active && activeRowClass,
                    )}
                  >
                    <div className="flex items-center gap-2.5">
                      {item.badge}
                      {item.idTag === undefined ? null : (
                        <span className="-mr-1 shrink-0 font-mono text-meta whitespace-nowrap text-muted tabular">
                          <Highlight text={`#${item.idTag.id}`} range={item.idTag.matched ? { start: 0, end: `#${item.idTag.id}`.length } : null} />
                        </span>
                      )}
                      <span className="min-w-0 truncate text-fg/90">
                        <Highlight text={item.label} range={matchOf(item.label, q)} />
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
                        <Highlight text={item.detail} range={matchOf(item.detail, q)} />
                      </div>
                    )}
                  </div>
                </div>
              );
            })
          )}
        </div>
        <KeyHints
          hints={[
            ["↑↓", "move"],
            ["↵", "open"],
            ["Ctrl ↵", "open session"],
            ["Alt C", "copy link"],
            ["Esc", "close"],
          ]}
        >
          <span aria-live="polite">
            {copyStatus === "copied" ? <span className="text-fg">Copied {copiedName}</span> : copyStatus === "failed" ? <span className="text-bad">Copy failed</span> : null}
          </span>
        </KeyHints>
      </div>
    </ModalLayer>
  );
}
