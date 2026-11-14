import { Link } from "react-router";
import type { PullRequest } from "../api/types.ts";
import { prLabel, prName, prTone, rowPulls } from "../lib/pulls.ts";
import { IconExternal } from "./icons.tsx";
import { Chip, Panel } from "./ui.tsx";

/** Toned `repo#num` chip that opens the PR on its host. */
export function PrChip({ pr }: { pr: PullRequest }) {
  return (
    <a
      href={pr.url}
      target="_blank"
      rel="noreferrer"
      title={`${prName(pr)} · ${pr.state}`}
      aria-label={`Pull request ${prName(pr)}, ${pr.state}`}
      className="inline-flex hover:opacity-80"
    >
      <Chip tone={prTone(pr.state)} dot className="tabular">
        {prLabel(pr)}
      </Chip>
    </a>
  );
}

/** Row PR chips: the first two, then a plain `+n` linking to the detail page that lists them all. */
export function PrChips({ prs, detailTo }: { prs: PullRequest[]; detailTo: string }) {
  if (prs.length === 0) return null;
  const { shown, more } = rowPulls(prs);
  return (
    <span className="inline-flex flex-wrap items-center gap-1.5">
      {shown.map((pr) => (
        <PrChip key={pr.url} pr={pr} />
      ))}
      {more === 0 ? null : (
        <Link to={detailTo} title={`${more} more pull request${more === 1 ? "" : "s"}`} className="inline-flex hover:opacity-80">
          <Chip className="tabular">+{more}</Chip>
        </Link>
      )}
    </span>
  );
}

/** Detail side panel listing every PR; renders nothing without PRs. */
export function PullRequestsPanel({ prs }: { prs: PullRequest[] }) {
  if (prs.length === 0) return null;
  return (
    <Panel title="Pull requests" meta={<span className="tabular">{prs.length}</span>} flush>
      <ul>
        {prs.map((pr) => (
          <li key={pr.url} className="flex items-center gap-2.5 border-b border-line px-3 py-2 last:border-b-0">
            <Chip tone={prTone(pr.state)} dot>
              {pr.state}
            </Chip>
            <a
              href={pr.url}
              target="_blank"
              rel="noreferrer"
              title={pr.url}
              className="inline-flex min-w-0 items-center gap-1.5 font-mono text-detail text-fg hover:underline"
            >
              <span className="truncate tabular">{prName(pr)}</span>
              <span className="shrink-0 text-muted">
                <IconExternal size={12} />
              </span>
            </a>
            <span className="ml-auto shrink-0 font-mono text-meta text-muted">{pr.source}</span>
          </li>
        ))}
      </ul>
    </Panel>
  );
}
