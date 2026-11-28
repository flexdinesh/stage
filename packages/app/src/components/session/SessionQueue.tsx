import { useId, useRef, useState } from "react";
import { useChangeTaskQueue, useTaskQueue } from "../../api/queries.ts";
import { queueView, type QueueSession } from "../../lib/sessionQueue.ts";
import { Button, ConfirmInline, ErrorText, MetaLine, Panel, RelTime, Row } from "../ui.tsx";

export function SessionQueue({ taskId, session, live }: { taskId: number; session: QueueSession; live: boolean }) {
  const queue = useTaskQueue(taskId, session, live);
  const change = useChangeTaskQueue(taskId, session);
  const [confirming, setConfirming] = useState<string | null>(null);
  if (!queue.data?.length) return null;
  return (
    <Panel title="Queued messages" meta={queue.data.length} flush>
      {queue.isError ? <div className="border-b border-line px-3 py-row"><ErrorText error={queue.error} /></div> : null}
      <ol aria-label="Queued messages">
        {queueView(queue.data).map((message) => (
          <QueuedMessageRow
            key={message.id}
            message={message}
            change={change}
            confirming={confirming === message.id}
            onConfirming={setConfirming}
          />
        ))}
      </ol>
    </Panel>
  );
}

function QueuedMessageRow({
  message, change, confirming, onConfirming,
}: {
  message: ReturnType<typeof queueView>[number];
  change: ReturnType<typeof useChangeTaskQueue>;
  confirming: boolean;
  onConfirming: (id: string | null) => void;
}) {
  const previewId = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const { id, preview, at, source, canSteer } = message;
  const selected = change.variables?.id === id;
  const steering = selected && change.variables?.action === "steer";
  const error = selected ? change.error : null;
  return (
    <Row>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <div className="min-w-0 flex-1 basis-52 max-sm:basis-full">
          <p id={previewId} className="line-clamp-2 text-small wrap-anywhere" title={preview}>{preview}</p>
          {at === null && source === null && canSteer ? null : (
            <MetaLine>
              {canSteer ? null : "Steering"}
              {at === null ? null : <RelTime at={at} />}
              {source}
            </MetaLine>
          )}
        </div>
        <div className="ml-auto flex flex-wrap items-center gap-1.5">
          {canSteer ? (
            <Button
              small
              className="max-md:min-h-control"
              aria-describedby={previewId}
              busy={steering && change.isPending}
              disabled={confirming || change.isPending}
              onClick={() => change.mutate({ id, action: "steer" })}
            >
              Steer now
            </Button>
          ) : null}
          <Button
            ref={trigger}
            small
            variant="ghost"
            className="max-md:min-h-control"
            aria-label="Cancel queued message"
            aria-describedby={previewId}
            aria-expanded={confirming}
            disabled={change.isPending}
            onClick={() => {
              change.reset();
              onConfirming(confirming ? null : id);
            }}
          >
            Cancel
          </Button>
        </div>
      </div>
      {confirming ? (
        <div className="mt-2">
          <ConfirmInline
            danger
            confirmLabel="Cancel message"
            busy={selected && !steering && change.isPending}
            disabled={!selected && change.isPending}
            error={steering ? null : error}
            onConfirm={() => change.mutate({ id, action: "cancel" }, { onSuccess: () => onConfirming(null) })}
            onCancel={() => {
              change.reset();
              onConfirming(null);
              trigger.current?.focus();
            }}
          >
            Cancel this queued message?
          </ConfirmInline>
        </div>
      ) : steering && error !== null ? (
        <div className="mt-2"><ErrorText error={error} /></div>
      ) : null}
    </Row>
  );
}
