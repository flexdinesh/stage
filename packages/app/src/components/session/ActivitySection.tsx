import type { Activity, ActivityItem } from "../../api/types.ts";
import { activityView } from "../../lib/sessionActivity.ts";
import { IconArrowRight, IconCheck, IconClock, IconCommand, IconMessage, IconSnippets, IconTasks, IconTool } from "../icons.tsx";
import { Chip, EmptyState, ErrorText, Marker, Meta, Panel, RelTime, Row, SkeletonRows, linkClass, type ChipTone } from "../ui.tsx";

const ITEM_ICON: Record<ActivityItem["kind"], typeof IconMessage> = {
  message_user: IconMessage,
  message_agent: IconMessage,
  reasoning: IconMessage,
  tool: IconTool,
  command: IconCommand,
  file_change: IconSnippets,
  plan: IconTasks,
  event: IconClock,
};

const ITEM_TONE: Record<Exclude<ActivityItem["status"], "">, ChipTone> = { running: "run", done: "done", failed: "bad" };

export function ActivitySection({
  activity, loading = false, offline = false, error, harness, href,
}: {
  activity: Activity | undefined;
  loading?: boolean;
  offline?: boolean;
  error?: unknown;
  harness: string;
  href: string | null;
}) {
  const view = activity === undefined ? undefined : activityView(activity);
  return (
    <Panel
      title="Recent activity"
      meta={activity?.model || undefined}
      flush
      actions={href === null ? undefined : (
        <a href={href} className={`${linkClass("rest")} inline-flex items-center gap-1 text-meta`}>
          Full transcript in {harness} <IconArrowRight size={12} />
        </a>
      )}
    >
      {offline ? (
        <EmptyState inset>{view === undefined ? "Activity is offline." : "Activity is offline. Showing the last read."}</EmptyState>
      ) : error != null ? (
        <div className="px-3 py-row"><ErrorText error={error} /></div>
      ) : loading ? <SkeletonRows rows={2} label="Loading activity" /> : null}
      {!offline && error == null && view?.empty === true ? <EmptyState inset>No recent activity.</EmptyState> : null}
      {activity === undefined || view === undefined ? null : (
        <>
          {activity.plan.length === 0 ? null : (
            <section aria-label="Plan" className="border-b border-line px-3 py-row">
              <h3 className="mb-2 text-small font-medium">Plan</h3>
              <ul className="flex flex-col gap-1.5">
                {activity.plan.map((step, index) => (
                  <li key={`${index}:${step.text}`} className="flex items-start gap-2 text-small">
                    <span className="mt-0.5 flex size-3.5 shrink-0 items-center justify-center border border-line-strong">
                      {step.status === "done" ? <IconCheck size={12} /> : step.status === "in_progress" ? <Marker tone="run" /> : null}
                    </span>
                    <span className="sr-only">{step.status.replaceAll("_", " ")}. </span>
                    <span className="wrap-anywhere">{step.text}</span>
                  </li>
                ))}
              </ul>
            </section>
          )}
          {view.files.length === 0 ? null : (
            <section aria-label="Changed files" className="border-b border-line px-3 py-row">
              <h3 className="mb-1 text-small font-medium">Changed files</h3>
              <ul className="divide-y divide-line">
                {view.files.map((file) => (
                  <li key={file.path} className="flex items-baseline justify-between gap-3 py-1 font-mono text-meta">
                    <span className="wrap-anywhere">{file.path}</span>
                    <span className="shrink-0 text-muted tabular">{file.added} {file.removed}</span>
                  </li>
                ))}
              </ul>
            </section>
          )}
          <ol aria-label="Activity items">
            {view.items.map((item) => {
              const Icon = ITEM_ICON[item.kind] ?? IconClock;
              return (
                <Row key={item.id} className="grid grid-cols-[auto_minmax(0,1fr)] items-start gap-x-2.5">
                  <span className="pt-0.5 text-muted"><Icon size={14} /></span>
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                      <span className="min-w-0 flex-1 text-small font-medium wrap-anywhere">{item.title}</span>
                      {item.status === "" ? null : <Chip tone={ITEM_TONE[item.status]}>{item.status}</Chip>}
                      <Meta><RelTime at={item.at} /></Meta>
                    </div>
                    {item.preview === "" ? null : (
                      <p className="mt-1 font-mono text-meta whitespace-pre-wrap wrap-anywhere text-muted">{item.preview}</p>
                    )}
                  </div>
                </Row>
              );
            })}
          </ol>
          {activity.truncated ? <p className="border-t border-line px-3 py-row text-small text-muted">Older activity in {harness}</p> : null}
        </>
      )}
    </Panel>
  );
}
