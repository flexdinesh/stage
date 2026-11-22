import { createContext } from "react";

/** One step of a page's location (project / tree / task); `to` makes it a link. */
export interface TrailSegment {
  label: string;
  to?: string;
}

/** The laptop content bar's location slot that `PageHeader` portals its trail into; null outside the app shell. */
export const TrailSlotContext = createContext<HTMLElement | null>(null);

/** Names the current history entry for the history list; null outside the app shell. */
export const NavLabelContext = createContext<((label: string) => void) | null>(null);
