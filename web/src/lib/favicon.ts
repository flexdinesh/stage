export const FAVICON_IDLE = "/favicon.svg";
export const FAVICON_NEED = "/favicon-need.svg";

/** Tab favicon: the yellow-step variant while anything needs input; idle while the count is unknown. */
export function faviconHref(needsInput: number | undefined): string {
  return needsInput !== undefined && needsInput > 0 ? FAVICON_NEED : FAVICON_IDLE;
}
