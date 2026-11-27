import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState } from "react";
import { ApiError } from "../../api/client.ts";
import type { SessionRef, SessionSearchResult } from "../../api/types.ts";
import { dateTime, relativeTime } from "../../lib/format.ts";
import { useApi } from "../../platform/context.tsx";
import { Button, ErrorText, Field, Input, Meta, Muted, RowButton, SkeletonRows, rovingKeyDown } from "../ui.tsx";

function SessionRow({ item, busy, onPick }: {
  item: SessionSearchResult;
  busy: boolean;
  onPick: (ref: SessionRef) => void;
}) {
  const id = item.session.externalSessionId;
  const tracked = item.trackedTaskId;
  const available = tracked !== undefined || item.eligibility.available;
  return (
    <li>
      <RowButton
        className="flex-col items-stretch gap-1 py-2.5 focus-visible:bg-panel focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent disabled:opacity-100"
        data-session-result
        disabled={!available}
        aria-disabled={busy || undefined}
        onClick={() => { if (!busy) onPick(item.session); }}
      >
        <span className="flex w-full flex-wrap items-baseline justify-between gap-x-2.5 gap-y-0.5">
          <span className="min-w-0 text-ui font-medium wrap-anywhere">{item.title || id}</span>
          {item.startedAt === undefined ? null : (
            <time className="shrink-0 text-meta text-muted" dateTime={item.startedAt} title={dateTime(item.startedAt)}>
              {relativeTime(item.startedAt)}
            </time>
          )}
        </span>
        {item.workspace ? <Meta className="wrap-anywhere">{item.workspace}</Meta> : null}
        {tracked !== undefined || !available ? (
          <span className="text-small text-muted wrap-anywhere">
            {tracked === undefined ? item.eligibility.reason || "Session unavailable for tracking." : `Tracked as task #${tracked}`}
          </span>
        ) : null}
      </RowButton>
    </li>
  );
}

export function SessionRoster({ connectionId, busy, onPick, onEnterReference }: {
  connectionId: number;
  busy: boolean;
  onPick: (ref: SessionRef) => void;
  onEnterReference: () => void;
}) {
  const api = useApi();
  const client = useQueryClient();
  const scope = useId();
  const results = useRef<HTMLUListElement>(null);
  const [input, setInput] = useState("");
  const [query, setQuery] = useState("");
  const waiting = input.trim() !== query;
  useEffect(() => {
    const timer = setTimeout(() => setQuery(input.trim()), 200);
    return () => clearTimeout(timer);
  }, [input]);

  const queryKey = ["session-roster", scope, connectionId, query];
  const roster = useInfiniteQuery({
    queryKey,
    queryFn: ({ pageParam, signal }) => api.searchSessions(connectionId, query, pageParam || undefined, signal),
    initialPageParam: "",
    getNextPageParam: (page) => page.nextCursor || undefined,
    gcTime: 0,
    staleTime: Infinity,
    retry: false,
  });
  const rows = roster.data?.pages.flatMap((page) => page.sessions) ?? [];
  const unique = new Map(rows.map((item) => [item.session.externalSessionId, item]));
  const error = roster.error;
  const unsupported = error instanceof ApiError && (error.code === "operation_unavailable" || error.status === 404);
  const loading = waiting || roster.isPending;
  return (
    <div className="flex min-w-0 flex-col gap-2.5">
      <div className="flex items-end gap-2">
        <Field label="Search sessions" className="flex-1">
          {(id) => (
            <Input
              id={id}
              type="search"
              data-autofocus
              autoComplete="off"
              placeholder="Title, workspace, or ID"
              value={input}
              readOnly={busy}
              disabled={unsupported}
              onChange={(event) => setInput(event.target.value)}
              onKeyDown={(event) => {
                if (event.key !== "ArrowDown" || busy) return;
                const first = results.current?.querySelector<HTMLButtonElement>("button:enabled");
                if (first === undefined || first === null) return;
                event.preventDefault();
                first.focus();
              }}
            />
          )}
        </Field>
        <Button className="shrink-0" disabled={busy} onClick={onEnterReference}>Use a link or ID</Button>
      </div>
      {loading ? <SkeletonRows label="Searching sessions" /> : (
        <>
          {roster.isSuccess ? <p className="sr-only" role="status">{unique.size} sessions loaded</p> : null}
          {unique.size === 0 && !roster.isError ? (
            <Muted>{query === "" ? "No sessions are listed on this connection." : "No sessions match this search."}</Muted>
          ) : null}
          {unique.size === 0 ? null : (
            <ul ref={results} aria-label="Sessions" className="divide-y divide-line border-y border-line"
              onKeyDown={rovingKeyDown("[data-session-result]")}>
              {[...unique.values()].map((item) => (
                <SessionRow key={item.session.externalSessionId} item={item} busy={busy} onPick={onPick} />
              ))}
            </ul>
          )}
          {unsupported ? <p role="status" className="text-small text-muted">{error.status === 404
            ? "Session search is unavailable on this connection. Use a link or ID." : error.message}</p>
            : <ErrorText error={error} />}
          {roster.isError && !unsupported ? (
            <div><Button onClick={() => void client.resetQueries({ queryKey, exact: true })}>Refresh sessions</Button></div>
          ) : null}
          {!roster.isError && roster.hasNextPage ? (
            <div><Button disabled={busy} busy={roster.isFetchingNextPage} onClick={() => void roster.fetchNextPage()}>Load more sessions</Button></div>
          ) : null}
        </>
      )}
    </div>
  );
}
