// The prompt that started a task or session: first panel of the overview,
// clamped so Last word stays in view (DESIGN.md "Prompt").
import { useQuery } from "@tanstack/react-query";
import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { ApiError } from "../api/client.ts";
import { useConnectionOperation } from "../api/queries.ts";
import { useApi } from "../platform/context.tsx";
import { RefText } from "./StageLinks.tsx";
import { Alert, Button, EmptyState, InlineError, Panel, RelTime, Skeleton, SkeletonBar, cx, errorText, CopyButton } from "./ui.tsx";

/** A truncated preview: the full text loads from the adopted task opening-prompt route. */
interface FullSource {
  taskId: number;
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
  const api = useApi();
  const [expanded, setExpanded] = useState(false);
  const [overflows, setOverflows] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const content = useRef<HTMLPreElement>(null);
  // Fetched up front (only for prompts over the preview cap) so Copy and Show all get the whole text.
  const fullText = useQuery({
    queryKey: ["opening-prompt-full", full?.taskId, full?.itemId, full?.updatedAt],
    queryFn: () => (full === undefined ? Promise.reject(new Error("no item")) : api.openingPrompt(full.taskId, true)),
    enabled: full !== undefined,
    staleTime: Infinity,
    retry: false,
    refetchInterval: false,
    refetchOnWindowFocus: false,
  });
  const whole = fullText.data?.item?.text ?? text;
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
          <div ref={box} className={cx("min-w-0", !expanded && "max-h-[9.75em] overflow-hidden", clamped && "prompt-clamp")}>
            <pre ref={content} className="font-mono text-small leading-relaxed break-words whitespace-pre-wrap">
              <RefText text={shown} />
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
              {fullText.error === null ? null : <InlineError>Full text failed: {errorText(fullText.error)}</InlineError>}
            </div>
          ) : null}
        </div>
        {children}
      </div>
    </Panel>
  );
}

/**
 * The session's first own message, read through its harness (an adopted task's detail) when the connection offers
 * operation `opening`; `harness` is its display name. A declared "unavailable" shows the harness's reason instead.
 */
export function OpeningPrompt({ taskId, connectionId, harness, live }: { taskId: number; connectionId: number; harness: string; live: boolean }) {
  const api = useApi();
  const opening = useConnectionOperation(connectionId, "opening", true);
  const unavailable = opening.known && !opening.offered;
  const query = useQuery({
    queryKey: ["opening-prompt", taskId],
    queryFn: () => api.openingPrompt(taskId),
    enabled: !unavailable,
    // The first message never changes once sent; a launching session is re-read until it has one.
    staleTime: Infinity,
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: (q) => (live && q.state.data?.item === null ? 5000 : false),
  });

  if (unavailable) {
    return (
      <Panel title="Prompt">
        <EmptyState>{opening.reason ?? `${harness} does not share a session's first prompt.`}</EmptyState>
      </Panel>
    );
  }
  if (query.data !== undefined) {
    const item = query.data.item;
    if (item === null) {
      return (
        <Panel title="Prompt">
          <EmptyState>{live ? `No prompt yet. It shows once ${harness} records the first message.` : "No prompt in this session."}</EmptyState>
        </Panel>
      );
    }
    return (
      <PromptPanel
        key={item.itemId}
        text={item.text}
        full={item.truncated ? { taskId, itemId: item.itemId, updatedAt: item.updatedAt } : undefined}
        meta={<RelTime at={item.updatedAt} className="tabular" />}
      />
    );
  }
  if (query.error !== null) {
    const offline = query.error instanceof ApiError && query.error.status === 503;
    return (
      <Panel title="Prompt">
        <Alert tone="bad" actions={<Button small onClick={() => void query.refetch()}>Retry</Button>}>
          {offline ? `${harness} not connected, so stage can't read the prompt. Reconnect in Settings.` : `Prompt failed: ${errorText(query.error)}`}
        </Alert>
      </Panel>
    );
  }
  return (
    <Panel title="Prompt">
      <Skeleton label="Loading prompt" className="flex flex-col gap-2">
        <SkeletonBar className="h-3 w-full" />
        <SkeletonBar className="h-3 w-2/3" />
      </Skeleton>
    </Panel>
  );
}
