import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent, type ReactNode } from "react";
import { ApiError } from "../api/client.ts";
import { useApi } from "../platform/context.tsx";
import type { HostState } from "../platform/index.ts";
import { Button, ErrorText, ExternalButton, Field, Input, Muted, PageHeader, Panel } from "./ui.tsx";

export function DesktopAccessGate({ state, brand, children }: { state: HostState; brand: ReactNode; children: ReactNode }) {
  const api = useApi();
  const key = ["desktop-access", state.target.id, state.target.url, state.accountEpoch];
  const access = useQuery({ queryKey: [...key, "status"], queryFn: api.accessStatus, retry: false });
  const me = useQuery({
    queryKey: [...key, "me"],
    queryFn: api.me,
    enabled: access.data?.mode === "claimed",
    retry: false,
    staleTime: Infinity,
  });
  const signingIn = me.error instanceof ApiError && me.error.status === 401;
  if (me.data !== undefined && me.error === null) return children;

  const pending = access.data?.mode === "setup_pending";
  const url = access.data?.canonicalUrl;
  const addressMatches = state.target.kind === "local" || url === state.target.url;
  const error = access.error ?? (signingIn ? null : me.error);
  const title = pending ? "Waiting for server setup" : signingIn ? "Sign in to Stage" : error === null ? "Checking account access" : "Couldn’t check account access";
  return (
    <main className="min-h-dvh bg-bg text-fg">
      <div className="mx-auto flex w-full max-w-[560px] flex-col gap-3.5 px-4 pt-[max(2.5rem,14vh)] pb-12">
        {brand}
        <div className="mt-6" aria-live="polite">
          <PageHeader title={title} meta={state.target.name} />
        </div>
        {url === undefined ? null : <p className="break-all font-mono text-meta text-muted">{url}</p>}
        {signingIn && addressMatches ? <DesktopSignIn /> : null}
        {pending ? <Muted>Ask the server owner to finish setup using their link in a browser.</Muted> : null}
        <ErrorText error={error} />
        {error !== null || pending ? (
          <div className="flex flex-wrap gap-2">
            <Button busy={access.isFetching || me.isFetching} onClick={() => { void access.refetch(); if (access.data?.mode === "claimed") void me.refetch(); }}>Retry</Button>
            {url !== undefined && (pending || !addressMatches) ? <ExternalButton href={url}>Open in browser</ExternalButton> : null}
          </div>
        ) : null}
      </div>
    </main>
  );
}

function DesktopSignIn() {
  const api = useApi();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy) return;
    const form = event.currentTarget;
    const data = new FormData(form);
    const email = data.get("email");
    const password = data.get("password");
    if (typeof email !== "string" || typeof password !== "string") return;
    setBusy(true);
    setError(null);
    try {
      await api.desktopSignIn(email, password);
      form.reset();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Panel>
      <form className="flex flex-col gap-3" onSubmit={(event) => void submit(event)}>
        <Field label="Email">{(id) => <Input id={id} name="email" type="email" autoComplete="username" required autoFocus disabled={busy} />}</Field>
        <Field label="Password">{(id) => <Input id={id} name="password" type="password" autoComplete="current-password" required disabled={busy} />}</Field>
        <ErrorText error={error} />
        <div><Button type="submit" variant="primary" busy={busy}>Sign in</Button></div>
      </form>
    </Panel>
  );
}
