// Answering a session's pending requests in Stage: the task-detail panel (every approval and question) and a row's
// quick answer (one approval inline, else a link to the panel). The harness stays authoritative: its own client (Open
// in / Copy resume command) keeps answering, and an answer given there first makes Stage's a 404 ("already answered").
// Logic: lib/requests.ts; queries: api/queries.ts useSessionRequests, useRespondRequest.
import { Fragment, useEffect, useId, useRef, useState } from "react";
import { Link, useLocation } from "react-router";
import { useRespondRequest } from "../api/queries.ts";
import type { HarnessChoice, RequestQuestion, RequestResponse, SessionRequest } from "../api/types.ts";
import {
  PREVIEW_LINES,
  REQUESTS_ANCHOR,
  allAnswered,
  answeredText,
  answersResponse,
  draftOf,
  expiredText,
  freeText,
  optionNote,
  optionResponse,
  pick,
  pickOther,
  previewLines,
  requestOptions,
  requestSummary,
  requestsPath,
  respondFailure,
  rowRequestAction,
  type QuestionDraft,
  type QuestionsDraft,
  type RowRequestAction,
} from "../lib/requests.ts";
import { Button, Checkbox, ChoiceRow, DisclosureButton, ErrorText, Input, Panel, Radio, buttonClass, cx, rowClass } from "./ui.tsx";

/** A request's state once answered here: sent (the harness has yet to move on), or no longer pending. */
export type RequestOutcome = "answered" | "expired";

type Respond = (response: RequestResponse) => void;

/** The preview (command, path, input): a sharp mono block, long ones folded to their first lines. */
function Preview({ text }: { text: string }) {
  const { lines, long } = previewLines(text);
  const [open, setOpen] = useState(false);
  const id = useId();
  return (
    <div className="flex flex-col items-start gap-1">
      <pre
        id={id}
        className="w-full overflow-x-auto border border-line bg-panel px-3 py-2 font-mono text-detail whitespace-pre-wrap break-words text-fg"
      >
        {long && !open ? lines.slice(0, PREVIEW_LINES).join("\n") : text}
      </pre>
      {long ? (
        <DisclosureButton open={open} onClick={() => setOpen((o) => !o)} aria-controls={id} className="h-6 font-mono text-meta text-muted hover:text-fg">
          {open ? "Show fewer lines" : `Show all ${lines.length} lines`}
        </DisclosureButton>
      ) : null}
    </div>
  );
}

/** Approval: the first option proceeds (primary); the text option asks for an optional reason before it sends. */
function ApprovalActions({ request, harness, busy, onRespond }: { request: SessionRequest; harness: string; busy: boolean; onRespond: Respond }) {
  const options = requestOptions(request);
  const [reasonFor, setReasonFor] = useState<HarnessChoice | null>(null);
  const [reason, setReason] = useState("");
  const [sent, setSent] = useState<string | null>(null);
  const inputId = useId();
  if (reasonFor !== null) {
    return (
      <form
        className="flex flex-col gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          onRespond(optionResponse(reasonFor.id, reason));
        }}
      >
        <label htmlFor={inputId} className="label-caps">
          Reason for {harness} (optional)
        </label>
        <Input id={inputId} value={reason} autoFocus disabled={busy} onChange={(e) => setReason(e.target.value)} placeholder="Tell it what to do instead" />
        <div className="flex flex-wrap gap-2">
          <Button type="submit" variant="primary" busy={busy}>
            {reasonFor.label}
          </Button>
          <Button variant="ghost" disabled={busy} onClick={() => setReasonFor(null)}>
            Cancel
          </Button>
        </div>
      </form>
    );
  }
  return (
    <div className="flex flex-wrap gap-2 max-md:flex-col max-md:items-stretch">
      {options.map((o, i) => (
        <Button
          key={o.id}
          variant={i === 0 ? "primary" : "secondary"}
          busy={busy && sent === o.id}
          disabled={busy || !o.available}
          title={o.available ? undefined : o.reason}
          // A rule label can be long: it wraps, and the button grows (the base height is fixed, hence the override).
          className="h-auto! min-h-control max-w-full py-1.5"
          onClick={() => {
            if (o.id === request.textOption) return setReasonFor(o);
            setSent(o.id);
            onRespond(optionResponse(o.id));
          }}
        >
          <span className="min-w-0 whitespace-normal break-words">{o.label}</span>
        </Button>
      ))}
    </div>
  );
}

/** One question: radios or checkboxes, plus Other with its text when free answers are allowed; a free-text question is one text field. */
function QuestionField({ q, d, disabled, onChange }: { q: RequestQuestion; d: QuestionDraft; disabled: boolean; onChange: (d: QuestionDraft) => void }) {
  const name = useId();
  const legendId = useId();
  const Control = q.multi === true ? Checkbox : Radio;
  const textOnly = freeText(q);
  return (
    <fieldset disabled={disabled} className="min-w-0">
      <legend id={legendId} className="mb-1 text-body">
        {q.header === undefined || q.header === "" ? null : <span className="mr-2 font-mono text-meta text-muted">{q.header}</span>}
        {q.prompt}
        {q.multi === true && !textOnly ? <span className="ml-2 font-mono text-meta text-muted">pick any</span> : null}
      </legend>
      {textOnly ? <Input aria-labelledby={legendId} value={d.other} onChange={(e) => onChange({ ...d, other: e.target.value })} /> : null}
      {textOnly ? null : <div className="flex flex-col">
        {(q.options ?? []).map((o) => (
          <Fragment key={o.label}>
            <ChoiceRow control={<Control name={name} checked={d.picked.includes(o.label)} onChange={() => onChange(pick(q, d, o.label))} />}>
              <span className="block">{o.label}</span>
              {optionNote(o) === null ? null : <span className="block text-meta text-muted">{optionNote(o)}</span>}
            </ChoiceRow>
            {o.preview === undefined || o.preview.trim() === "" ? null : (
              // Outside the label (its fold toggle is a button), indented to the option labels like Other's input.
              <div className="mt-1 mb-1.5 pl-[26px]">
                <Preview text={o.preview} />
              </div>
            )}
          </Fragment>
        ))}
        {q.custom === true ? (
          <>
            <ChoiceRow control={<Control name={name} checked={d.otherOn} onChange={() => onChange(pickOther(q, d))} />}>Other</ChoiceRow>
            {d.otherOn ? (
              // Indented to the option labels (control 16px + gap 10px).
              <div className="mt-1 mb-1 pl-[26px]">
                <Input aria-label={`Other answer: ${q.prompt}`} autoFocus value={d.other} onChange={(e) => onChange({ ...d, other: e.target.value })} />
              </div>
            ) : null}
          </>
        ) : null}
      </div>}
    </fieldset>
  );
}

function QuestionsForm({ request, busy, onRespond }: { request: SessionRequest; busy: boolean; onRespond: Respond }) {
  const questions = request.questions ?? [];
  const [draft, setDraft] = useState<QuestionsDraft>({});
  const ready = allAnswered(questions, draft);
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (ready) onRespond(answersResponse(questions, draft));
      }}
    >
      {questions.map((q) => (
        <QuestionField key={q.id} q={q} d={draftOf(draft, q.id)} disabled={busy} onChange={(d) => setDraft((all) => ({ ...all, [q.id]: d }))} />
      ))}
      <div>
        <Button type="submit" variant="primary" busy={busy} disabled={!ready} title={ready ? undefined : "Answer every question first"}>
          Send
        </Button>
      </div>
    </form>
  );
}

/** One request: what it asks, then its answer controls, or the outcome once answered here. */
export function RequestCard({
  request,
  harness,
  outcome = null,
  busy = false,
  error = null,
  onRespond,
}: {
  request: SessionRequest;
  /** The harness's display name ("Claude Code"). */
  harness: string;
  outcome?: RequestOutcome | null;
  busy?: boolean;
  error?: unknown;
  onRespond: Respond;
}) {
  return (
    <article className={cx(rowClass, "flex flex-col gap-2.5")} aria-label={request.title}>
      <div className="min-w-0">
        <h3 className="text-body font-medium break-words">{request.title}</h3>
        {request.detail === undefined || request.detail === "" ? null : <p className="mt-0.5 text-small text-muted break-words">{request.detail}</p>}
      </div>
      {request.preview === undefined || request.preview === "" ? null : <Preview text={request.preview} />}
      {outcome !== null ? (
        <p className="font-mono text-meta text-muted" role="status">
          {outcome === "answered" ? answeredText(harness) : expiredText(harness)}
        </p>
      ) : request.kind === "question" ? (
        <QuestionsForm request={request} busy={busy} onRespond={onRespond} />
      ) : (
        <ApprovalActions request={request} harness={harness} busy={busy} onRespond={onRespond} />
      )}
      <ErrorText error={error} />
    </article>
  );
}

/**
 * Task detail: every pending request of the task's session, under the needs-input alert. Answered requests stay in
 * place with their outcome while the task still needs input. `#requests` scrolls here and focuses it.
 */
export function RequestsPanel({ taskId, requests, harness, needsInput }: { taskId: number; requests: readonly SessionRequest[]; harness: string; needsInput: boolean }) {
  const respond = useRespondRequest(taskId);
  const [resolved, setResolved] = useState<ReadonlyMap<string, { request: SessionRequest; outcome: RequestOutcome }>>(new Map());
  const [failed, setFailed] = useState<{ id: string; error: unknown } | null>(null);
  const ref = useRef<HTMLDivElement>(null);
  const { hash } = useLocation();
  const open = requests.filter((r) => !resolved.has(r.id));
  const pendingCount = open.length;
  const shown = needsInput ? [...open, ...resolved.values()] : [];
  const visible = shown.length > 0;

  useEffect(() => {
    if (hash !== `#${REQUESTS_ANCHOR}` || !visible) return;
    ref.current?.scrollIntoView({ block: "start" });
    ref.current?.focus({ preventScroll: true });
  }, [hash, visible]);

  if (!visible) return null;
  const answer = (request: SessionRequest) => (response: RequestResponse) => {
    setFailed(null);
    respond.mutate(
      { requestId: request.id, response },
      {
        onSuccess: () => setResolved((m) => new Map(m).set(request.id, { request, outcome: "answered" })),
        onError: (error) =>
          respondFailure(error) === "expired" ? setResolved((m) => new Map(m).set(request.id, { request, outcome: "expired" })) : setFailed({ id: request.id, error }),
      },
    );
  };
  return (
    <div id={REQUESTS_ANCHOR} ref={ref} tabIndex={-1} className="scroll-mt-4 focus:outline-none">
      <Panel title="Requests" meta={`${pendingCount} pending`} flush>
        {shown.map((entry) => {
          const request = "outcome" in entry ? entry.request : entry;
          const outcome = "outcome" in entry ? entry.outcome : null;
          const pending = respond.isPending && respond.variables.requestId === request.id;
          return (
            <RequestCard
              key={request.id}
              request={request}
              harness={harness}
              outcome={outcome}
              busy={pending}
              error={failed?.id === request.id ? failed.error : null}
              onRespond={answer(request)}
            />
          );
        })}
      </Panel>
    </div>
  );
}

/** A row's one-line summary of its single pending approval (mono, truncated; the full text in the tooltip). */
export function RowRequestLine({ action, className }: { action: RowRequestAction | null; className?: string }) {
  if (action?.kind !== "inline") return null;
  const text = requestSummary(action.request);
  return (
    <p title={text} className={cx("truncate font-mono text-meta text-fg", className)}>
      {text}
    </p>
  );
}

/** A row's answer: proceed (primary) and decline (ghost) for one approval, else Answer on the task's panel. */
export function RowRequestControls({
  action,
  taskId,
  busy = false,
  onRespond,
  onNavigate,
}: {
  action: RowRequestAction | null;
  taskId: number;
  busy?: boolean;
  onRespond: Respond;
  /** Answer leaves the surface (the inbox drawer closes). */
  onNavigate?: () => void;
}) {
  if (action === null) return null;
  if (action.kind === "answer") {
    return (
      <Link to={requestsPath(taskId)} onClick={onNavigate} className={buttonClass("primary", true)}>
        {action.count > 1 ? `Answer ${action.count}` : "Answer"}
      </Link>
    );
  }
  const { proceed, decline } = action;
  return (
    <>
      <Button small variant="primary" busy={busy} disabled={!proceed.available} title={proceed.reason} onClick={() => onRespond(optionResponse(proceed.id))}>
        {proceed.label}
      </Button>
      {decline === null ? null : (
        <Button small variant="ghost" disabled={busy || !decline.available} title={decline.reason} onClick={() => onRespond(optionResponse(decline.id))}>
          {decline.label}
        </Button>
      )}
    </>
  );
}

/** A row's request state: its answer action and the inline answer's mutation. */
export function useRowRequest(taskId: number | null, requests: readonly SessionRequest[]) {
  const respond = useRespondRequest(taskId ?? 0);
  const action = taskId === null ? null : rowRequestAction(requests);
  const onRespond = (response: RequestResponse) => {
    if (action?.kind === "inline") respond.mutate({ requestId: action.request.id, response });
  };
  // A 404 is an answer given in the harness first: the row refreshes, no error to show.
  const error = respond.error !== null && respondFailure(respond.error) === "error" ? respond.error : null;
  return { action, busy: respond.isPending, error, onRespond };
}
