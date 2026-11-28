// Folder browsing for a declared `path` field (`directoryBrowse`): whether the connection offers it, where the browser
// opens and what it announces. The harness lists its own host's folders; nothing here names a harness.
import type { Capabilities, DirectoryListing } from "../api/types.ts";

/** The connection declares folder browsing available. */
export function canBrowse(caps: Capabilities | undefined): boolean {
  return caps?.operations.directoryBrowse?.available === true;
}

/** An absolute path on a POSIX host (the only kind adapters list). */
export function isAbsolutePath(value: string): boolean {
  return value.startsWith("/");
}

/** Where the browser opens: the field's absolute path, else "" (the harness's default start). */
export function browseStart(value: string): string {
  const trimmed = value.trim();
  return isAbsolutePath(trimmed) ? trimmed : "";
}

/** What the live region says once a folder is listed. */
export function listingStatus(listing: DirectoryListing): string {
  const n = listing.entries.length;
  const count = n === 0 ? "No folders" : n === 1 ? "1 folder" : `${n}${listing.truncated ? "+" : ""} folders`;
  return `${count} in ${listing.path}`;
}

/** The empty listing's line: hidden folders may still be there. */
export function emptyListing(showHidden: boolean): string {
  return showHidden ? "No folders here." : "No visible folders here. Show hidden to include dot-folders.";
}

/** After going up, focus the folder we came from when it is listed. */
export function returnFocus(listing: DirectoryListing, from: string | null): number {
  if (from === null) return 0;
  const at = listing.entries.findIndex((entry) => entry.path === from);
  return at < 0 ? 0 : at;
}
