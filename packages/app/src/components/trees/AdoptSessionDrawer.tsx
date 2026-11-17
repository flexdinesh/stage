import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { useInvalidateWork } from "../../api/queries.ts";
import type { AdoptSessionRequest, Task, Thread, TreeDetail } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";
import { canLinkSession } from "../../lib/sessions.ts";
import { TaskStateBadge } from "../StateBadge.tsx";
import { SnippetTextarea } from "../SnippetPicker.tsx";
import { Button, Drawer, ErrorText, Field, Input, Panel } from "../ui.tsx";

/** Explicit lookup only. Preview never adopts a session. */
export function AdoptSessionDrawer({ tree, onClose, onAdopted }: {
  tree?: TreeDetail; onClose: () => void; onAdopted?: (task: Task) => void;
}) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const navigate = useNavigate();
  const [ref, setRef] = useState("");
  const [picked, setPicked] = useState<Thread | null>(null);
  const [title, setTitle] = useState("");
  const [note, setNote] = useState("");
  const preview = useMutation({
    mutationFn: async () => {
      const resolved = await api.resolveRef(ref.trim());
      if (resolved.threadId === null) throw new Error("Paste a T3 session link or thread ID.");
      return (await api.thread(resolved.threadId)).thread;
    },
    onSuccess: (thread) => { setPicked(thread); setTitle(thread.title); },
  });
  const adopt = useMutation({
    mutationFn: (thread: Thread) => {
      const body: AdoptSessionRequest = tree === undefined ? {} : { treeId: tree.id };
      if (thread.taskId === null) {
        if (title.trim() !== "") body.title = title.trim();
        if (note.trim() !== "") body.initialNote = note;
      }
      return api.adoptThread(thread.t3ThreadId, body);
    },
    onSuccess: async (task) => {
      await invalidate(); onClose();
      if (onAdopted !== undefined) onAdopted(task);
      else await navigate(`/tasks/${task.id}`);
    },
  });
  const eligible = picked !== null && (picked.taskId !== null || canLinkSession(picked));
  return (
    <Drawer title={tree === undefined ? "Track session" : "Add session"} onClose={onClose}
      footer={<Button variant="primary" disabled={!eligible} busy={adopt.isPending}
        onClick={() => { if (picked !== null) adopt.mutate(picked); }}>
        {tree !== undefined ? "Add to tree" : picked?.taskId != null ? "Open task" : "Track as task"}
      </Button>}>
      <div className="flex flex-col gap-3.5">
        <p className="text-small text-muted">Track an existing T3 session. Stage can stop it and follow its state; conversations and restarts stay in T3.</p>
        <form className="flex flex-col gap-2" onSubmit={(event) => { event.preventDefault(); preview.mutate(); }}>
          <Field label="T3 session link or thread ID">{(id) =>
            <Input id={id} data-autofocus value={ref} disabled={preview.isPending || adopt.isPending}
              onChange={(event) => { setRef(event.target.value); setPicked(null); preview.reset(); adopt.reset(); }} />}
          </Field>
          <Button type="submit" disabled={ref.trim() === ""} busy={preview.isPending}>Preview session</Button>
        </form>
        <ErrorText error={preview.error ?? adopt.error} />
        {picked === null ? null : <>
          <Panel title="Session">
            <div className="flex flex-col gap-2">
              <p className="text-small font-medium break-words">{picked.title}</p>
              <p className="font-mono text-meta break-words">{picked.projectTitle ?? picked.t3ProjectId}</p>
              <TaskStateBadge state={picked.state} />
              <p className="font-mono text-meta break-all text-muted">{picked.t3ThreadId}</p>
              {!eligible ? <p className="text-small text-bad">Archived and child sessions cannot be adopted. Choose a top-level, unarchived session in T3.</p> : null}
              {picked.taskId === null ? null : <p className="text-small text-muted">Already tracked as task #{picked.taskId}.{tree === undefined ? "" : " Confirm to move that task into this tree."}</p>}
            </div>
          </Panel>
          {picked.taskId !== null || !eligible ? null : <>
            <Field label="Title">{(id) => <Input id={id} value={title} onChange={(event) => setTitle(event.target.value)} />}</Field>
            <Field label="Initial note">{(id) => <SnippetTextarea id={id} rows={3} value={note} onValueChange={setNote} projectId={picked.projectId} />}</Field>
          </>}
        </>}
      </div>
    </Drawer>
  );
}

export function TrackSessionButton() {
  const [open, setOpen] = useState(false);
  return <><Button onClick={() => setOpen(true)}>Track session</Button>
    {open ? <AdoptSessionDrawer onClose={() => setOpen(false)} /> : null}</>;
}
