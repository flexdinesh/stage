import type { Links } from "@stage/app/platform";

export interface LinksDeps {
  origin: () => string;
  open: (url: string) => void;
  assign: (url: string) => void;
}

/** Copied links use the address this page was opened on; app links (t3code://) navigate in place so the OS hands them over. */
export function browserLinks(
  deps: LinksDeps = {
    origin: () => window.location.origin,
    open: (url) => void window.open(url, "_blank", "noopener,noreferrer"),
    assign: (url) => window.location.assign(url),
  },
): Links {
  return {
    appOrigin: () => deps.origin(),
    openExternal: (url) => deps.open(url),
    openApp: (url) => deps.assign(url),
  };
}
