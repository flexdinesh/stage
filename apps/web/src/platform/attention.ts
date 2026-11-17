import type { Attention } from "@stage/app/platform";

export const FAVICON_IDLE = "/favicon.svg";
export const FAVICON_NEED = "/favicon-need.svg";

/** Tab favicon: the yellow-step variant while anything needs input; idle while the count is unknown. */
export function faviconHref(needsInput: number | undefined): string {
  return needsInput !== undefined && needsInput > 0 ? FAVICON_NEED : FAVICON_IDLE;
}

export interface AttentionDeps {
  setFavicon: (href: string) => void;
  /** Installed-app icon badge; null where the Badging API does not exist. */
  badge: { set: (count: number) => Promise<void>; clear: () => Promise<void> } | null;
}

function setFavicon(href: string): void {
  const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
  if (link !== null) link.href = href;
}

const badgingApi = (): AttentionDeps["badge"] =>
  "setAppBadge" in navigator ? { set: (n) => navigator.setAppBadge(n), clear: () => navigator.clearAppBadge() } : null;

/** Tab favicon swaps to its yellow-step variant, and the installed app icon shows the count. */
export function browserAttention(deps: AttentionDeps = { setFavicon, badge: badgingApi() }): Attention {
  return {
    setNeedsInput(count) {
      deps.setFavicon(faviconHref(count));
      if (count === undefined || deps.badge === null) return;
      (count > 0 ? deps.badge.set(count) : deps.badge.clear()).catch(() => undefined);
    },
  };
}
