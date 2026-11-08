import { Link } from "react-router";
import type { Chain } from "../api/types.ts";
import { relativeTime } from "../lib/format.ts";
import { ChainStateBadge } from "./StateBadge.tsx";
import { Muted } from "./ui.tsx";

export function ChainList({ chains, emptyText = "No chains." }: { chains: Chain[]; emptyText?: string }) {
  if (chains.length === 0) return <Muted>{emptyText}</Muted>;
  return (
    <ul className="divide-y divide-neutral-200 rounded-lg border border-neutral-200 bg-white dark:divide-neutral-800 dark:border-neutral-800 dark:bg-neutral-900">
      {chains.map((c) => {
        const current = c.steps.find((s) => s.id === c.currentTaskId);
        const passed = c.steps.filter((s) => s.state === "settled" || s.state === "cancelled").length;
        return (
          <li key={c.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
            <div className="min-w-0 flex-1 basis-60">
              <Link to={`/chains/${c.id}`} className="font-medium hover:underline">
                {c.title}
              </Link>
              <div className="text-xs text-neutral-500">
                {passed}/{c.steps.length} steps passed
                {current === undefined ? "" : ` · current: ${current.title}`}
              </div>
            </div>
            <ChainStateBadge state={c.state} />
            <span className="text-xs text-neutral-500">{relativeTime(c.updatedAt)}</span>
          </li>
        );
      })}
    </ul>
  );
}
