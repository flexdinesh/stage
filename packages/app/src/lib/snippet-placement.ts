/** Gap between the `/` line and the panel. */
export const ANCHOR_GAP = 4;
/** Room kept between the panel and the bottom of the visible area (keyboard top). */
export const ANCHOR_EDGE = 8;
/** Header plus about 4.5 phone rows, so a cut row hints the list scrolls. */
export const ANCHOR_TARGET_HEIGHT = 296;

/** Phone geometry, in layout-viewport px (as `getBoundingClientRect` and fixed boxes use). */
export interface AnchorInput {
  /** Top and bottom of the line holding the trigger's `/`. */
  lineTop: number;
  lineBottom: number;
  /** The textarea's left edge and width; the panel matches them. */
  fieldLeft: number;
  fieldWidth: number;
  /** Visible area: the visual viewport inside the layout viewport (shrinks above the keyboard). */
  viewTop: number;
  viewBottom: number;
  /** Top of the scroll container's visible box; the `/` line never scrolls above it. */
  containerTop: number;
}

export interface AnchorBox {
  top: number;
  left: number;
  width: number;
  maxHeight: number;
}

/**
 * How far to scroll the form when the picker opens so the panel fits at its target height under the `/` line:
 * only the missing room, capped so the line stays below the container's top. Zero when it already fits.
 */
export function anchorRoom(a: AnchorInput): number {
  const needed = a.lineBottom + ANCHOR_GAP + ANCHOR_TARGET_HEIGHT + ANCHOR_EDGE - a.viewBottom;
  const limit = a.lineTop - Math.max(a.containerTop, a.viewTop);
  return Math.max(0, Math.min(needed, limit));
}

/**
 * Panel box right under the `/` line; never flips above. The top follows only the line; the height cap follows the
 * visible bottom, so content shorter than the cap shrinks the panel from the bottom.
 */
export function anchorBox(a: AnchorInput): AnchorBox {
  const top = a.lineBottom + ANCHOR_GAP;
  const maxHeight = Math.max(0, Math.min(ANCHOR_TARGET_HEIGHT, a.viewBottom - ANCHOR_EDGE - top));
  return { top, left: a.fieldLeft, width: a.fieldWidth, maxHeight };
}
