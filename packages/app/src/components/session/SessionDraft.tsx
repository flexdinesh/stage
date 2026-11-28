import { createContext, useContext, useRef, useState, type ReactNode } from "react";
import { useSendMessage } from "../../api/queries.ts";
import type { Delivery, SendMessageRequest } from "../../api/types.ts";
import type { QueueSession } from "../../lib/sessionQueue.ts";

interface Receipt {
  delivery: Delivery;
  at: string;
}

interface Draft {
  text: string;
  setText: (text: string) => void;
  send: (session: QueueSession, request: SendMessageRequest & { delivery: Delivery }) => void;
  pending: Delivery | undefined;
  result: { attempt: number; receipt?: Receipt; error?: unknown } | null;
}

const SessionDraft = createContext<Draft | null>(null);

/** The parent task route keeps the draft and in-flight delivery alive across its children. */
export function SessionDraftProvider({ taskId, children }: { taskId: number; children: ReactNode }) {
  const [text, setText] = useState("");
  const [result, setResult] = useState<Draft["result"]>(null);
  const version = useRef(0);
  const sending = useRef(false);
  const message = useSendMessage(taskId);
  const send: Draft["send"] = (session, request) => {
    if (sending.current) return;
    sending.current = true;
    const submitted = version.current;
    setResult(null);
    void message.mutateAsync({ session, body: request }).then(
      () => {
        if (submitted === version.current) setText("");
        setResult({ attempt: session.launchAttempt, receipt: { delivery: request.delivery, at: new Date().toISOString() } });
      },
      (error: unknown) => setResult({ attempt: session.launchAttempt, error }),
    ).finally(() => { sending.current = false; });
  };
  const edit = (value: string) => {
    version.current += 1;
    setText(value);
  };
  return (
    <SessionDraft.Provider value={{ text, setText: edit, send, pending: message.isPending ? message.variables.body.delivery : undefined, result }}>
      {children}
    </SessionDraft.Provider>
  );
}

export function useSessionDraft(): Draft {
  const draft = useContext(SessionDraft);
  if (draft === null) throw new Error("Session composer requires a task draft provider.");
  return draft;
}
