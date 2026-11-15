import { useQuery } from "@tanstack/react-query";
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";
import { Link } from "react-router";
import { api } from "../api/client.ts";
import { keys } from "../api/queries.ts";
import type { TaskState } from "../api/types.ts";
import { parseStageRef, refPath, sessionMarkdown, sessionUrl, splitStageRefs, taskMarkdown, taskUrl, type StageRef } from "../lib/refs.ts";
import { copyText } from "./CopyButton.tsx";
import { IconChainLink, IconCheck } from "./icons.tsx";
import { taskStateLabel, taskStateTone } from "./StateBadge.tsx";
import { Button, cx, toneClass } from "./ui.tsx";

// ---- Copy ----

type CopyStatus = "idle" | "copied" | "failed";

/** Copy with a status that resets after a moment. */
export function useCopy(): [CopyStatus, (text: string) => void] {
  const [status, setStatus] = useState<CopyStatus>("idle");
  useEffect(() => {
    if (status === "idle") return;
    const t = setTimeout(() => setStatus("idle"), 1500);
    return () => clearTimeout(t);
  }, [status]);
  const copy = (text: string) => {
    copyText(text).then(
      () => setStatus("copied"),
      () => setStatus("failed"),
    );
  };
  return [status, copy];
}

/** What a copied link points at: a task, or a session (T3 thread). */
export type LinkSubject = { kind: "task"; id: number; title: string } | { kind: "session"; id: string; title: string };

export function subjectLinks(s: LinkSubject, origin = window.location.origin): { markdown: string; url: string; name: string } {
  return s.kind === "task"
    ? { markdown: taskMarkdown(origin, s.id, s.title), url: taskUrl(origin, s.id), name: `task #${s.id}` }
    : { markdown: sessionMarkdown(origin, s.id, s.title), url: sessionUrl(origin, s.id), name: "session" };
}

const statusText = (s: CopyStatus, idle: string) => (s === "copied" ? "Copied" : s === "failed" ? "Copy failed" : idle);

/** Detail-page header: "Copy link" (self-describing markdown, for prompts) joined to "URL" (bare). */
export function CopyLinkPair({ subject }: { subject: LinkSubject }) {
  const { markdown, url, name } = subjectLinks(subject);
  const [mdStatus, copyMd] = useCopy();
  const [urlStatus, copyUrl] = useCopy();
  return (
    <span role="group" aria-label={`Copy link to ${name}`} className="inline-flex">
      <Button small title={markdown} onClick={() => copyMd(markdown)}>
        <IconChainLink />
        {statusText(mdStatus, "Copy link")}
      </Button>
      <Button small title={url} aria-label={urlStatus === "idle" ? "Copy URL" : undefined} className="-ml-px" onClick={() => copyUrl(url)}>
        {statusText(urlStatus, "URL")}
      </Button>
      <span className="sr-only" aria-live="polite">
        {mdStatus === "copied" || urlStatus === "copied" ? "Copied" : mdStatus === "failed" || urlStatus === "failed" ? "Copy failed" : ""}
      </span>
    </span>
  );
}

/** Laptop row action: a ghost link icon shown on row hover or keyboard focus (the row carries `group/row`). */
export function RowCopyLink({ subject, className }: { subject: LinkSubject; className?: string }) {
  const { markdown, name } = subjectLinks(subject);
  const [status, copy] = useCopy();
  const label = status === "copied" ? "Copied" : status === "failed" ? "Copy failed" : `Copy link to ${name}`;
  return (
    <button
      type="button"
      title={status === "idle" ? `Copy link to ${name}` : label}
      aria-label={label}
      onClick={() => copy(markdown)}
      className={cx(
        "hidden size-6 shrink-0 items-center justify-center text-muted transition-[opacity,color,background-color] duration-150 hover:bg-panel hover:text-fg focus-visible:opacity-100 md:inline-flex",
        status === "idle" ? "opacity-0 group-focus-within/row:opacity-100 group-hover/row:opacity-100" : "opacity-100",
        status === "failed" && "text-bad",
        className,
      )}
    >
      {status === "copied" ? <IconCheck size={12} /> : <IconChainLink size={14} />}
      <span className="sr-only" aria-live="polite">
        {status === "idle" ? "" : label}
      </span>
    </button>
  );
}

// ---- Chips ----

type Resolved = { title: string; state: TaskState; number: number | null };

function useResolved(ref: StageRef): { data: Resolved | null; failed: boolean } {
  const task = useQuery({
    queryKey: [...keys.task, ref.kind === "task" ? ref.id : 0],
    queryFn: () => api.task(ref.kind === "task" ? ref.id : 0),
    enabled: ref.kind === "task",
    refetchInterval: 30_000,
    retry: false,
  });
  const thread = useQuery({
    queryKey: [...keys.thread, ref.kind === "session" ? ref.id : ""],
    queryFn: () => api.thread(ref.kind === "session" ? ref.id : ""),
    enabled: ref.kind === "session",
    refetchInterval: 30_000,
    retry: false,
  });
  if (ref.kind === "task") {
    const d = task.data;
    return { data: d === undefined ? null : { title: d.title, state: d.state, number: d.id }, failed: task.isError };
  }
  const t = thread.data?.thread;
  return { data: t === undefined ? null : { title: t.title, state: t.state, number: t.taskId }, failed: thread.isError };
}

/** A stage link as a live chip: mono #id (or "session"), title, square state marker; opens in stage. */
export function RefChip({ stageRef, text, href }: { stageRef: StageRef; text: string; href: string }) {
  const { data } = useResolved(stageRef);
  if (data === null) {
    // Loading, or not this stage's task/session: keep what was written.
    return (
      <a href={href} target="_blank" rel="noopener noreferrer" className="underline decoration-line-strong underline-offset-2 break-all">
        {text}
      </a>
    );
  }
  const tag = data.number === null ? "session" : `#${data.number}`;
  const state = taskStateLabel(data.state);
  return (
    <Link
      to={refPath(stageRef)}
      title={`${stageRef.kind === "task" ? `Task ${tag}` : "Session"}: ${data.title} (${state})`}
      className={cx(
        "mx-px inline-flex max-w-full items-center gap-1.5 border border-line-strong bg-surface px-1.5 align-middle font-sans text-small leading-[18px] font-medium whitespace-nowrap text-fg no-underline transition-colors duration-150 hover:bg-panel",
        toneClass(taskStateTone(data.state)),
      )}
    >
      <span aria-hidden className="size-[7px] shrink-0 bg-[var(--c)]" />
      <span className="font-mono text-meta font-normal text-muted tabular">{tag}</span>
      <span className="max-w-[36ch] min-w-0 truncate">{data.title}</span>
      <span className="sr-only">, {state}</span>
    </Link>
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
