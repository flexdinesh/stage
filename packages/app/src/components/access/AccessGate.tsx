import { QueryClientProvider, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { Navigate, Outlet, useLocation, useNavigate } from "react-router";
import { grantKind, LOGIN_PATH, returnToWork } from "../../lib/accessPaths.ts";
import { CHECKING, observeAccess, type Access } from "../../lib/accessState.ts";
import { useHostState } from "../../lib/hostState.ts";
import { hostAccessScope } from "../../lib/host.ts";
import { PlatformProvider, useApi, usePlatform } from "../../platform/context.tsx";
import { Button, SkeletonRows } from "../ui.tsx";
import { AccessFrame, FormProblem, GrantScreen, LoginScreen } from "./AccessScreens.tsx";
import { SharedAccountContext } from "./SessionContext.ts";

function useAccess() {
  const api = useApi();
  const platform = usePlatform();
  const client = useQueryClient();
  const [view, setView] = useState<Access>(CHECKING);
  const observer = useRef<ReturnType<typeof observeAccess> | null>(null);

  useEffect(() => {
    const current = observeAccess(api, platform, (next, previous) => {
      if (previous.kind === "personal" && next.kind !== "personal") client.clear();
      setView(next);
    });
    observer.current = current;
    return () => current.close();
  }, [api, client, platform]);

  return { view, observer };
}

function CheckingAccess() {
  const [late, setLate] = useState(false);
  useEffect(() => {
    const timer = setTimeout(() => setLate(true), 250);
    return () => clearTimeout(timer);
  }, []);
  return late
    ? <AccessFrame title="Opening Stage"><SkeletonRows /></AccessFrame>
    : <div aria-busy="true" className="min-h-dvh bg-bg" />;
}

export function AccessGate() {
  const host = useHostState();
  return <ServerAccess key={hostAccessScope(host)} />;
}

function ServerAccess() {
  const { view, observer } = useAccess();
  const { sessionLifecycle } = usePlatform();
  const location = useLocation();
  const navigate = useNavigate();
  const grant = grantKind(location.pathname);
  const onLogin = location.pathname.toLowerCase().replace(/\/+$/, "") === LOGIN_PATH;
  const [passwordReset, setPasswordReset] = useState(false);
  const sessionChanged = () => {
    void observer.current?.check(true);
    sessionLifecycle?.changed();
  };
  const complete = (destination: string, stillHere: boolean) => {
    if (stillHere) {
      setPasswordReset(destination === LOGIN_PATH);
      void navigate(destination, { replace: true });
    }
    sessionChanged();
  };

  if (view.kind === "checking") return <CheckingAccess />;
  if (view.kind === "error") {
    return (
      <AccessFrame title="Stage is unavailable">
        <FormProblem error={view.error} />
        <Button onClick={() => void observer.current?.check(true)}>Try again</Button>
      </AccessFrame>
    );
  }
  if (view.kind === "personal") return grant !== null || onLogin ? <Navigate to="/" replace /> : <Outlet />;
  if (grant !== null) {
    return <GrantScreen key={grant} kind={grant} onComplete={(stillHere) => complete(grant === "reset" ? LOGIN_PATH : "/", stillHere)} />;
  }
  if (view.kind === "setup_pending") {
    return (
      <AccessFrame title="Waiting for the owner">
        <p className="text-body text-muted">The server owner must create the first admin account before you can sign in.</p>
        <div><Button onClick={() => void observer.current?.check(true)}>Check again</Button></div>
      </AccessFrame>
    );
  }
  if (view.kind === "signed_out") {
    return <LoginScreen key={location.key} passwordReset={onLogin && passwordReset}
      onComplete={(stillHere) => complete(returnToWork(location), stillHere)} />;
  }
  if (view.kind !== "signed_in") return null;
  if (onLogin) return <Navigate to="/" replace />;

  return (
    <SharedAccountContext.Provider value={{
      account: view.account,
      refresh: () => void observer.current?.check(),
      sessionChanged,
    }}>
      <PlatformProvider key={view.generation} platform={view.session.platform}>
        <QueryClientProvider client={view.session.client}>
          <Outlet />
        </QueryClientProvider>
      </PlatformProvider>
    </SharedAccountContext.Provider>
  );
}
