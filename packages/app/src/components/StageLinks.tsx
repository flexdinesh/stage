import { useQuery } from "@tanstack/react-query";
import { useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";
import { useApi } from "../platform/context.tsx";
import { useSessionDestination } from "../lib/hostState.ts";
import { keys } from "../api/queries.ts";
import type { TaskState } from "../api/types.ts";
import {
  parseStageRef,
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
import { Button, ExternalLink, IconButton, Marker, Meta, copyStatusText, cx, linkClass, useCopy } from "./ui.tsx";

/** What a copied link points at: a task or a tree. */
export type LinkSubject = { kind: "task" | "tree"; id: number; title: string };

export function subjectLinks(s: LinkSubject, link: StageLink): { markdown: string; url: string; name: string } {
  if (s.kind === "tree") return { markdown: treeMarkdown(link, s.id, s.title), url: treeUrl(link, s.id), name: `tree #${s.id}` };
  return { markdown: taskMarkdown(link, s.id, s.title), url: taskUrl(link, s.id), name: `task #${s.id}` };
}

/** Detail-page header: "Copy link" (self-describing markdown, for prompts) joined to "URL" (bare). */
export function CopyLinkPair({ subject }: { subject: LinkSubject }) {
  const { links } = usePlatform();
  const { markdown, url, name } = subjectLinks(subject, (path) => links.stageLink(path));
  const [mdStatus, copyMd] = useCopy();
  const [urlStatus, copyUrl] = useCopy();
  return (
    <span role="group" aria-label={`Copy link to ${name}`} className="inline-flex">
      <Button small title={markdown} onClick={() => copyMd(markdown)}>
        <IconChainLink />
        {copyStatusText(mdStatus, "Copy link")}
      </Button>
      <Button small title={url} aria-label={urlStatus === "idle" ? "Copy URL" : undefined} className="-ml-px" onClick={() => copyUrl(url)}>
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

type Resolved = { title: string; state: TaskState | null; number: number | null; href: string | null };

function useResolved(ref: StageRef): { data: Resolved | null; failed: boolean } {
  const api = useApi();
  const destination = useSessionDestination();
  const task = useQuery({
    queryKey: [...keys.task, ref.kind === "task" ? ref.id : 0],
    queryFn: () => api.task(ref.kind === "task" ? ref.id : 0),
    enabled: ref.kind === "task",
    refetchInterval: 30_000,
    retry: false,
  });
  // A session ref without a connection names no session to resolve: it stays plain text.
  const sessionConnection = ref.kind === "session" ? (ref.connectionId ?? null) : null;
  const sessionPath = ref.kind === "session" && sessionConnection !== null ? `/connections/${sessionConnection}/sessions/${encodeURIComponent(ref.id)}` : "";
  const thread = useQuery({
    queryKey: ["session-reference",sessionPath],
    queryFn: () => api.resolveRef(sessionPath,undefined,true),
    enabled: sessionPath !== "",
    refetchInterval: 30_000,
    retry: false,
  });
  if (ref.kind === "task") {
    const d = task.data;
    return { data: d === undefined ? null : { title: d.title, state: d.state, number: d.id, href: `/tasks/${d.id}` }, failed: task.isError };
  }
  const t = thread.data;
  return { data: t === undefined ? null : { title: t.sessionTitle || ref.id, state: t.state ?? null, number: t.taskId, href: t.taskId === null ? destination(t) : `/tasks/${t.taskId}` }, failed: thread.isError };
}

/** A stage link as a live chip: mono #id (or "session"), title, square state marker; opens in stage. */
export function RefChip({ stageRef, text, href }: { stageRef: StageRef; text: string; href: string }) {
  const { data } = useResolved(stageRef);
  if (data === null) {
    // Loading, or not this stage's task/session: keep what was written.
    return (
      <ExternalLink href={href} className="break-all">
        {text}
      </ExternalLink>
    );
  }
  const tag = data.number === null ? "session" : `#${data.number}`;
  const state = data.state === null ? "session" : taskStateLabel(data.state);
  const tone = data.state === null ? "idle" : taskStateTone(data.state);
  return (
    <a
      href={data.href ?? undefined}
      title={`${stageRef.kind === "task" ? `Task ${tag}` : "Session"}: ${data.title} (${state})`}
      className="mx-px inline-flex max-w-full items-center gap-1.5 border border-line-strong bg-surface px-1.5 align-middle font-sans text-small leading-[18px] font-medium whitespace-nowrap text-fg no-underline transition-colors duration-150 hover:bg-panel"
    >
      <Marker tone={tone} size="md" />
      <Meta className="font-normal tabular">{tag}</Meta>
      <span className="max-w-[36ch] min-w-0 truncate">{data.title}</span>
      <span className="sr-only">, {state}</span>
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

/** A journal entry's source session; without a connection, the session the server resolves by id alone. */
export function SourceSessionLink({ id, connectionId }: { id: string; connectionId?: number }) {
  const { data } = useResolved(connectionId === undefined ? { kind: "session", id } : { kind: "session", id, connectionId });
  if (data?.href == null) return <span title={id}>source session</span>;
  return <a href={data.href} className={linkClass("rest")}>source session</a>;
}
