import { useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { ApiError } from "../../api/client.ts";
import type { GrantInfo, GrantKind } from "../../api/types.ts";
import { LOGIN_PATH } from "../../lib/accessPaths.ts";
import { dateTime } from "../../lib/format.ts";
import { useApi } from "../../platform/context.tsx";
import { Alert, Button, Field, Input, Muted, PageHeader, SkeletonRows } from "../ui.tsx";

export function AccessFrame({ title, children }: { title: string; children: ReactNode }) {
  useEffect(() => {
    document.title = `${title} · stage`;
  }, [title]);
  return (
    <main className="mx-auto flex min-h-dvh w-full max-w-[432px] flex-col px-4 pt-[clamp(36px,14vh,144px)] pb-8 sm:px-6">
      <div className="mb-6 flex items-center gap-2 font-bold tracking-[-0.02em]">
        <svg width={16} height={16} viewBox="0 0 16 16" aria-hidden className="fill-current">
          <rect x={1} y={11} width={14} height={3} />
          <rect x={3} y={7} width={10} height={3} opacity={0.72} />
          <rect x={5} y={3} width={6} height={3} opacity={0.44} />
        </svg>
        stage
      </div>
      <PageHeader title={title} />
      <div className="flex flex-col gap-3.5">{children}</div>
    </main>
  );
}

export function FormProblem({ error }: { error: unknown }) {
  const summary = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (error != null) summary.current?.focus();
  }, [error]);
  if (error == null) return null;
  const message = error instanceof ApiError ? error.message : "Stage could not complete the request. Try again.";
  return <div ref={summary} tabIndex={-1}><Alert tone="bad">{message}</Alert></div>;
}

export function formText(fields: FormData, name: string): string {
  const value = fields.get(name);
  return typeof value === "string" ? value : "";
}

/** Keep passwords in native fields so browser and third-party password managers can fill them. */
export function PasswordField({ label = "Password", name = "password", fresh = false }: {
  label?: string;
  name?: string;
  fresh?: boolean;
}) {
  const hintId = useId();
  return (
    <Field label={label} hint={fresh ? <span id={hintId}>Use 15 to 128 characters.</span> : undefined}>
      {(id) => <Input id={id} name={name} type="password" autoComplete={fresh ? "new-password" : "current-password"}
        aria-describedby={fresh ? hintId : undefined} required />}
    </Field>
  );
}

export function EmailField({ email, recipient = false }: { email?: string; recipient?: boolean }) {
  if (email !== undefined) {
    return (
      <div className="flex min-w-0 flex-col gap-1.5">
        <p className="label-caps">Email</p>
        <p className="text-body [overflow-wrap:anywhere]">{email}</p>
        <Input hidden type="email" name="email" autoComplete="username" defaultValue={email} />
      </div>
    );
  }
  return (
    <Field label="Email">
      {(id) => <Input id={id} name="email" type="email" autoComplete={recipient ? "email" : "username"} autoCapitalize="none"
        spellCheck={false} required />}
    </Field>
  );
}

export function NameField({ name }: { name?: string }) {
  return (
    <Field label="Display name">
      {(id) => <Input id={id} name="name" type="text" autoComplete="name" required defaultValue={name} />}
    </Field>
  );
}

export function useFormAction(action: (fields: FormData, stillHere: () => boolean) => Promise<void>) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const busy = useRef(false);
  const mounted = useRef(false);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (busy.current) return;
    const form = e.currentTarget;
    const fields = new FormData(form);
    busy.current = true;
    setPending(true);
    setError(null);
    try {
      await action(fields, () => mounted.current);
    } catch (failure) {
      setError(failure);
    } finally {
      for (const input of form.querySelectorAll<HTMLInputElement>('input[type="password"]')) input.value = "";
      busy.current = false;
      setPending(false);
    }
  }
  return { pending, error, submit };
}

export function LoginScreen({ passwordReset, onComplete }: { passwordReset: boolean; onComplete: (stillHere: boolean) => void }) {
  const api = useApi();
  const form = useFormAction(async (fields, stillHere) => {
    await api.login(formText(fields, "email"), formText(fields, "password"));
    onComplete(stillHere());
  });
  return (
    <AccessFrame title="Sign in to Stage">
      {passwordReset ? <Alert tone="plain">Password updated. Sign in with your new password.</Alert> : null}
      <form onSubmit={form.submit} aria-busy={form.pending} className="flex flex-col gap-3.5 border border-line bg-surface p-4">
        <EmailField />
        <PasswordField />
        <FormProblem error={form.error} />
        <Button type="submit" variant="primary" busy={form.pending}>Sign in</Button>
      </form>
    </AccessFrame>
  );
}

const GRANT_TEXT = {
  setup: {
    title: "Set up Stage",
    action: "Create admin account",
    guidance: "Ask the installation owner to generate a new setup link.",
  },
  invite: {
    title: "Accept invitation",
    action: "Create account",
    guidance: "Ask a Stage admin for a new invitation.",
  },
  reset: {
    title: "Reset password",
    action: "Set new password",
    guidance: "Ask your Stage admin or installation owner for a new reset link.",
  },
};

type Inspection = { kind: "loading" } | { kind: "unavailable" } | { kind: "error"; error: unknown } | { kind: "valid"; info: GrantInfo };

export function GrantScreen({ kind, onComplete }: { kind: GrantKind; onComplete: (stillHere: boolean) => void }) {
  const api = useApi();
  const location = useLocation();
  const navigate = useNavigate();
  const [fragment, setFragment] = useState(location.hash);
  const [inspection, setInspection] = useState<Inspection>({ kind: "loading" });
  const [attempt, setAttempt] = useState(() => ({ token: new URLSearchParams(location.hash.slice(1)).get("token") }));
  const { token } = attempt;
  const copy = GRANT_TEXT[kind];

  if (location.hash !== fragment) {
    setFragment(location.hash);
    if (location.hash !== "") {
      setAttempt({ token: new URLSearchParams(location.hash.slice(1)).get("token") });
      setInspection({ kind: "loading" });
    }
  }
  useEffect(() => {
    if (location.hash !== "") void navigate(location.pathname, { replace: true });
  }, [location.hash, location.pathname, navigate]);

  useEffect(() => {
    if (attempt.token == null || attempt.token === "") return;
    let active = true;
    void api.inspectGrant(kind, attempt.token).then(
      (info) => { if (active) setInspection({ kind: "valid", info }); },
      (error: unknown) => {
        if (!active) return;
        setInspection(error instanceof ApiError && (error.code === "grant_unavailable" || error.code === "setup_pending")
          ? { kind: "unavailable" } : { kind: "error", error });
      },
    );
    return () => { active = false; };
  }, [api, attempt, kind]);

  const form = useFormAction(async (fields, stillHere) => {
    if (token === null) return;
    const password = formText(fields, "password");
    try {
      if (kind === "setup") await api.setupAccount(token, formText(fields, "email"), formText(fields, "name"), password);
      else if (kind === "invite") await api.acceptInvite(token, formText(fields, "name"), password);
      else await api.resetPassword(token, password);
    } catch (error) {
      if (error instanceof ApiError && error.code === "grant_unavailable") setInspection({ kind: "unavailable" });
      else throw error;
      return;
    }
    onComplete(stillHere());
  });

  const unavailable = token == null || token === "" || inspection.kind === "unavailable";
  return (
    <AccessFrame title={copy.title}>
      {unavailable ? (
        <>
          <Alert tone="bad">This link is invalid, expired, or already used. {copy.guidance}</Alert>
          <Link to={LOGIN_PATH} replace className="self-start text-body underline underline-offset-4">Go to sign-in</Link>
        </>
      ) : inspection.kind === "loading" ? (
        <SkeletonRows />
      ) : inspection.kind === "error" ? (
        <>
          <FormProblem error={inspection.error} />
          <div><Button onClick={() => { setInspection({ kind: "loading" }); setAttempt({ token }); }}>Try again</Button></div>
        </>
      ) : inspection.kind === "valid" ? (
        <form onSubmit={form.submit} aria-busy={form.pending} className="flex flex-col gap-3.5 border border-line bg-surface p-4">
          {kind === "setup" ? <EmailField /> : inspection.info.email === null ? null : <EmailField email={inspection.info.email} />}
          {kind === "reset" ? null : <NameField />}
          <PasswordField fresh label="New password" />
          <Muted>Link expires {dateTime(inspection.info.expiresAt)}.</Muted>
          <FormProblem error={form.error} />
          <Button type="submit" variant="primary" busy={form.pending}>{copy.action}</Button>
        </form>
      ) : null}
    </AccessFrame>
  );
}
