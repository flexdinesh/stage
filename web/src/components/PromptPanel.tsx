// The prompt that started a task or session: first panel of the overview,
// clamped so Last word stays in view (DESIGN.md "Prompt").
import { useQuery } from "@tanstack/react-query";
import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { ApiError, api } from "../api/client.ts";
import type { Attachment, ThreadDetail } from "../api/types.ts";
import { dateTime, relativeTime } from "../lib/format.ts";
import { sessionPromptSource } from "../lib/prompt.ts";
import { CopyButton } from "./CopyButton.tsx";
import { ImageStrip } from "./Images.tsx";
import { Alert, Button, Muted, Panel, cx, errorText } from "./ui.tsx";

/** A truncated T3 preview: the full text loads from the transcript item route. */
interface FullSource {
  threadId: string;
  itemId: string;
  updatedAt: string;
}

/**
 * Prompt panel: mono text clamped to about 6 lines with a fade and Show all.
 * Children (images, handoff, launch notes) sit below the text, unclamped.
 */
export function PromptPanel({
  text,
  full,
  meta,
  children,
}: {
  text: string;
  full?: FullSource | undefined;
  meta?: ReactNode;
  children?: ReactNode;
}) {
  const [expanded, setExpanded] = useState(false);
  const [overflows, setOverflows] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const content = useRef<HTMLPreElement>(null);
  // Fetched up front (only for prompts over the preview cap) so Copy and Show all get the whole text.
  const fullText = useQuery({
    queryKey: ["transcript-item", full?.threadId, full?.itemId, full?.updatedAt],
    queryFn: () => (full === undefined ? Promise.reject(new Error("no item")) : api.transcriptItem(full.threadId, full.itemId)),
    enabled: full !== undefined,
    staleTime: Infinity,
    retry: false,
    refetchInterval: false,
    refetchOnWindowFocus: false,
  });
  const whole = fullText.data?.text ?? text;
  const shown = expanded ? whole : text;

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

  const truncated = full !== undefined;
  const clamped = !expanded && (overflows || truncated);
  const canToggle = expanded || overflows || truncated;
  return (
    <Panel title="Prompt" meta={meta} actions={<CopyButton text={whole} label="Copy" />}>
      <div className="flex flex-col gap-3">
        <div>
          <div ref={box} className={cx("min-w-0", !expanded && "max-h-[9.75em] overflow-hidden", clamped && "transcript-clamp")}>
            <pre ref={content} className="font-mono text-small leading-relaxed break-words whitespace-pre-wrap">
              {shown}
            </pre>
          </div>
          {canToggle ? (
            <div className="mt-1.5 flex flex-wrap items-center gap-2">
              <Button
                variant="ghost"
                small
                busy={expanded && fullText.isFetching}
                aria-expanded={expanded}
                onClick={() => setExpanded((v) => !v)}
                className="-ml-2"
              >
                {expanded ? "Show less" : "Show all"}
              </Button>
              {fullText.error === null ? null : <span className="text-meta text-bad">Full text failed: {errorText(fullText.error)}</span>}
            </div>
          ) : null}
        </div>
        {children}
      </div>
    </Panel>
  );
}

/** Session detail: the linked task's prompt and images, else the first own message read from T3. */
export function SessionPromptPanel({ detail, live }: { detail: Pick<ThreadDetail, "thread" | "task">; live: boolean }) {
  const source = sessionPromptSource(detail);
  if (source.kind === "task") return <TaskPrompt prompt={source.prompt} attachments={source.attachments} />;
  return <OpeningPrompt threadId={source.threadId} live={live} />;
}

function TaskPrompt({ prompt, attachments }: { prompt: string; attachments: readonly Attachment[] }) {
  return (
    <PromptPanel text={prompt}>
      <ImageStrip attachments={attachments} label="Prompt images" />
    </PromptPanel>
  );
}

function OpeningPrompt({ threadId, live }: { threadId: string; live: boolean }) {
  const query = useQuery({
    queryKey: ["transcript-opening", threadId],
    queryFn: () => api.transcriptOpening(threadId),
    // The first message never changes once sent; a launching session is re-read until it has one.
    staleTime: Infinity,
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: (q) => (live && q.state.data?.item === null ? 5000 : false),
  });

  if (query.data !== undefined) {
    const item = query.data.item;
    if (item === null) {
      return (
        <Panel title="Prompt">
          <Muted>{live ? "No prompt yet. It shows once T3 records the first message." : "No prompt in this session."}</Muted>
        </Panel>
      );
    }
    return (
      <PromptPanel
        key={item.itemId}
        text={item.text}
        full={item.truncated ? { threadId, itemId: item.itemId, updatedAt: item.updatedAt } : undefined}
        meta={
          <time dateTime={item.updatedAt} title={dateTime(item.updatedAt)} className="tabular">
            {relativeTime(item.updatedAt)}
          </time>
        }
      />
    );
  }
  if (query.error !== null) {
    const offline = query.error instanceof ApiError && query.error.status === 503;
    return (
      <Panel title="Prompt">
        <Alert tone="bad" actions={<Button small onClick={() => void query.refetch()}>Retry</Button>}>
          {offline ? "T3 not connected, so stage can't read the prompt. Reconnect in Settings." : `Prompt failed: ${errorText(query.error)}`}
        </Alert>
      </Panel>
    );
  }
  return (
    <Panel title="Prompt">
      <div aria-busy="true" aria-label="Loading prompt" className="flex flex-col gap-2">
        <div className="h-3 w-full animate-pulse bg-panel" />
        <div className="h-3 w-2/3 animate-pulse bg-panel" />
      </div>
    </Panel>
  );
}
