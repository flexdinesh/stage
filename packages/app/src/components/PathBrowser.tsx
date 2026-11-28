// The inline folder browser behind a declared `path` field's "Browse…" (`directoryBrowse`). The harness lists folders on
// its own host; the person walks them and picks one, which fills the field. Nothing here names a harness.
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import { ApiError } from "../api/client.ts";
import { connectionCapabilityOptions } from "../api/queries.ts";
import type { DirectoryListing } from "../api/types.ts";
import { browseStart, canBrowse, emptyListing, listingStatus, returnFocus } from "../lib/directoryBrowse.ts";
import { useApi } from "../platform/context.tsx";
import { IconFolder, IconFolderUp } from "./icons.tsx";
import { Button, Checkbox, ChoiceRow, ErrorText, Hint, Muted, Row, RowButton, RowList, SkeletonRows, cx, rovingKeyDown } from "./ui.tsx";

/** Whether the connection offers folder browsing (its declared `directoryBrowse`); false while unknown. */
export function useCanBrowse(connectionId: number | undefined): boolean {
  const api = useApi();
  const caps = useQuery({ ...connectionCapabilityOptions(api, connectionId ?? 0), enabled: connectionId !== undefined });
  return connectionId !== undefined && canBrowse(caps.data);
}

/** A listing, and the typed path that could not be opened when the browser fell back to the harness's default start. */
interface Browsed {
  listing: DirectoryListing;
  failed: { path: string; message: string } | null;
}

const ROW = "[data-folder-row]";

/**
 * The browser: current path and Up, the folder rows, Show hidden, Cancel and Use this folder. It opens at `value` when
 * that is an absolute path (falling back to the default start, with a note, when the harness refuses it). Esc closes it
 * without closing the drawer or panel around it.
 */
export function PathBrowser({ id, connectionId, value, onPick, onClose }: {
  id: string;
  connectionId: number;
  value: string;
  onPick: (path: string) => void;
  onClose: () => void;
}) {
  const api = useApi();
  const headingId = useId();
  const [target, setTarget] = useState(() => browseStart(value));
  // Only the first, typed path may fall back; a folder picked from the list reports its own error.
  const [typed, setTyped] = useState(() => browseStart(value) !== "");
  const [hidden, setHidden] = useState(false);
  const [from, setFrom] = useState<string | null>(null);
  const list = useRef<HTMLDivElement>(null);
  // Opening and walking put focus on the new listing; toggling Show hidden leaves it on the toggle.
  const focusListing = useRef(true);
  const query = useQuery({
    queryKey: ["directories", connectionId, target, hidden, typed],
    queryFn: async ({ signal }): Promise<Browsed> => {
      try {
        return { listing: await api.browseDirectories(connectionId, target, hidden, signal), failed: null };
      } catch (error) {
        if (!typed || !(error instanceof ApiError) || error.status !== 400) throw error;
        return { listing: await api.browseDirectories(connectionId, "", hidden, signal), failed: { path: target, message: error.message } };
      }
    },
    placeholderData: keepPreviousData,
    staleTime: 10_000,
    retry: false,
  });
  const browsed = query.data;
  const listing = browsed?.listing;
  const loading = query.isFetching;
  const current = listing?.path ?? "";

  const go = (path: string, back: string | null) => {
    focusListing.current = true;
    setTyped(false);
    setFrom(back);
    setTarget(path);
  };
  const up = () => {
    if (listing !== undefined && listing.parent !== "" && !loading) go(listing.parent, listing.path);
  };

  // Each new listing puts focus on its first folder, or on the one we came up from.
  const shown = query.isPlaceholderData ? null : listing;
  useEffect(() => {
    if (shown == null || !focusListing.current) return;
    focusListing.current = false;
    const row = list.current?.querySelectorAll<HTMLElement>(ROW)[returnFocus(shown, from)];
    (row ?? document.getElementById(`${id}-use`))?.focus();
  }, [shown, from, id]);

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      onClose();
      return;
    }
    // Enter belongs to the focused control here, never the surrounding form or panel.
    if (event.key === "Enter" && event.target instanceof HTMLInputElement) {
      event.preventDefault();
      event.stopPropagation();
    }
  };
  const onListKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Backspace" || (event.key === "ArrowUp" && event.altKey)) {
      event.preventDefault();
      up();
      return;
    }
    rovingKeyDown(ROW)(event);
  };

  return (
    <div id={id} role="group" aria-labelledby={headingId} onKeyDown={onKeyDown}
      className="flex min-w-0 flex-col border border-line-strong bg-surface">
      <div className="flex min-w-0 items-center gap-2 border-b border-line py-1.5 pl-3 pr-1.5">
        <h3 id={headingId} className="sr-only">Browse folders</h3>
        <span className="min-w-0 flex-1 font-mono text-meta wrap-anywhere" title={current}>
          {current || (query.isError ? "—" : "Opening…")}
        </span>
        <Button small disabled={listing === undefined || listing.parent === "" || loading} onClick={up}
          aria-keyshortcuts="Backspace Alt+ArrowUp">
          <IconFolderUp size={14} />
          Up
        </Button>
      </div>
      {browsed?.failed ? (
        <Hint className="border-b border-line px-3 py-1.5 wrap-anywhere">
          Couldn’t open {browsed.failed.path}: {browsed.failed.message} Showing the default folder.
        </Hint>
      ) : null}
      <div className="max-h-72 min-h-24 overflow-y-auto overscroll-contain" aria-busy={loading || undefined}>
        {listing === undefined ? (
          query.isError ? <div className="p-3"><ErrorText error={query.error} /></div> : <SkeletonRows rows={4} label="Loading folders" />
        ) : (
          <>
            {query.isError ? <div className="border-b border-line p-3"><ErrorText error={query.error} /></div> : null}
            {listing.entries.length === 0 ? (
              <Muted className="px-3 py-2.5">{emptyListing(hidden)}</Muted>
            ) : (
              <div ref={list} onKeyDown={onListKeyDown}>
                <RowList label={`Folders in ${listing.path}`} className={cx(loading && "opacity-55")}>
                  {listing.entries.map((entry) => (
                    <Row key={entry.path} bare>
                      <RowButton data-folder-row="" disabled={loading} title={entry.path}
                        onClick={() => go(entry.path, null)}>
                        <span className="text-muted"><IconFolder /></span>
                        <span className="min-w-0 truncate text-small">{entry.name}</span>
                      </RowButton>
                    </Row>
                  ))}
                </RowList>
              </div>
            )}
            {listing.truncated ? (
              <Hint className="border-t border-line px-3 py-1.5">Showing the first {listing.entries.length} folders. Type the path for one not listed.</Hint>
            ) : null}
          </>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-2 border-t border-line bg-panel px-3 py-1.5">
        <ChoiceRow className="mr-auto" control={<Checkbox checked={hidden} onChange={(e) => { focusListing.current = false; setHidden(e.target.checked); }} />}>
          Show hidden
        </ChoiceRow>
        <Button small onClick={onClose}>Cancel</Button>
        <Button id={`${id}-use`} small variant="primary" disabled={listing === undefined || loading}
          onClick={() => { if (listing !== undefined) onPick(listing.path); }}>
          Use this folder
        </Button>
      </div>
      <div role="status" aria-live="polite" className="sr-only">
        {shown == null ? "" : listingStatus(shown)}
      </div>
    </div>
  );
}
