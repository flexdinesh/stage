// Read-only session transcript (docs/api.md "Transcript"): REST tail page,
// Load earlier, and live upserts over SSE while visible. Loaded lazily.
import { useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useLayoutEffect, useReducer, useRef, useState, type ReactNode } from "react";
import { ApiError } from "../api/client.ts";
import { useApi } from "../platform/context.tsx";
import type { TranscriptItem, TranscriptPage } from "../api/types.ts";
import { dateTime } from "../lib/format.ts";
import {
  emptyTranscript,
  newSince,
  parseJson,
  parseTranscriptItem,
  parseTranscriptState,
  transcriptReducer,
  type TranscriptModel,
} from "../lib/transcript.ts";
// This chunk is already lazy, so the shared renderer (marked + DOMPurify) loads with it.
import { useHostEpoch } from "./Host.tsx";
import ReplyMarkdown from "./ReplyMarkdown.tsx";
import { Alert, Button, Chip, Panel, cx, errorText } from "./ui.tsx";

const PAGE = 50;
/** Distance from the document bottom that still counts as "following the tail". */
const FOLLOW_SLACK = 120;

type JumpOffset = "session" | "task";
type Conn = "connecting" | "open" | "reconnecting" | "closed";

function useDocumentVisible(): boolean {
  const [visible, setVisible] = useState(() => document.visibilityState === "visible");
  useEffect(() => {
    const on = () => setVisible(document.visibilityState === "visible");
    document.addEventListener("visibilitychange", on);
    return () => document.removeEventListener("visibilitychange", on);
  }, []);
  return visible;
}

/** Loads the tail page; a reset or retry refetches it and remounts the live view from scratch. */
export function TranscriptPanel({ threadId, jumpOffset = "session" }: { threadId: string; jumpOffset?: JumpOffset }) {
  const api = useApi();
  const [epoch, setEpoch] = useState(0);
  // A desktop host's server came back: refetch and reopen the stream, as after a reset.
  const hostEpoch = useHostEpoch();
  const tail = useQuery({
    queryKey: ["transcript", threadId, epoch, hostEpoch],
    queryFn: () => api.transcript(threadId, { limit: PAGE }),
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
    refetchInterval: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  const reload = useCallback(() => setEpoch((n) => n + 1), []);

  if (tail.data !== undefined) {
    return <LiveTranscript key={tail.dataUpdatedAt} threadId={threadId} page={tail.data} onReset={reload} jumpOffset={jumpOffset} />;
  }
  if (tail.error !== null) {
    const offline = tail.error instanceof ApiError && tail.error.status === 503;
    return (
      <Panel title="Transcript" flush>
        <div className="p-3">
          <Alert tone="bad" actions={<Button small onClick={reload}>Retry</Button>}>
            {offline ? "T3 not connected. Pair or check the connection in Settings." : `Transcript failed: ${errorText(tail.error)}`}
          </Alert>
        </div>
      </Panel>
    );
  }
  return (
    <Panel title="Transcript" flush>
      <SkeletonRows />
    </Panel>
  );
}

const fromPage = (page: TranscriptPage): TranscriptModel => transcriptReducer(emptyTranscript, { type: "tail", page });

/** SSE upserts on top of the tail page. The stream is open only while the page is visible and reopens from the cursor. */
function LiveTranscript({
  threadId,
  page,
  onReset,
  jumpOffset,
}: {
  threadId: string;
  page: TranscriptPage;
  onReset: () => void;
  jumpOffset: JumpOffset;
}) {
  const api = useApi();
  const [model, dispatch] = useReducer(transcriptReducer, page, fromPage);
  const [conn, setConn] = useState<Conn>("connecting");
  const [earlier, setEarlier] = useState<{ busy: boolean; error: unknown }>({ busy: false, error: null });
  const cursor = useRef(model.cursor);
  const visible = useDocumentVisible();

  useEffect(() => {
    cursor.current = model.cursor;
  }, [model.cursor]);

  useEffect(() => {
    if (!visible) return;
    const stream = api.transcriptStream(threadId, cursor.current, {
      open: () => setConn("open"),
      // The transport retries by itself (resuming from the last event id) unless the server refused the stream.
      error: (closed) => setConn(closed ? "closed" : "reconnecting"),
      events: {
        item: (data) => {
          const item = parseTranscriptItem(parseJson(data));
          if (item !== null) dispatch({ type: "item", item });
        },
        state: (data) => {
          const state = parseTranscriptState(parseJson(data));
          if (state !== null) dispatch({ type: "state", state });
        },
        reset: () => {
          stream.close();
          onReset();
        },
      },
    });
    return () => stream.close();
  }, [api, threadId, visible, onReset]);

  const first = model.items[0];
  const loadEarlier = () => {
    if (first === undefined) return;
    setEarlier({ busy: true, error: null });
    api.transcript(threadId, { before: first.position, limit: PAGE }).then(
      (older) => {
        dispatch({ type: "earlier", page: older });
        setEarlier({ busy: false, error: null });
      },
      (error: unknown) => setEarlier({ busy: false, error }),
    );
  };

  // ---- Tail following ----
  const list = useRef<HTMLOListElement>(null);
  const following = useRef(true);
  const scrolledInitially = useRef(false);
  const last = model.items[model.items.length - 1];
  const lastPosition = last === undefined ? -1 : last.position;
  const [seen, setSeen] = useState(lastPosition);

  // Following = the list's end is on screen. On phone the side panel stacks
  // below the transcript, so the document bottom is not the tail.
  useEffect(() => {
    const on = () => {
      const el = list.current;
      if (el === null) return;
      following.current = el.getBoundingClientRect().bottom <= window.innerHeight + FOLLOW_SLACK;
      if (following.current) setSeen(lastPosition);
    };
    window.addEventListener("scroll", on, { passive: true });
    return () => window.removeEventListener("scroll", on);
  }, [lastPosition]);

  // Open on the latest message, then keep the tail in view while following (new items and growing text).
  useLayoutEffect(() => {
    const el = list.current;
    if (el === null || model.items.length === 0) return;
    if (!scrolledInitially.current) {
      scrolledInitially.current = true;
      el.scrollIntoView({ block: "end" });
      return;
    }
    if (following.current) {
      el.scrollIntoView({ block: "end" });
      setSeen(lastPosition);
    }
  }, [model.items, lastPosition]);

  const unseen = newSince(model.items, seen);
  const jump = () => {
    list.current?.scrollIntoView({ block: "end", behavior: "smooth" });
    following.current = true;
    setSeen(lastPosition);
  };

  const meta =
    conn === "reconnecting" ? (
      <span>reconnecting</span>
    ) : conn === "closed" ? (
      <span>offline</span>
    ) : model.active ? (
      <span className="tone-run inline-flex items-center gap-1.5 text-[var(--cf)]">
        <span className="size-1.5 bg-[var(--c)]" />
        live
      </span>
    ) : (
      <span>idle</span>
    );

  const rows: ReactNode[] = [];
  let prevInherited: boolean | null = null;
  for (const item of model.items) {
    if (item.inherited && prevInherited !== true) rows.push(<Divider key={`d-${item.position}`}>from parent session</Divider>);
    if (!item.inherited && prevInherited === true) rows.push(<Divider key={`d-${item.position}`}>this session</Divider>);
    prevInherited = item.inherited;
    rows.push(<TranscriptRow key={item.position} threadId={threadId} item={item} />);
  }

  return (
    <Panel title="Transcript" meta={meta} flush>
      {model.error === null ? null : (
        <div className="border-b border-line p-3">
          <Alert tone="bad">Live updates paused: {model.error}</Alert>
        </div>
      )}
      {conn === "closed" ? (
        <div className="border-b border-line p-3">
          <Alert tone="bad" actions={<Button small onClick={onReset}>Reconnect</Button>}>
            Live updates stopped.
          </Alert>
        </div>
      ) : null}
      {model.hasEarlier ? (
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2">
          <Button small busy={earlier.busy} onClick={loadEarlier}>
            Load earlier
          </Button>
          {earlier.error === null ? null : <span className="text-meta text-bad">{errorText(earlier.error)}</span>}
        </div>
      ) : null}
      {rows.length === 0 ? (
        <p className="px-3 py-3 text-small text-muted">No messages yet.</p>
      ) : (
        <ol
          ref={list}
          aria-label="Transcript"
          className={cx(
            "flex flex-col md:scroll-mb-4",
            // Clear the phone bottom tabs (and the task action bar) when scrolled to the tail.
            jumpOffset === "task"
              ? "max-md:scroll-mb-[calc(3.5rem+env(safe-area-inset-bottom)+4.75rem)]"
              : "max-md:scroll-mb-[calc(3.5rem+env(safe-area-inset-bottom)+1rem)]",
          )}
        >
          {rows}
        </ol>
      )}
      {unseen > 0 ? (
        <div
          className={cx(
            "pointer-events-none sticky flex justify-center pb-3 md:bottom-3",
            jumpOffset === "task"
              ? "max-md:bottom-[calc(3.5rem+env(safe-area-inset-bottom)+4.25rem)]"
              : "max-md:bottom-[calc(3.5rem+env(safe-area-inset-bottom)+0.5rem)]",
          )}
        >
          <Button small onClick={jump} className="pointer-events-auto">
            <span className="tabular">{unseen}</span> new · Jump to latest
          </Button>
        </div>
      ) : null}
    </Panel>
  );
}

// ---- Rows ----

/** "10:42" today, "Nov 3 10:42" otherwise. */
function stamp(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const time = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
  if (d.toDateString() === new Date().toDateString()) return time;
  return `${d.toLocaleDateString([], { month: "short", day: "numeric" })} ${time}`;
}

function roleLabel(item: TranscriptItem): string {
  if (item.kind === "assistant") return "assistant";
  if (item.kind === "plan") return "plan";
  return item.createdBy === null || item.createdBy === "user" ? "user" : `user · ${item.createdBy}`;
}

function TranscriptRow({ threadId, item }: { threadId: string; item: TranscriptItem }) {
  const api = useApi();
  const [expanded, setExpanded] = useState(false);
  const [overflows, setOverflows] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const content = useRef<HTMLDivElement>(null);
  // Truncated preview: fetch the full text when expanded; a changed item refetches.
  const full = useQuery({
    queryKey: ["transcript-item", threadId, item.itemId, item.updatedAt],
    queryFn: () => api.transcriptItem(threadId, item.itemId),
    enabled: expanded && item.truncated,
    staleTime: Infinity,
    retry: false,
    refetchInterval: false,
    refetchOnWindowFocus: false,
  });
  const text = expanded && full.data !== undefined ? full.data.text : item.text;

  // Content height changes as text streams in; the box stays at its clamp height.
  useLayoutEffect(() => {
    const outer = box.current;
    const inner = content.current;
    if (outer === null || inner === null) return;
    const measure = () => setOverflows(inner.offsetHeight > outer.clientHeight + 1);
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(outer);
    ro.observe(inner);
    return () => ro.disconnect();
  }, []);

  const clamped = !expanded && (overflows || item.truncated);
  const canToggle = expanded || overflows || item.truncated;
  return (
    <li className={cx("border-b border-line px-3 py-2.5 last:border-b-0", item.kind === "user" && "bg-panel")}>
      <div className="mb-1.5 flex min-h-5 items-center gap-2 font-mono text-meta text-muted">
        {item.kind === "plan" ? <Chip>plan</Chip> : <span>{roleLabel(item)}</span>}
        {item.streaming ? (
          <Chip tone="run" dot>
            streaming
          </Chip>
        ) : null}
        <span className="tabular ml-auto" title={dateTime(item.updatedAt)}>
          {stamp(item.updatedAt)}
        </span>
      </div>
      <div ref={box} className={cx("min-w-0", !expanded && "max-h-[23.2em] overflow-hidden", clamped && "transcript-clamp")}>
        <div ref={content}>
          <ReplyMarkdown text={text} refs={item.kind === "user"} />
        </div>
      </div>
      {canToggle ? (
        <div className="mt-1.5 flex flex-wrap items-center gap-2">
          <Button
            variant="ghost"
            small
            busy={expanded && full.isFetching}
            aria-expanded={expanded}
            onClick={() => setExpanded((v) => !v)}
            className="-ml-2"
          >
            {expanded ? "Show less" : "Show all"}
          </Button>
          {full.error === null ? null : <span className="text-meta text-bad">Full text failed: {errorText(full.error)}</span>}
        </div>
      ) : null}
    </li>
  );
}

function Divider({ children }: { children: ReactNode }) {
  return (
    <li className="flex items-center gap-2 border-b border-line px-3 py-1.5">
      <span className="label-caps">{children}</span>
      <span className="h-px flex-1 bg-line" />
    </li>
  );
}

function SkeletonRows() {
  return (
    <div aria-busy="true" aria-label="Loading transcript" className="flex flex-col">
      {[0, 1, 2].map((i) => (
        <div key={i} className="flex flex-col gap-2 border-b border-line px-3 py-3 last:border-b-0">
          <div className="h-3 w-20 animate-pulse bg-panel" />
          <div className="h-3 w-full animate-pulse bg-panel" />
          <div className="h-3 w-2/3 animate-pulse bg-panel" />
        </div>
      ))}
    </div>
  );
}
