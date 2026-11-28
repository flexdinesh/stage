import { createContext, useContext, useRef, useState, type ReactNode } from "react";
import { useSendMessage } from "../../api/queries.ts";
import type { Delivery, SendMessageRequest } from "../../api/types.ts";

interface Receipt {
  delivery: Delivery;
  at: string;
}

interface Draft {
  text: string;
  setText: (text: string) => void;
  send: (attempt: number, request: SendMessageRequest & { delivery: Delivery }) => void;
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
  const send: Draft["send"] = (attempt, request) => {
    if (sending.current) return;
    sending.current = true;
    const submitted = version.current;
    setResult(null);
    void message.mutateAsync(request).then(
      () => {
        if (submitted === version.current) setText("");
        setResult({ attempt, receipt: { delivery: request.delivery, at: new Date().toISOString() } });
      },
      (error: unknown) => setResult({ attempt, error }),
    ).finally(() => { sending.current = false; });
  };
  const edit = (value: string) => {
    version.current += 1;
    setText(value);
  };
  return (
    <SessionDraft.Provider value={{ text, setText: edit, send, pending: message.isPending ? message.variables.delivery : undefined, result }}>
      {children}
    </SessionDraft.Provider>
  );
}

export function useSessionDraft(): Draft {
  const draft = useContext(SessionDraft);
  if (draft === null) throw new Error("Session composer requires a task draft provider.");
  return draft;
}
