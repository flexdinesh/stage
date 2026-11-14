import { useMutation } from "@tanstack/react-query";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useLocation } from "react-router";
import { ApiError, api } from "../api/client.ts";
import { useInvalidateWork, useThreadQuestions } from "../api/queries.ts";
import type { PendingRequest, Question, QuestionAnswer } from "../api/types.ts";
import {
  ANSWER_HASH,
  answerText,
  answeredCount,
  buildAnswers,
  customAllowed,
  initialDraft,
  isSelected,
  optionAnswer,
  setCustom,
  toggleOption,
  type Draft,
} from "../lib/questions.ts";
import { AppButton, Button, Checkbox, Chip, ErrorText, ExternalButton, Radio, Textarea, buttonClass, cx, toneClass } from "./ui.tsx";

/** Links that open the session in T3 (app deep link first). */
export interface T3Links {
  t3DeepLink: string | null;
  t3WebUrl: string | null;
  t3AppUrl: string | null;
}

export function OpenInT3({ links, variant = "secondary", label = "Open in T3" }: { links: T3Links; variant?: "primary" | "secondary"; label?: string }) {
  if (links.t3DeepLink !== null) return <AppButton small variant={variant} href={links.t3DeepLink}>{label}</AppButton>;
  const href = links.t3WebUrl ?? links.t3AppUrl;
  return href === null ? null : <ExternalButton small variant={variant} href={href}>{label}</ExternalButton>;
}

/** Row quick action: opens the detail page on the Needs input panel. */
export function AnswerButton({ to, onClick, className }: { to: string; onClick?: () => void; className?: string }) {
  return (
    <Link to={`${to}#${ANSWER_HASH}`} onClick={onClick} className={cx(buttonClass("primary", true), className)}>
      Answer
    </Link>
  );
}

/** Focuses the first answer control, else Open in T3 (only approvals wait). */
function focusFirst(panel: HTMLElement) {
  const el = panel.querySelector<HTMLElement>("form input, form textarea") ?? panel.querySelector<HTMLElement>("a, button");
  el?.focus({ preventScroll: true });
}

/** Scrolls to the panel and focuses its first control (phone action bar). */
export function focusAnswerPanel() {
  const panel = document.getElementById(ANSWER_HASH);
  if (panel === null) return;
  panel.scrollIntoView({ behavior: "smooth", block: "start" });
  focusFirst(panel);
}

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;

type Notice = { tone: "ok" | "idle"; text: string; detail?: string };

/**
 * Mounts the Needs input panel while the thread has pending requests, and
 * keeps it after an answer so its confirmation stays visible once T3 drops
 * the request (until the page changes).
 */
export function NeedsInputSlot({ pending, threadId, links }: { pending: number; threadId: string; links: T3Links }) {
  const [answered, setAnswered] = useState(false);
  if (pending === 0 && !answered) return null;
  return <NeedsInputPanel threadId={threadId} links={links} onNotice={() => setAnswered(true)} />;
}

/**
 * Needs input panel (top of task and session detail): answers T3 user-input
 * questions; approvals are counted and sent to T3.
 */
function NeedsInputPanel({ threadId, links, onNotice }: { threadId: string; links: T3Links; onNotice: () => void }) {
  const query = useThreadQuestions(threadId, true);
  const invalidate = useInvalidateWork();
  const [notices, setNotices] = useState<Record<string, Notice>>({});
  const panel = useRef<HTMLElement>(null);
  const { hash } = useLocation();
  const landed = useRef(false);

  const data = query.data;
  const notice = (requestId: string, n: Notice) => {
    setNotices((prev) => ({ ...prev, [requestId]: n }));
    onNotice();
    void invalidate();
  };
  const open = data?.requests.filter((r) => notices[r.requestId] === undefined) ?? [];
  const questionTotal = open.reduce((n, r) => n + r.questions.length, 0);
  const approvals = data?.approvalCount ?? 0;
  const noticed = Object.keys(notices).length > 0;
  // The yellow edge marks only what still waits on the owner.
  const waiting = query.isPending || query.error !== null || open.length > 0 || approvals > 0 || !noticed;

  // Deep link (#answer from a row or push): land on the panel once it has content.
  useEffect(() => {
    if (landed.current || hash !== `#${ANSWER_HASH}` || query.isPending) return;
    landed.current = true;
    const el = panel.current;
    if (el === null) return;
    el.scrollIntoView({ block: "start" });
    focusFirst(el);
  }, [hash, query.isPending]);

  const meta =
    data === undefined
      ? undefined
      : [questionTotal > 0 ? plural(questionTotal, "question") : null, approvals > 0 ? plural(approvals, "approval") : null]
          .filter((s) => s !== null)
          .join(" · ");

  return (
    <section
      ref={panel}
      id={ANSWER_HASH}
      aria-label="Needs input"
      className={cx("min-w-0 scroll-mt-16 border border-l-2 border-line bg-surface", waiting ? "border-l-need" : "border-l-transparent")}
    >
      <div className="flex min-h-8 flex-wrap items-center gap-x-2 gap-y-1 border-b border-line bg-panel px-3 py-1">
        <h2 className="label-caps text-fg [text-box:trim-both_cap_alphabetic]">{waiting ? "Needs input" : "Answered"}</h2>
        {meta === undefined || meta === "" ? null : <Chip tone="need" dot>{meta}</Chip>}
        <div className="ml-auto flex items-center gap-1.5">
          {open.length > 0 && approvals === 0 ? <OpenInT3 links={links} /> : null}
        </div>
      </div>

      {query.isPending ? (
        <div aria-busy="true" aria-label="Loading questions" className="flex flex-col gap-2 p-3">
          <div className="h-4 w-2/3 animate-pulse bg-panel" />
          <div className="h-9 animate-pulse border border-line bg-panel" />
          <div className="h-9 animate-pulse border border-line bg-panel" />
        </div>
      ) : query.error !== null ? (
        <LoadError error={query.error} links={links} onRetry={() => void query.refetch()} />
      ) : (
        <>
          {Object.entries(notices).map(([id, n]) => (
            <NoticeRow key={id} notice={n} />
          ))}
          {open.map((r, i) => (
            <RequestForm
              key={r.requestId}
              threadId={threadId}
              request={r}
              divided={i > 0 || noticed}
              onDone={(n) => notice(r.requestId, n)}
            />
          ))}
          {approvals > 0 ? (
            <ApprovalRow count={approvals} links={links} primary={open.length === 0} divided={open.length > 0 || noticed} />
          ) : null}
          {open.length === 0 && approvals === 0 && !noticed ? (
            <p className="px-3 py-3 text-small text-muted">Nothing is waiting now. T3 may have moved on; this clears on the next sync.</p>
          ) : null}
        </>
      )}
    </section>
  );
}

function LoadError({ error, links, onRetry }: { error: unknown; links: T3Links; onRetry: () => void }) {
  const offline = error instanceof ApiError && error.status === 503;
  return (
    <div className="flex flex-col gap-2.5 p-3">
      {offline ? (
        <p className="text-small">T3 is offline, so stage can't load the questions. Answer in T3, or reconnect in Settings.</p>
      ) : (
        <ErrorText error={error} />
      )}
      <div className="flex flex-wrap gap-2">
        <OpenInT3 links={links} variant="primary" />
        <Button small onClick={onRetry}>
          Retry
        </Button>
      </div>
    </div>
  );
}

function NoticeRow({ notice }: { notice: Notice }) {
  return (
    <div role="status" className={cx(toneClass(notice.tone), "flex items-start gap-2.5 border-b border-line px-3 py-2.5 last:border-b-0")}>
      <span className="mt-[6px] size-[7px] shrink-0 bg-[var(--c)]" />
      <div className="min-w-0">
        <p className="text-small font-medium">{notice.text}</p>
        {notice.detail === undefined ? null : <p className="mt-0.5 font-mono text-meta break-words text-muted">{notice.detail}</p>}
      </div>
    </div>
  );
}

function ApprovalRow({ count, links, primary, divided }: { count: number; links: T3Links; primary: boolean; divided: boolean }) {
  return (
    <div className={cx("flex flex-wrap items-center gap-x-3 gap-y-2 px-3 py-2.5", divided && "border-t border-line")}>
      <p className="min-w-0 flex-1 text-small">
        {count === 1 ? "1 approval waits in T3." : `${count} approvals wait in T3.`}{" "}
        <span className="text-muted">stage can't approve permission requests.</span>
      </p>
      <OpenInT3 links={links} variant={primary ? "primary" : "secondary"} />
    </div>
  );
}

function summary(questions: Question[], answers: Record<string, QuestionAnswer>): string {
  return questions
    .map((q) => {
      const a = answers[q.id];
      return a === undefined ? null : `${q.header === "" ? q.question : q.header}: ${answerText(q, a)}`;
    })
    .filter((s) => s !== null)
    .join(" · ");
}

function RequestForm({
  threadId,
  request,
  divided,
  onDone,
}: {
  threadId: string;
  request: PendingRequest;
  divided: boolean;
  onDone: (n: Notice) => void;
}) {
  const { questions } = request;
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const answers = buildAnswers(questions, drafts);
  const send = useMutation({
    mutationFn: (a: Record<string, QuestionAnswer>) => api.answerQuestion(threadId, request.requestId, a),
    onSuccess: (_res, a) => onDone({ tone: "ok", text: "Answer sent to T3", detail: summary(questions, a) }),
    onError: (err) => {
      if (!(err instanceof ApiError)) return;
      if (err.code === "already_answered") onDone({ tone: "idle", text: "Already answered in T3", detail: err.message });
      if (err.code === "request_stale") onDone({ tone: "idle", text: "No longer pending in T3", detail: err.message });
    },
  });
  const draftOf = (q: Question) => drafts[q.id] ?? initialDraft(q);
  const update = (q: Question, next: Draft) => setDrafts((prev) => ({ ...prev, [q.id]: next }));
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (answers !== null) send.mutate(answers);
  };
  const conflict = send.error instanceof ApiError && send.error.code !== null;

  return (
    <form onSubmit={submit} className={cx(divided && "border-t border-line")}>
      <fieldset disabled={send.isPending} className="flex min-w-0 flex-col">
        {questions.map((q, i) => (
          <QuestionBlock
            key={q.id}
            question={q}
            index={questions.length > 1 ? i + 1 : null}
            name={`${request.requestId}:${q.id}`}
            draft={draftOf(q)}
            onChange={(next) => update(q, next)}
          />
        ))}
      </fieldset>
      <div className="flex flex-col gap-2 border-t border-line bg-panel px-3 py-2.5">
        {send.error === null || conflict ? null : <ErrorText error={send.error} />}
        <div className="flex items-center gap-3">
          <span className="font-mono text-meta-sm text-muted tabular" aria-live="polite">
            {answeredCount(questions, drafts)}/{questions.length} answered
          </span>
          <Button type="submit" variant="primary" className="ml-auto" busy={send.isPending} disabled={answers === null}>
            Send answer
          </Button>
        </div>
      </div>
    </form>
  );
}

function QuestionBlock({
  question: q,
  index,
  name,
  draft,
  onChange,
}: {
  question: Question;
  index: number | null;
  name: string;
  draft: Draft;
  onChange: (next: Draft) => void;
}) {
  const multi = q.multiSelect === true;
  const legendId = `${name}:legend`;
  return (
    <div role="group" aria-labelledby={legendId} className="flex flex-col gap-2.5 border-b border-line px-3 py-3 last:border-b-0">
      <div id={legendId} className="flex flex-col gap-1">
        <div className="flex flex-wrap items-center gap-2">
          {index === null ? null : <span className="font-mono text-meta text-muted tabular">{index}</span>}
          {q.header === "" ? null : <Chip>{q.header}</Chip>}
          {multi ? <span className="font-mono text-meta-sm text-muted">pick any</span> : null}
        </div>
        <p className="text-body font-medium break-words text-pretty">{q.question}</p>
      </div>
      {q.options.length === 0 ? null : (
        <ul className="border border-line">
          {q.options.map((o) => {
            const value = optionAnswer(o);
            const checked = isSelected(q, draft, value);
            const toggle = () => onChange(toggleOption(q, draft, value));
            return (
              <li key={value} className="border-b border-line last:border-b-0">
                <label
                  className={cx(
                    "grid cursor-pointer grid-cols-[16px_minmax(0,1fr)] items-start gap-x-2.5 px-3 py-2.5 transition-colors duration-150 hover:bg-panel md:py-2",
                    checked && "bg-[color-mix(in_srgb,var(--accent)_5%,transparent)]",
                  )}
                >
                  {multi ? (
                    <Checkbox className="mt-[3px]" name={name} checked={checked} onChange={toggle} />
                  ) : (
                    <Radio className="mt-[3px]" name={name} checked={checked} onChange={toggle} />
                  )}
                  <span className="min-w-0">
                    <span className="block text-body font-medium break-words">{o.label}</span>
                    {o.description === "" ? null : <span className="mt-0.5 block text-small break-words text-muted">{o.description}</span>}
                  </span>
                </label>
              </li>
            );
          })}
        </ul>
      )}
      {customAllowed(q) ? (
        <Textarea
          rows={2}
          value={draft.custom}
          onChange={(e) => onChange(setCustom(q, draft, e.target.value))}
          placeholder={q.options.length === 0 ? "Type your answer" : "Or type your own answer"}
          aria-label={q.options.length === 0 ? `Answer: ${q.question}` : `Other answer: ${q.question}`}
          className="resize-y"
        />
      ) : null}
    </div>
  );
}
