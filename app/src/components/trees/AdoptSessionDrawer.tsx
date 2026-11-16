import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { useInvalidateWork, useThreads } from "../../api/queries.ts";
import type { AdoptSessionRequest, Thread, TreeDetail } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";
import { relativeTime } from "../../lib/format.ts";
import { canLinkSession } from "../../lib/sessions.ts";
import { TaskStateBadge } from "../StateBadge.tsx";
import { Button, Drawer, ErrorText, Field, Input, QueryState, Radio, Textarea, cx } from "../ui.tsx";

/**
 * Add a T3 session to the tree as a node: search top-level sessions by title, pick one, optionally retitle it and add
 * notes. stage tracks it as a task (never starts it); a session a task already owns moves that task in.
 */
export function AdoptSessionDrawer({ tree, onClose }: { tree: TreeDetail; onClose: () => void }) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const [query, setQuery] = useState("");
  const [picked, setPicked] = useState<Thread | null>(null);
  const [title, setTitle] = useState("");
  const [notes, setNotes] = useState("");
  const q = query.trim();
  const sessions = useThreads({ subagents: "exclude", sort: "activity", limit: 20, ...(q === "" ? {} : { q }) });
  const inTree = new Set(tree.tasks.map((t) => t.t3ThreadId));
  const list = (sessions.data?.threads ?? []).filter((t) => canLinkSession(t) && !inTree.has(t.t3ThreadId));

  const adopt = useMutation({
    mutationFn: (thread: Thread) => {
      const body: AdoptSessionRequest = { treeId: tree.id };
      if (title.trim() !== "") body.title = title.trim();
      if (notes.trim() !== "") body.notes = notes;
      return api.adoptThread(thread.t3ThreadId, body);
    },
    onSuccess: async () => {
      await invalidate();
      onClose();
    },
  });

  const footer = (
    <>
      <span className="min-w-0 flex-1 font-mono text-meta-sm text-muted md:text-right">
        {picked === null ? "required: a session" : picked.taskId === null ? "joins as a root" : `task #${picked.taskId} moves in`}
      </span>
      <Button variant="primary" busy={adopt.isPending} disabled={picked === null} onClick={() => picked !== null && adopt.mutate(picked)}>
        Add to tree
      </Button>
    </>
  );

  return (
    <Drawer title="Add session" onClose={onClose} footer={footer}>
      <div className="flex flex-col gap-4">
        <p className="text-small text-muted">
          A session joins as a tracked task, so other tasks can need it. stage watches it and never starts or restarts it.
        </p>
        <Field label="Find a session">
          {(id) => (
            <Input id={id} type="search" data-autofocus placeholder="Search titles across projects" value={query} onChange={(e) => setQuery(e.target.value)} />
          )}
        </Field>
        <QueryState isPending={sessions.isPending} error={sessions.error}>
          <div role="radiogroup" aria-label="Session" className="border border-line">
            {list.length === 0 ? (
              <p className="px-3 py-2.5 text-small text-muted">No session matches. Subagent and archived sessions cannot join.</p>
            ) : (
              list.map((t) => (
                <label
                  key={t.t3ThreadId}
                  className={cx(
                    "flex min-h-11 cursor-pointer items-center gap-3 border-b border-line px-3 py-2 last:border-b-0 hover:bg-panel md:min-h-10",
                    picked?.t3ThreadId === t.t3ThreadId && "bg-accent/5",
                  )}
                >
                  <Radio
                    name="session"
                    checked={picked?.t3ThreadId === t.t3ThreadId}
                    onChange={() => {
                      setPicked(t);
                      setTitle(t.title);
                    }}
                  />
                  <span className="min-w-0 flex-1">
                    <span className="block text-small font-medium break-words">{t.title}</span>
                    <span className="mt-0.5 block truncate font-mono text-meta text-muted">
                      {[t.projectTitle ?? t.t3ProjectId, relativeTime(t.updatedAt), t.taskId === null ? null : `task #${t.taskId}`]
                        .filter((part) => part !== null)
                        .join(" · ")}
                    </span>
                  </span>
                  <TaskStateBadge state={t.state} />
                </label>
              ))
            )}
          </div>
        </QueryState>
        {picked === null ? null : picked.taskId !== null ? (
          <p className="font-mono text-meta text-muted">Already tracked as task #{picked.taskId}; that task moves into this tree as is.</p>
        ) : (
          <>
            <Field label="Title" hint="Defaults to the session title.">
              {(id) => <Input id={id} value={title} onChange={(e) => setTitle(e.target.value)} />}
            </Field>
            <Field label="Notes">{(id) => <Textarea id={id} rows={3} value={notes} onChange={(e) => setNotes(e.target.value)} />}</Field>
          </>
        )}
        <ErrorText error={adopt.error} />
      </div>
    </Drawer>
  );
}
