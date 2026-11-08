import { useState } from "react";

const KEY = "t3stage-dashboard-collapsed";

function read(): Set<string> {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(KEY) ?? "[]");
    return new Set(Array.isArray(raw) ? raw.filter((v) => typeof v === "string") : []);
  } catch {
    return new Set();
  }
}

export interface Collapsed {
  has: (key: string) => boolean;
  toggle: (key: string) => void;
}

/** Collapsed dashboard project groups (keys `group:project`), remembered in localStorage. Default expanded. */
export function useCollapsedProjects(): Collapsed {
  const [set, setSet] = useState(read);
  return {
    has: (key) => set.has(key),
    toggle: (key) =>
      setSet((prev) => {
        const next = new Set(prev);
        if (next.has(key)) next.delete(key);
        else next.add(key);
        try {
          localStorage.setItem(KEY, JSON.stringify([...next]));
        } catch {
          // Storage unavailable: collapse still works for this visit.
        }
        return next;
      }),
  };
}
