import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { ApiError } from "../../api/client.ts";
import type { Account } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";
import { Button, ConfirmInline, Meta, Muted, Panel } from "../ui.tsx";
import { EmailField, FormProblem, NameField, PasswordField, formText, useFormAction } from "./AccessScreens.tsx";
import { OneTimeSecret, type IssuedSecret } from "./OneTimeSecret.tsx";
import { useSharedAccount } from "./SessionContext.ts";

function Profile({ account, onSave }: { account: Account; onSave: () => void }) {
  const api = useApi();
  const [saved, setSaved] = useState(false);
  const form = useFormAction(async (fields) => {
    setSaved(false);
    await api.updateName(formText(fields, "name"));
    setSaved(true);
    onSave();
  });
  return (
    <Panel title="Profile" meta={account.role === "admin" ? "Admin" : "Member"}>
      <form onSubmit={form.submit} aria-busy={form.pending} className="flex max-w-sm flex-col gap-3">
        <p className="text-body [overflow-wrap:anywhere]">{account.email}</p>
        <NameField name={account.displayName} />
        <FormProblem error={form.error} />
        <div className="flex flex-wrap items-center gap-2">
          <Button type="submit" variant="primary" busy={form.pending}>Save name</Button>
          {saved ? <span role="status"><Meta>Name saved.</Meta></span> : null}
        </div>
      </form>
    </Panel>
  );
}

function Security({ account, sessionChanged }: { account: Account; sessionChanged: () => void }) {
  const api = useApi();
  const form = useFormAction(async (fields) => {
    await api.changePassword(formText(fields, "currentPassword"), formText(fields, "password"));
    sessionChanged();
  });
  const logout = useMutation({
    mutationFn: async () => {
      try {
        await api.logout();
      } catch (error) {
        if (!(error instanceof ApiError && error.code === "authentication_required")) throw error;
      }
      sessionChanged();
    },
  });
  return (
    <Panel title="Sign-in and password">
      <div className="flex max-w-sm flex-col gap-4">
        <form onSubmit={form.submit} aria-busy={form.pending} className="flex flex-col gap-3">
          {account.email === null ? null : <EmailField email={account.email} />}
          <PasswordField label="Current password" name="currentPassword" />
          <PasswordField label="New password" fresh />
          <Muted>Changing your password signs you out on every device.</Muted>
          <FormProblem error={form.error} />
          <div><Button type="submit" busy={form.pending} disabled={logout.isPending}>Change password</Button></div>
        </form>
        <div className="flex flex-col items-start gap-2 border-t border-line pt-3">
          <Button busy={logout.isPending} disabled={form.pending} onClick={() => logout.mutate()}>Sign out</Button>
          <FormProblem error={logout.error} />
        </div>
      </div>
    </Panel>
  );
}

function AgentCredentials() {
  const api = useApi();
  const [secret, setSecret] = useState<IssuedSecret | null>(null);
  const [confirmed, setConfirmed] = useState<"issue" | "revoke" | null>(null);
  const change = useMutation({
    mutationFn: async (action: "issue" | "revoke") => {
      if (action === "issue") {
        const result = await api.issueAgentCredential();
        setSecret({ value: result.token, expiresAt: result.expiresAt, label: "Agent credential" });
      } else {
        await api.revokeAgentCredential();
        setSecret(null);
      }
      setConfirmed(null);
    },
  });
  function ask(action: "issue" | "revoke") {
    change.reset();
    setConfirmed(action);
  }
  return (
    <Panel title="Agent credential">
      <div className="flex flex-col gap-3">
        <Muted>Use this credential for Stage MCP and agent API requests on this server.</Muted>
        <OneTimeSecret secret={secret} onClear={() => setSecret(null)} hint="Copy this credential now. Stage shows it once." />
        {secret !== null ? null : confirmed === null ? (
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => ask("issue")}>Issue credential</Button>
            <Button onClick={() => ask("revoke")}>Revoke credential</Button>
          </div>
        ) : (
          <ConfirmInline confirmLabel={confirmed === "issue" ? "Issue credential" : "Revoke credential"}
            onConfirm={() => change.mutate(confirmed)} onCancel={() => setConfirmed(null)}
            busy={change.isPending} danger={confirmed === "revoke"} stackActionsOnPhone>
            {confirmed === "issue"
              ? "Issuing a credential replaces any existing agent credential for your account."
              : "Agents using this credential will lose access to this server."}
          </ConfirmInline>
        )}
        <FormProblem error={change.error} />
        {change.isSuccess && change.variables === "revoke" ? <p role="status" className="text-small">Credential revoked.</p> : null}
      </div>
    </Panel>
  );
}

export function AccountSettings() {
  const shared = useSharedAccount();
  if (shared === null) return null;
  return (
    <div className="flex max-w-[720px] flex-col gap-3.5">
      <Profile account={shared.account} onSave={shared.refresh} />
      <Security account={shared.account} sessionChanged={shared.sessionChanged} />
      <AgentCredentials />
    </div>
  );
}
