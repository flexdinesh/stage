import { useMutation, useQueryClient } from "@tanstack/react-query";
import { lazy, Suspense, useEffect, useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router";
import { ApiError } from "../api/client.ts";
import { keys, useInvalidateWork, useJournal, useJournalEntry } from "../api/queries.ts";
import type { JournalEntry, JournalKind, JournalOwner, JournalQuery, NoteCategory, Task } from "../api/types.ts";
import { useApi } from "../platform/context.tsx";
import { isNoteCategory, journalEntryId, journalEntryPath, journalOwnerKey, MAX_NOTE_BYTES, NOTE_CATEGORIES, noteBytes, noteRequest, type NoteRequest } from "../lib/journal.ts";
import { SourceSessionLink } from "./StageLinks.tsx";
import { ActorCause, ActorLabel } from "./ActorLabel.tsx";
import { SnippetTextarea } from "./SnippetPicker.tsx";
import {
  Alert,
  Button,
  Chip,
  EmptyState,
  ErrorText,
  Field,
  InlineError,
  Input,
  MetaLine,
  Muted,
  Panel,
  RelTime,
  Segmented,
  Skeleton,
  SkeletonBar,
  Combobox,
  TextLink,
  cx,
  rowClass,
} from "./ui.tsx";

const Markdown = lazy(() => import("./ReplyMarkdown.tsx"));
const KIND_OPTIONS: { value: JournalKind; label: string }[] = [
  { value: "notes", label: "Notes" },
  { value: "events", label: "Events" },
  { value: "all", label: "All" },
];

/** One composer per owner; query changes never replace its draft or retry key. */
function NoteComposer({ owner, projectId, tasks }: { owner: JournalOwner; projectId: number | null; tasks: Task[] }) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const [text, setText] = useState("");
  const [category, setCategory] = useState<NoteCategory>("");
  const [selectedTask, setSelectedTask] = useState("");
  const [saved, setSaved] = useState<JournalEntry | null>(null);
  const previous = useRef<NoteRequest | null>(null);
  const member = tasks.find((task) => String(task.id) === selectedTask);
  const target: JournalOwner = owner.treeId !== undefined && member !== undefined ? { taskId: member.id } : owner;
  const missingMember = selectedTask !== "" && member === undefined;
  const bytes = noteBytes(text);
  const valid = text.trim() !== "" && bytes <= MAX_NOTE_BYTES && !missingMember;
  const append = useMutation({
    mutationFn: (body: NoteRequest) => api.appendNote(target, body),
    onSuccess: (entry) => {
      setText("");
      setCategory("");
      previous.current = null;
      setSaved(entry);
      void invalidate();
    },
  });
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!valid || append.isPending) return;
    previous.current = noteRequest(previous.current, text, category);
    setSaved(null);
    append.mutate(previous.current);
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-3 border-b border-line p-3">
      {owner.treeId === undefined ? null : (
        <Field label="Save note to">
          {(id) => (
            <Combobox
              id={id}
              value={selectedTask}
              disabled={append.isPending}
              onChange={(next) => {
                setSelectedTask(next);
                previous.current = null;
              }}
              listLabel="Note owners"
              options={[
                { value: "", label: "This tree" },
                ...tasks.map((task) => ({ value: String(task.id), label: `Task #${task.id}: ${task.title}` })),
                ...(missingMember ? [{ value: selectedTask, label: `Task #${selectedTask} left this tree` }] : []),
              ]}
            />
          )}
        </Field>
      )}
      {missingMember ? <Alert tone="bad">This task left the tree. Choose where to save your note.</Alert> : null}
      <Field label="Add note" hint={target.taskId === undefined ? "Saved to this tree. Notes stay here when tasks move." : "Saved to this task. Notes follow it between trees."}>
        {(id) => (
          <SnippetTextarea
            id={id}
            rows={4}
            value={text}
            onValueChange={setText}
            projectId={member?.projectId ?? projectId}
            disabled={append.isPending}
            placeholder="Record context or a decision. Type / for snippets."
          />
        )}
      </Field>
      <div className="flex flex-wrap items-end gap-3">
        <div className="min-w-0 flex-1">
          <Field label="Category (optional)">
            {(id) => (
              <Combobox
                id={id}
                value={category}
                disabled={append.isPending}
                onChange={(next) => { if (isNoteCategory(next)) setCategory(next); }}
                listLabel="Categories"
                options={[{ value: "", label: "Uncategorized" }, ...NOTE_CATEGORIES]}
              />
            )}
          </Field>
        </div>
        <Button type="submit" variant="primary" disabled={!valid} busy={append.isPending}>Append note</Button>
      </div>
      <MetaLine>
        Append-only
        {`${bytes.toLocaleString()} / ${MAX_NOTE_BYTES.toLocaleString()} bytes`}
      </MetaLine>
      {bytes > MAX_NOTE_BYTES ? <Alert tone="bad">Note exceeds 16 KiB. Shorten it before appending.</Alert> : null}
      <ErrorText error={append.error} />
      <div role="status" className="text-small text-muted empty:hidden">
        {saved === null ? null : <>Note appended. <TextLink to={journalEntryPath(saved)} variant="rest">View entry #{saved.id}</TextLink></>}
      </div>
    </form>
  );
}

export function JournalRow({ entry, anchored = false }: { entry: JournalEntry; anchored?: boolean }) {
  const [expanded, setExpanded] = useState(false);
  const full = useJournalEntry(entry.id, expanded && entry.truncated);
  const shown = expanded ? full.data ?? entry : entry;
  const root = useRef<HTMLElement>(null);
  useEffect(() => {
    if (anchored) root.current?.scrollIntoView({ block: "start" });
  }, [anchored]);
  return (
    <article ref={root} id={`journal-${entry.id}`} className={cx("min-w-0 scroll-mt-20", rowClass)}>
      <div className="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-meta text-muted">
        <TextLink to={journalEntryPath(entry)} variant="rest" aria-label={`Link to journal entry ${entry.id}`}>#{entry.id}</TextLink>
        <RelTime at={entry.createdAt} />
        {entry.kind === "event" ? <span>{entry.eventType.replaceAll("_", " ")}</span> : <Chip>{entry.category || "uncategorized"}</Chip>}
        <ActorLabel work={entry} />
        {entry.taskId === null ? <span>tree {entry.kind}</span> : <TextLink to={`/tasks/${entry.taskId}`} variant="rest">task #{entry.taskId}</TextLink>}
        {entry.attempt === null ? null : <span>attempt {entry.attempt}</span>}
      </div>
      <div className="min-w-0 break-words">
        <Suspense fallback={<p className="text-small whitespace-pre-wrap">{shown.text}</p>}>
          <Markdown text={shown.text} refs />
        </Suspense>
      </div>
      {entry.truncated ? (
        <div className="mt-2 flex flex-col items-start gap-2">
          <Button small variant="ghost" busy={full.isFetching} aria-expanded={expanded} onClick={() => setExpanded((value) => !value)}>
            {expanded ? "Show preview" : "Read full entry"}
          </Button>
          <ErrorText error={full.error} />
          {full.isError && expanded ? <Button small onClick={() => void full.refetch()}>Retry full entry</Button> : null}
        </div>
      ) : null}
      <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 font-mono text-meta-sm text-muted empty:hidden">
        <ActorCause work={entry} taskId={entry.taskId} sourceSession={entry.sourceSession} />
        {entry.sourceTaskId === null ? null : <TextLink to={`/tasks/${entry.sourceTaskId}`} variant="rest">source task #{entry.sourceTaskId}</TextLink>}
        {entry.sourceSession ? <SourceSessionLink id={entry.sourceSession.externalSessionId} connectionId={entry.sourceSession.connectionId} /> : null}
        {entry.fromTreeId === null ? null : <TextLink to={`/trees/${entry.fromTreeId}`} variant="rest">from tree #{entry.fromTreeId}</TextLink>}
        {entry.toTreeId === null ? null : <TextLink to={`/trees/${entry.toTreeId}`} variant="rest">to tree #{entry.toTreeId}</TextLink>}
      </div>
    </article>
  );
}

export function JournalHistoryNotice() {
  return <p className="border-b border-line px-3 py-2 text-small text-muted">Tree event history starts when journals were introduced. Earlier task history remains on each task.</p>;
}

function JournalSkeleton() {
  return (
    <Skeleton label="Loading journal" className="flex flex-col gap-3 p-3">
      <SkeletonBar className="h-3 w-1/3" />
      <SkeletonBar className="h-4 w-full" />
      <SkeletonBar className="h-4 w-2/3" />
    </Skeleton>
  );
}

/** Shared paginated notes/events, also used by the tree Activity tab. */
export function JournalFeed({ owner, query, linkedId = null, active = true }: { owner: JournalOwner; query: Omit<JournalQuery, "cursor">; linkedId?: number | null; active?: boolean }) {
  // A hidden feed (a tab not shown) keeps its pages but stops polling.
  const journal = useJournal(owner, query, { active });
  const linked = useJournalEntry(linkedId);
  const qc = useQueryClient();
  const stale = journal.error instanceof ApiError && journal.error.status === 409;
  const entries = journal.data?.pages.flatMap((page) => page.entries) ?? [];
  const restart = () => qc.resetQueries({ queryKey: [...keys.journal, owner, query], exact: true });
  return (
    <>
      {owner.treeId !== undefined && query.kind !== "notes" ? <JournalHistoryNotice /> : null}
      {linkedId === null ? null : linked.data === undefined ? (
        <div className="border-b border-line p-3">
          <ErrorText error={linked.error} />
          {linked.isPending ? <JournalSkeleton /> : <Button small onClick={() => void linked.refetch()}>Retry linked entry</Button>}
        </div>
      ) : <JournalRow key={`linked-${linkedId}`} entry={linked.data} anchored />}
      {journal.isPending ? <JournalSkeleton /> : null}
      {journal.isError ? (
        <div className="flex flex-col items-start gap-2 border-b border-line p-3">
          <ErrorText error={journal.error} />
          {stale ? <Muted>Tree membership changed. Reload entries to see the current members&apos; notes.</Muted> : null}
          <Button small onClick={() => void (stale ? restart() : journal.refetch())}>{stale ? "Reload entries" : "Retry journal"}</Button>
        </div>
      ) : null}
      {!journal.isPending && !journal.isError && entries.length === 0 ? (
        <EmptyState inset>
          {query.kind === "events" ? "No events match these filters." : "No entries match. Append a note or change the filters."}
        </EmptyState>
      ) : null}
      <div>{entries.filter((entry) => entry.id !== linkedId).map((entry) => <JournalRow key={entry.id} entry={entry} />)}</div>
      {journal.hasNextPage ? (
        <div className="border-t border-line p-3">
          <Button small busy={journal.isFetchingNextPage} disabled={stale} onClick={() => void journal.fetchNextPage()}>Load older entries</Button>
        </div>
      ) : null}
    </>
  );
}

export function JournalPanel({ owner, projectId = null, tasks = [], active = true }: { owner: JournalOwner; projectId?: number | null; tasks?: Task[]; active?: boolean }) {
  const [params] = useSearchParams();
  const [kind, setKind] = useState<JournalKind>("notes");
  const [category, setCategory] = useState<NoteCategory>("");
  const [search, setSearch] = useState("");
  const [task, setTask] = useState("");
  const [filters, setFilters] = useState<Omit<JournalQuery, "kind" | "cursor">>({});
  const taskId = journalEntryId(task);
  const invalidTask = task !== "" && taskId === null;
  const apply = (event: FormEvent) => {
    event.preventDefault();
    if (invalidTask) return;
    setFilters({
      ...(category === "" ? {} : { category }),
      ...(search === "" ? {} : { q: search }),
      ...(taskId === null ? {} : { taskId }),
    });
  };
  return (
    <Panel title="Notes" flush>
      <NoteComposer key={journalOwnerKey(owner)} owner={owner} projectId={projectId} tasks={tasks} />
      <form onSubmit={apply} className="flex flex-col gap-3 border-b border-line p-3">
        <div className="self-start"><Segmented label="Journal entries" options={KIND_OPTIONS} value={kind} onChange={setKind} /></div>
        <div className={owner.treeId === undefined ? "grid grid-cols-1 items-end gap-3 sm:grid-cols-2" : "grid grid-cols-1 items-end gap-3 sm:grid-cols-2 lg:grid-cols-4"}>
          <Field label="Search text">{(id) => <Input id={id} type="search" placeholder="Literal text" value={search} onChange={(e) => setSearch(e.target.value)} />}</Field>
          <Field label="Category">
            {(id) => (
              <Combobox
                id={id}
                value={category}
                onChange={(next) => { if (isNoteCategory(next)) setCategory(next); }}
                listLabel="Categories"
                options={[{ value: "", label: "All categories" }, ...NOTE_CATEGORIES]}
              />
            )}
          </Field>
          {owner.treeId === undefined ? null : <Field label="Task ID">{(id) => <Input id={id} inputMode="numeric" placeholder="All tasks and tree" value={task} aria-invalid={invalidTask} onChange={(e) => setTask(e.target.value)} />}</Field>}
          <div className="flex flex-wrap gap-2">
            <Button type="submit" disabled={invalidTask}>Filter</Button>
            <Button variant="ghost" onClick={() => { setCategory(""); setSearch(""); setTask(""); setFilters({}); }}>Clear</Button>
          </div>
        </div>
        {invalidTask ? <InlineError>Enter a positive task ID.</InlineError> : null}
      </form>
      <JournalFeed owner={owner} query={{ ...filters, kind }} linkedId={active ? journalEntryId(params.get("entry")) : null} active={active} />
    </Panel>
  );
}
