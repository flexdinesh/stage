import { useState } from "react";
import { useSteeringEvents, useTree } from "../api/queries.ts";
import type { SteeringEvent, Task, TaskSteering, TreeSteerer } from "../api/types.ts";
import { deliveryError, endReasonLabel, eventPending, steeringEventLine } from "../lib/steering.ts";
import { Button, Chip, Detail, Details, Drawer, EmptyState, ErrorText, Meta, Panel, RelTime, Row, RowList, SkeletonRows, TextLink } from "./ui.tsx";

/**
 * A steerer task's side panel (ADR 0013): the tree it steers, whether that binding is active or how it ended, the
 * delivery progress while active, and the tree's steering feed in a drawer.
 */
export function SteeringPanel({ task, steering }: { task: Pick<Task, "id">; steering: TaskSteering }) {
  const tree = useTree(steering.treeId);
  const [events, setEvents] = useState(false);
  // The tree's steerer is this task only while its binding is active.
  const steerer = steering.active && tree.data?.steerer?.taskId === task.id ? tree.data.steerer : null;
  const title = tree.data?.title ?? `tree ${steering.treeId}`;
  const error = steerer === null ? null : deliveryError(steerer);
  return (
    <Panel title="Steering">
      <div className="flex flex-col gap-3">
        <Details>
          <Detail label="Tree">
            <TextLink to={`/trees/${steering.treeId}`} variant="rest" className="font-sans">
              {title}
            </TextLink>
          </Detail>
          <Detail label="Binding">
            {steering.active ? (
              "active"
            ) : (
              <>
                ended{steering.endReason === undefined ? "" : ` · ${endReasonLabel(steering.endReason)}`}
                {steering.endedAt === undefined ? null : <> · <RelTime at={steering.endedAt} /></>}
              </>
            )}
          </Detail>
          {steerer === null ? null : <Delivery steerer={steerer} />}
        </Details>
        {error === null ? null : <p className="font-mono text-meta break-words text-need-fg">Last delivery failed: {error}</p>}
        <ErrorText error={tree.error} />
        <div>
          <Button small onClick={() => setEvents(true)}>
            Steering events
          </Button>
        </div>
      </div>
      {events ? <SteeringEventsDrawer treeId={steering.treeId} treeTitle={title} active={steerer} onClose={() => setEvents(false)} /> : null}
    </Panel>
  );
}

function Delivery({ steerer }: { steerer: TreeSteerer }) {
  return (
    <>
      <Detail label="Delivered">through #{steerer.deliveredThrough}</Detail>
      <Detail label="Last update">{steerer.lastDeliveredAt === null ? "none yet" : <RelTime at={steerer.lastDeliveredAt} />}</Detail>
      <Detail label="Pending">
        <span className="tabular">{steerer.pending}</span>
      </Detail>
    </>
  );
}

/** The tree's steering feed (every binding), oldest first, a page at a time; marks the steerer's own and undelivered events. */
function SteeringEventsDrawer({ treeId, treeTitle, active, onClose }: { treeId: number; treeTitle: string; active: TreeSteerer | null; onClose: () => void }) {
  const feed = useSteeringEvents(treeId);
  const events = feed.data?.pages.flatMap((p) => p.events) ?? [];
  return (
    <Drawer title="Steering events" onClose={onClose} wide>
      <div className="flex flex-col gap-3">
        <Meta>{`${treeTitle} · oldest first · every steerer of this tree`}</Meta>
        <ErrorText error={feed.error} />
        {feed.isPending ? (
          <SkeletonRows />
        ) : events.length === 0 ? (
          <EmptyState>No steering events yet.</EmptyState>
        ) : (
          <div className="border border-line bg-surface">
            <RowList label="Steering events">
              {events.map((e) => (
                <Row key={e.id}>
                  <EventLine event={e} pending={eventPending(e, active)} />
                </Row>
              ))}
            </RowList>
          </div>
        )}
        {feed.hasNextPage ? (
          <div>
            <Button small busy={feed.isFetchingNextPage} onClick={() => void feed.fetchNextPage()}>
              Load more
            </Button>
          </div>
        ) : null}
      </div>
    </Drawer>
  );
}

function EventLine({ event: e, pending }: { event: SteeringEvent; pending: boolean }) {
  const line = steeringEventLine(e);
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1 font-mono text-meta text-muted">
        <span className="tabular">#{e.id}</span>
        <RelTime at={e.createdAt} className="tabular" />
        <span className="text-fg">{line.label}</span>
        {e.taskId === null ? null : (
          <TextLink to={`/tasks/${e.taskId}`} variant="rest">
            task #{e.taskId}
          </TextLink>
        )}
        {e.self ? <span>by the steerer</span> : null}
        {pending ? <Chip>pending</Chip> : null}
      </div>
      {line.title === null ? null : <p className="min-w-0 text-small break-words">{line.title}</p>}
      {line.detail === null ? null : <p className="line-clamp-3 min-w-0 text-small break-words text-muted">{line.detail}</p>}
    </div>
  );
}
