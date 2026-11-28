import { useQuery } from "@tanstack/react-query";
import { useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";
import { useApi } from "../platform/context.tsx";
import { useSessionDestination } from "../lib/hostState.ts";
import { taskSessionPath } from "../lib/taskSession.ts";
import { keys } from "../api/queries.ts";
import type { TreeStatus } from "../api/types.ts";
import {
  journalEntryMarkdown,
  journalEntryUrl,
  parseStageRef,
  projectMarkdown,
  projectUrl,
  scheduleMarkdown,
  scheduleUrl,
  splitStageRefs,
  taskMarkdown,
  taskUrl,
  treeMarkdown,
  treeUrl,
  type StageLink,
  type StageRef,
} from "../lib/refs.ts";
import { usePlatform } from "../platform/context.tsx";
import { IconChainLink, IconCheck } from "./icons.tsx";
import { taskStateLabel, taskStateTone } from "./StateBadge.tsx";
import { Button, ExternalLink, IconButton, Marker, Meta, copyStatusText, cx, linkClass, useCopy, type ChipTone } from "./ui.tsx";

/** What a copied link points at: a task, tree, schedule, project or journal entry. */
export type LinkSubject =
  | { kind: "task" | "tree" | "schedule" | "project"; id: number; title: string }
  | { kind: "journal"; owner: "task" | "tree"; ownerId: number; entryId: number };

export function subjectLinks(s: LinkSubject, link: StageLink): { markdown: string; url: string; name: string } {
  switch (s.kind) {
    case "journal":
      return {
        markdown: journalEntryMarkdown(link, s.owner, s.ownerId, s.entryId),
        url: journalEntryUrl(link, s.owner, s.ownerId, s.entryId),
        name: `note #${s.entryId}`,
      };
    case "tree":
      return { markdown: treeMarkdown(link, s.id, s.title), url: treeUrl(link, s.id), name: `tree #${s.id}` };
    case "schedule":
      return { markdown: scheduleMarkdown(link, s.id, s.title), url: scheduleUrl(link, s.id), name: `schedule #${s.id}` };
    case "project":
      return { markdown: projectMarkdown(link, s.id, s.title), url: projectUrl(link, s.id), name: `project #${s.id}` };
    case "task":
      return { markdown: taskMarkdown(link, s.id, s.title), url: taskUrl(link, s.id), name: `task #${s.id}` };
  }
}

/** Detail-page header: "Copy link" (self-describing markdown, for prompts) joined to "URL" (bare). */
export function CopyLinkPair({ subject, small = false }: { subject: LinkSubject; small?: boolean }) {
  const { links } = usePlatform();
  const { markdown, url, name } = subjectLinks(subject, (path) => links.stageLink(path));
  const [mdStatus, copyMd] = useCopy();
  const [urlStatus, copyUrl] = useCopy();
  return (
    <span role="group" aria-label={`Copy link to ${name}`} className="inline-flex">
      <Button small={small} title={markdown} onClick={() => copyMd(markdown)}>
        <IconChainLink />
        {copyStatusText(mdStatus, "Copy link")}
      </Button>
      <Button small={small} title={url} aria-label={urlStatus === "idle" ? "Copy URL" : undefined} className="-ml-px" onClick={() => copyUrl(url)}>
        {copyStatusText(urlStatus, "URL")}
      </Button>
      <span className="sr-only" aria-live="polite">
        {mdStatus === "copied" || urlStatus === "copied" ? "Copied" : mdStatus === "failed" || urlStatus === "failed" ? "Copy failed" : ""}
      </span>
    </span>
  );
}

/** Laptop row action: a ghost link icon shown on row hover or keyboard focus (the row carries `group/row`). */
export function RowCopyLink({ subject, className }: { subject: LinkSubject; className?: string }) {
  const { links } = usePlatform();
  const { markdown, name } = subjectLinks(subject, (path) => links.stageLink(path));
  const [status, copy] = useCopy();
  const label = copyStatusText(status, `Copy link to ${name}`);
  return (
    <>
      <IconButton
        small
        label={label}
        icon={status === "copied" ? IconCheck : IconChainLink}
        onClick={() => copy(markdown)}
        data-failed={status === "failed" || undefined}
        className={cx(
          "focus-visible:opacity-100 data-failed:text-bad max-md:hidden",
          status === "idle" ? "opacity-0 group-focus-within/row:opacity-100 group-hover/row:opacity-100" : "opacity-100",
          className,
        )}
      />
      <span className="sr-only" aria-live="polite">
        {status === "idle" ? "" : label}
      </span>
    </>
  );
}

// ---- Chips ----

/** A chip's view: `tag` is the mono #id (or "session"), `name` heads the tooltip. */
type Resolved = { name: string; tag: string; title: string; state: string; tone: ChipTone; href: string | null };

const TREE_TONES: Record<TreeStatus, ChipTone> = { open: "plain", done: "done", archived: "idle" };

function useResolved(ref: StageRef): { data: Resolved | null; failed: boolean } {
  const api = useApi();
  const destination = useSessionDestination();
  const taskId = ref.kind === "task" ? ref.id : 0;
  const treeId = ref.kind === "tree" ? ref.id : 0;
  const scheduleId = ref.kind === "schedule" ? ref.id : 0;
  const projectId = ref.kind === "project" ? ref.id : 0;
  // Same keys as the page hooks, so a chip and its page share one cache entry.
  const task = useQuery({
    queryKey: [...keys.task, taskId],
    queryFn: () => api.task(taskId),
    enabled: taskId !== 0,
    refetchInterval: 30_000,
    retry: false,
  });
  const tree = useQuery({ queryKey: [...keys.tree, treeId], queryFn: () => api.tree(treeId), enabled: treeId !== 0, refetchInterval: 30_000, retry: false });
  const schedule = useQuery({
    queryKey: [...keys.schedule, scheduleId],
    queryFn: () => api.schedule(scheduleId),
    enabled: scheduleId !== 0,
    refetchInterval: 30_000,
    retry: false,
  });
  const project = useQuery({
    queryKey: [...keys.projects, "group", projectId],
    queryFn: ({ signal }) => api.projectGroup(projectId, signal),
    enabled: projectId !== 0,
    refetchInterval: 30_000,
    retry: false,
  });
  // A session ref without a connection names no session to resolve: it stays plain text.
  const sessionConnection = ref.kind === "session" ? (ref.connectionId ?? null) : null;
  const sessionPath = ref.kind === "session" && sessionConnection !== null ? `/connections/${sessionConnection}/sessions/${encodeURIComponent(ref.id)}` : "";
  const session = useQuery({
    queryKey: ["session-reference",sessionPath],
    queryFn: () => api.resolveRef(sessionPath,undefined,true),
    enabled: sessionPath !== "",
    refetchInterval: 30_000,
    retry: false,
  });
  switch (ref.kind) {
    case "task": {
      const d = task.data;
      return {
        data: d === undefined ? null : { name: `Task #${d.id}`, tag: `#${d.id}`, title: d.title, state: taskStateLabel(d.state), tone: taskStateTone(d.state), href: `/tasks/${d.id}` },
        failed: task.isError,
      };
    }
    case "tree": {
      const d = tree.data;
      return { data: d === undefined ? null : { name: `Tree #${d.id}`, tag: `#${d.id}`, title: d.title, state: `tree ${d.status}`, tone: TREE_TONES[d.status], href: `/trees/${d.id}` }, failed: tree.isError };
    }
    case "schedule": {
      const d = schedule.data;
      return {
        data: d === undefined ? null : { name: `Schedule #${d.id}`, tag: `#${d.id}`, title: d.title, state: d.paused ? "paused" : "schedule", tone: d.paused ? "idle" : "plain", href: `/schedules/${d.id}` },
        failed: schedule.isError,
      };
    }
    case "project": {
      const d = project.data;
      const archived = d?.archivedAt != null;
      return {
        data: d === undefined ? null : { name: `Project #${d.id}`, tag: `#${d.id}`, title: d.title, state: archived ? "archived" : "project", tone: archived ? "idle" : "plain", href: `/projects/${d.id}` },
        failed: project.isError,
      };
    }
    case "journal":
      // Not fetched: the entry's owner page shows and anchors it.
      return {
        data: { name: `Note #${ref.entryId}`, tag: `note #${ref.entryId}`, title: `on ${ref.owner} #${ref.ownerId}`, state: "note", tone: "plain", href: `/${ref.owner}s/${ref.ownerId}?entry=${ref.entryId}#journal-${ref.entryId}` },
        failed: false,
      };
    case "session": {
      const t = session.data;
      return {
        data:
          t === undefined
            ? null
            : {
                name: "Session",
                tag: t.taskId === null ? "session" : `#${t.taskId}`,
                title: t.sessionTitle || ref.id,
                state: t.state === undefined ? "session" : taskStateLabel(t.state),
                tone: t.state === undefined ? "idle" : taskStateTone(t.state),
                href: t.taskId === null ? destination(t) : taskSessionPath(t.taskId, t.attempt),
              },
        failed: session.isError,
      };
    }
  }
}

/** A stage link as a live chip: mono #id (or "session", "note #45"), title, square state marker; opens in stage. */
export function RefChip({ stageRef, text, href }: { stageRef: StageRef; text: string; href: string }) {
  const { data } = useResolved(stageRef);
  if (data === null) {
    // Loading, or not this stage's record/session: keep what was written.
    return (
      <ExternalLink href={href} className="break-all">
        {text}
      </ExternalLink>
    );
  }
  return (
    <a
      href={data.href ?? undefined}
      title={`${data.name}: ${data.title} (${data.state})`}
      className="mx-px inline-flex max-w-full items-center gap-1.5 border border-line-strong bg-surface px-1.5 align-middle font-sans text-small leading-[18px] font-medium whitespace-nowrap text-fg no-underline transition-colors duration-150 hover:bg-panel"
    >
      <Marker tone={data.tone} size="md" />
      <Meta className="font-normal tabular">{data.tag}</Meta>
      <span className="max-w-[36ch] min-w-0 truncate">{data.title}</span>
      <span className="sr-only">, {data.state}</span>
    </a>
  );
}

/** Plain text (a prompt, a handoff block) with stage links rendered as chips. */
export function RefText({ text }: { text: string }): ReactNode {
  return splitStageRefs(text).map((part, i) =>
    part.kind === "text" ? part.text : <RefChip key={i} stageRef={part.ref} text={part.text} href={part.href} />,
  );
}

type Slot = { host: HTMLElement; anchor: HTMLAnchorElement; ref: StageRef; text: string; href: string };

/**
 * Chips for stage links inside sanitized HTML React does not own: each matching anchor is swapped for a host span
 * that receives a portal, and restored on cleanup (StrictMode reruns the effect).
 */
export function useRefSlots(html: string, enabled: boolean): [RefObject<HTMLDivElement | null>, ReactNode] {
  const root = useRef<HTMLDivElement | null>(null);
  const [slots, setSlots] = useState<Slot[]>([]);
  useLayoutEffect(() => {
    const el = root.current;
    // Reruns when the HTML changes: React replaced the nodes the previous run swapped.
    if (!enabled || el === null || html === "") return;
    const found: Slot[] = [];
    for (const anchor of el.querySelectorAll<HTMLAnchorElement>("a[href]")) {
      const href = anchor.getAttribute("href") ?? "";
      const ref = parseStageRef(href);
      if (ref === null) continue;
      const host = document.createElement("span");
      anchor.replaceWith(host);
      found.push({ host, anchor, ref, text: anchor.textContent ?? href, href });
    }
    setSlots(found);
    return () => {
      for (const s of found) s.host.replaceWith(s.anchor);
    };
  }, [html, enabled]);
  const portals = slots.map((s, i) => createPortal(<RefChip stageRef={s.ref} text={s.text} href={s.href} />, s.host, `${i}`));
  return [root, portals];
}

/** A journal entry's source session. */
export function SourceSessionLink({ id, connectionId }: { id: string; connectionId: number }) {
  const { data } = useResolved({ kind: "session", id, connectionId });
  if (data?.href == null) return <span title={id}>source session</span>;
  return <a href={data.href} className={linkClass("rest")}>source session</a>;
}
