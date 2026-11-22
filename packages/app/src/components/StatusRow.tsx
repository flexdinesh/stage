import { useCallback, useState } from "react";
import { Link } from "react-router";
import { useHarnesses, useOpenCodeStatus, useT3Status } from "../api/queries.ts";
import { relativeTime } from "../lib/format.ts";
import { t3Identity } from "../lib/onboarding.ts";
import { openCodeErrorText, openCodeSummary, t3ErrorText, t3Summary, type HarnessSummary, type StatusRead } from "../lib/connections.ts";
import { serviceHostText, signInText } from "../lib/opencode.ts";
import { statusGroups, statusRow, type StatusItem, type StatusRow as Row, type StatusTone } from "../lib/status.ts";
import { IconChevronUp, IconSettings } from "./icons.tsx";
import { InlineError, Marker, Meta, PanelHeader, PopoverPanel, Row as ListRow, RowButton, RowList, cx, rowButtonClass, usePopover } from "./ui.tsx";

/** A status query as the row reads it; a data-less query returns to pending on every refetch, so only the first counts as checking. */
function statusRead<T>(q: { data: T | undefined; isPending: boolean; errorUpdateCount: number; error: Error | null }): StatusRead<T> {
  return { data: q.data, checking: q.isPending && q.errorUpdateCount === 0, error: q.error?.message ?? null };
}

const item = (s: HarnessSummary, name: string): StatusItem => ({
  key: s.harness,
  group: "Harnesses",
  name,
  tone: s.tone,
  state: s.state,
  label: s.label,
});

/** Detail lines per item key, for the panel. */
type Details = Record<string, { lines: string[]; error: string | null }>;

/** The row's sentence and tone, and each item's detail lines: every configured harness (workers join later). */
export function useStatus(): { row: Row; details: Details } {
  const harnesses = useHarnesses();
  const t3 = statusRead(useT3Status());
  const openCode = statusRead(useOpenCodeStatus());
  // Until connections load, keep checking; if they never load, fall back to T3 alone.
  const showT3 = harnesses.loaded ? harnesses.activeConnection("t3")?.configured === true : harnesses.error != null;
  const showOpenCode = harnesses.loaded && harnesses.activeConnection("opencode")?.configured === true;
  const items: StatusItem[] = [];
  const details: Details = {};
  if (showT3) {
    items.push(item(t3Summary(t3), harnesses.kindName("t3")));
    const d = t3.data;
    const expires = d?.tokenExpiresAt ?? null;
    details.t3 = {
      lines: [
        [d === undefined ? null : t3Identity(d), d?.t3Url ?? null].filter((p) => p !== null && p !== "").join(" · "),
        [
          d?.lastFullSyncAt == null ? null : `synced ${relativeTime(d.lastFullSyncAt)}`,
          expires === null ? null : `token ${d?.state === "expired" ? "expired" : "expires"} ${relativeTime(expires)}`,
        ]
          .filter((p) => p !== null)
          .join(" · "),
      ].filter((l) => l !== ""),
      error: t3ErrorText(t3),
    };
  }
  if (showOpenCode) {
    items.push(item(openCodeSummary(openCode), harnesses.kindName("opencode")));
    const d = openCode.data;
    details.opencode = {
      lines: d === undefined ? [] : [[d.endpoint === "" ? null : serviceHostText(d), signInText(d)].filter((p) => p !== null).join(" · "), `checked ${relativeTime(d.checkedAt ?? null)}`],
      error: openCodeErrorText(openCode),
    };
  }
  return { row: statusRow(items, { checking: !harnesses.loaded && !showT3, empty: "Connect a harness" }), details };
}

const TEXT: Record<StatusTone, string> = { ok: "text-muted", idle: "text-muted", need: "text-need-fg", bad: "text-bad" };

/** One section per group; each line links to Settings. `touch` sizes rows for the phone. */
export function StatusSections({ row, details, touch = false, onPick }: { row: Row; details: Details; touch?: boolean; onPick?: () => void }) {
  const groups = statusGroups(row.items);
  if (groups.length === 0) {
    return (
      <p className={cx("flex items-center gap-2 px-3 text-ui text-muted", touch ? "min-h-11" : "min-h-9")}>
        <Marker tone={row.tone} />
        {row.text === "Checking" ? "Checking connections" : "No harness connected yet"}
      </p>
    );
  }
  return groups.map(({ group, items }) => (
    <section key={group} aria-label={group}>
      <PanelHeader title={group} meta={items.length} />
      <RowList>
        {items.map((i) => {
          const d = details[i.key];
          return (
            <ListRow key={i.key} bare>
              <Link
                to="/settings"
                onClick={onPick}
                className="grid grid-cols-[6px_minmax(0,1fr)_auto] items-center gap-x-2 gap-y-0.5 px-3 py-row hover:bg-panel"
              >
                <Marker tone={i.tone} />
                <span className={cx("truncate text-ui", i.tone === "need" || i.tone === "bad" ? TEXT[i.tone] : "text-fg")}>{i.name}</span>
                <span className={cx("font-mono text-meta", TEXT[i.tone])}>{i.state}</span>
                {d?.lines.map((l) => (
                  <Meta key={l} small className="col-start-2 col-end-4 truncate">
                    {l}
                  </Meta>
                ))}
                {d?.error == null ? null : <InlineError className="col-start-2 col-end-4 line-clamp-2">{d.error}</InlineError>}
              </Link>
            </ListRow>
          );
        })}
      </RowList>
    </section>
  ));
}

/**
 * Sidebar footer status row: one marker in the worst tone and one sentence ("All connected", "T3 pairing expired",
 * "2 need attention"). Click opens the details panel above it; hovering with a mouse previews it.
 */
export function StatusRow({ className }: { className?: string }) {
  const { row, details } = useStatus();
  const [hover, setHover] = useState(false);
  const popover = usePopover(useCallback(() => setHover(false), []));
  const shown = popover.open || hover;
  return (
    <div
      {...popover.wrapProps}
      className={cx("relative", className)}
      onPointerEnter={(e) => e.pointerType === "mouse" && setHover(true)}
      onPointerLeave={() => setHover(false)}
    >
      <RowButton {...popover.triggerProps} selected={shown} className="font-mono text-meta">
        <Marker tone={row.tone} />
        <span className={cx("min-w-0 flex-1 truncate", TEXT[row.tone])}>
          <span className="sr-only">Status: </span>
          {row.text}
        </span>
        <span className={cx("flex shrink-0 text-muted transition-transform duration-150 motion-reduce:transition-none", popover.open && "rotate-180")}>
          <IconChevronUp size={12} />
        </span>
      </RowButton>
      <PopoverPanel popover={popover} open={shown} label="Status" className="absolute bottom-full left-0 mb-1 w-[300px]">
        <StatusSections row={row} details={details} onPick={popover.close} />
        <Link to="/settings" onClick={popover.close} className={cx(rowButtonClass(), "border-t border-line text-small text-fg")}>
          <IconSettings size={14} />
          Open settings
        </Link>
      </PopoverPanel>
    </div>
  );
}

/** The More tab's marker tone: only when something needs attention. */
export function useStatusAlert(): StatusTone | null {
  const { row } = useStatus();
  return row.tone === "need" || row.tone === "bad" ? row.tone : null;
}

