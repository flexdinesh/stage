import { useMutation } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { ApiError } from "../../api/client.ts";
import { useConnections, useHarnesses, useInvalidateWork, useMachines } from "../../api/queries.ts";
import type { AdoptSessionRequest, SessionPreview, SessionRef, Task, TreeDetail } from "../../api/types.ts";
import { connectionOptions } from "../../lib/machines.ts";
import { useApi } from "../../platform/context.tsx";
import { BlockedReason } from "../LaunchGate.tsx";
import { SnippetTextarea } from "../SnippetPicker.tsx";
import { Button, Combobox, Drawer, ErrorText, Field, InlineError, Input, Meta, Muted } from "../ui.tsx";
import { SessionRoster } from "./SessionRoster.tsx";

export function useTrackSessionBlock(): string | null {
  const connections = useConnections();
  if (connections.data === undefined) return connections.error?.message ?? "Checking harness connections.";
  return connections.data.connections.some((c) => c.active && c.configured)
    ? null : "Connect a harness in Settings to track sessions.";
}

type PreviewTarget = SessionRef | { link: string; connectionId: number };

export function AdoptSessionDrawer({ tree, initial, initialConnectionId, onClose }: {
  tree?: TreeDetail;
  initial?: SessionRef;
  initialConnectionId?: number;
  onClose: () => void;
}) {
  const api = useApi();
  const connections = useConnections();
  const harnesses = useHarnesses();
  const { machines } = useMachines();
  const invalidate = useInvalidateWork();
  const navigate = useNavigate();
  const [chosen, setConnection] = useState(initial?.connectionId ?? initialConnectionId);
  const [direct, setDirect] = useState(false);
  const [link, setLink] = useState(initial?.externalSessionId ?? "");
  const [picked, setPicked] = useState<SessionPreview | null>(null);
  const [title, setTitle] = useState("");
  const [note, setNote] = useState("");
  const available = connections.data?.connections.filter((c) => c.active && c.configured) ?? [];
  const connection = chosen ?? (available.length === 1 ? available[0]?.id : undefined);
  const sessionHint = harnesses.descriptor(harnesses.harness(connection ?? 0) ?? "")?.sessionHint;
  const picker = useRef<HTMLDivElement>(null);
  const previewHeading = useRef<HTMLHeadingElement>(null);
  const refocusPicker = useRef(false);
  const inFlight = useRef<AbortController | null>(null);

  const startRequest = useCallback(() => {
    inFlight.current?.abort();
    const controller = new AbortController();
    inFlight.current = controller;
    return controller.signal;
  }, []);
  useEffect(() => () => inFlight.current?.abort(), []);
  useEffect(() => {
    if (picked !== null) previewHeading.current?.focus();
    else if (refocusPicker.current && connection !== undefined) {
      refocusPicker.current = false;
      picker.current?.querySelector<HTMLElement>(direct ? "[data-session-reference]" : "input[type=search]")?.focus();
    }
  }, [picked, direct, connection]);

  const preview = useMutation({
    mutationFn: async ({ target, signal }: { target: PreviewTarget; signal: AbortSignal }) => {
      const ref = "link" in target
        ? (await api.resolveRef(target.link, target.connectionId, undefined, signal)).session
        : target;
      signal.throwIfAborted();
      if (ref?.connectionId !== target.connectionId) {
        throw new Error("Choose a session link or ID from this connection.");
      }
      return api.sessionContext(ref, signal);
    },
    onSuccess: (session, { signal }) => {
      if (signal.aborted) return;
      setPicked(session);
      setTitle(session.title);
      setNote("");
    },
  });
  const previewMutate = preview.mutate;
  useEffect(() => {
    if (initial !== undefined) previewMutate({ target: initial, signal: startRequest() });
  }, [initial, previewMutate, startRequest]);

  function openTask(task: Task) {
    onClose();
    void navigate(`/tasks/${task.id}`);
  }
  const adopt = useMutation({
    mutationFn: ({ session, signal }: { session: SessionPreview; signal: AbortSignal }) => {
      const body: AdoptSessionRequest = tree === undefined ? {} : { treeId: tree.id };
      if (session.task === null) {
        if (title.trim() !== "") body.title = title.trim();
        if (note.trim() !== "") body.initialNote = note;
      }
      return api.adoptSession(session.session, body, signal);
    },
    onSuccess: async (task, { signal }) => {
      if (signal.aborted) return;
      await invalidate();
      if (!signal.aborted) openTask(task);
    },
  });

  function previewTarget(target: PreviewTarget) {
    preview.mutate({ target, signal: startRequest() });
  }

  function switchMode(next: boolean) {
    refocusPicker.current = true;
    setDirect(next);
    preview.reset();
  }

  const tracked = picked?.task ?? null;
  const changesTree = tree !== undefined && tracked?.treeId !== tree.id;
  const moving = tracked?.treeId != null && changesTree;
  const canConfirm = picked !== null && !picked.unavailableReason;

  return (
    <Drawer
      title={tree === undefined ? "Track session" : "Add session"}
      onClose={onClose}
      footer={picked === null ? undefined : (
        <>
          <Button disabled={adopt.isPending} onClick={() => {
            refocusPicker.current = true;
            setPicked(null);
            adopt.reset();
          }}>Back</Button>
          {tracked === null ? null : (
            <Button variant={changesTree ? "secondary" : "primary"} disabled={adopt.isPending} onClick={() => openTask(tracked)}>Open task</Button>
          )}
          {tracked !== null && !changesTree ? null : (
            <Button
              variant="primary"
              disabled={!canConfirm}
              busy={adopt.isPending}
              onClick={() => adopt.mutate({ session: picked, signal: startRequest() })}
            >
              {tree === undefined ? "Track as task"
                : tracked === null ? "Track and add to tree" : moving ? "Move to this tree" : "Add to this tree"}
            </Button>
          )}
        </>
      )}
    >
      <div ref={picker} hidden={picked !== null}>
        <div className="flex min-w-0 flex-col gap-3.5">
          <Field label="Connection">
            {(id) => (
              <Combobox
                id={id}
                value={connection === undefined ? "" : String(connection)}
                disabled={preview.isPending}
                autoFocus={connection === undefined}
                onChange={(next) => {
                  refocusPicker.current = true;
                  setConnection(Number(next));
                  setLink("");
                  preview.reset();
                }}
                listLabel="Connections"
                showTag
                options={connectionOptions(available, machines, harnesses.kindName)}
                placeholder="Choose a connection"
              />
            )}
          </Field>
          {connection === undefined ? null : direct ? (
            <form className="flex flex-col gap-2" onSubmit={(event) => {
              event.preventDefault();
              if (!preview.isPending && link.trim() !== "") previewTarget({ link: link.trim(), connectionId: connection });
            }}>
              <Field label="Session link or ID" hint={sessionHint || undefined}>
                {(id) => (
                  <Input id={id} data-session-reference autoComplete="off" value={link} readOnly={preview.isPending}
                    onChange={(event) => { setLink(event.target.value); preview.reset(); }} />
                )}
              </Field>
              <div className="flex flex-wrap gap-2">
                <Button type="submit" disabled={link.trim() === ""} busy={preview.isPending}>Preview session</Button>
                <Button disabled={preview.isPending} onClick={() => switchMode(false)}>Browse sessions</Button>
              </div>
            </form>
          ) : (
            <SessionRoster key={connection} connectionId={connection} busy={preview.isPending}
              onPick={previewTarget} onEnterReference={() => switchMode(true)} />
          )}
          {preview.isPending && !direct ? <p role="status" className="text-small text-muted">Opening session preview…</p> : null}
          <ErrorText error={preview.error instanceof ApiError && preview.error.status === 404
            ? "This session is unavailable. Check the link or ID, or choose another session."
            : preview.error} />
        </div>
      </div>
      {picked === null ? null : (
        <div className="flex flex-col gap-3.5">
          <section aria-labelledby="session-preview-heading" className="flex min-w-0 flex-col gap-1.5 border-b border-line pb-3.5">
            <h3 id="session-preview-heading" ref={previewHeading} tabIndex={-1} className="text-title font-semibold wrap-anywhere focus:outline-none">
              {picked.title || "Untitled session"}
            </h3>
            {picked.project === null ? null : <p className="text-small wrap-anywhere">{picked.project.title}</p>}
            {picked.workspace ? <Meta className="wrap-anywhere">{picked.workspace}</Meta> : null}
            <Meta className="break-all">{picked.session.externalSessionId}</Meta>
            {picked.unavailableReason ? <InlineError>{picked.unavailableReason}</InlineError> : null}
            {tracked === null ? null : (
              <Muted>
                Tracked as task #{tracked.id}.
                {moving ? ` Moving it here removes it from ${tracked.treeTitle || "its current tree"}.` : ""}
              </Muted>
            )}
          </section>
          {tracked !== null || !canConfirm ? null : (
            <>
              <Field label="Task title">
                {(id) => <Input id={id} value={title} readOnly={adopt.isPending} onChange={(event) => setTitle(event.target.value)} />}
              </Field>
              <Field label="Initial note (optional)">
                {(id) => (
                  <SnippetTextarea id={id} rows={3} value={note} onValueChange={setNote} projectId={picked.project?.id ?? null} />
                )}
              </Field>
            </>
          )}
          <ErrorText error={adopt.error instanceof ApiError && adopt.error.status === 404
            ? "This session is no longer available. Go back to choose another session."
            : adopt.error} />
        </div>
      )}
    </Drawer>
  );
}

export function TrackSessionButton() {
  const [open, setOpen] = useState(false);
  const reason = useTrackSessionBlock();
  return (
    <>
      <BlockedReason reason={reason ?? undefined} showOnPhone>
        <Button disabled={reason !== null} onClick={() => setOpen(true)}>Track session</Button>
      </BlockedReason>
      {open ? <AdoptSessionDrawer onClose={() => setOpen(false)} /> : null}
    </>
  );
}
