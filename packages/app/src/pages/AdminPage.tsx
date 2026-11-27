import { useInfiniteQuery, useMutation, useQueries, useQueryClient } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import type { Account, Invitation } from "../api/types.ts";
import { EmailField, FormProblem, formText, useFormAction } from "../components/access/AccessScreens.tsx";
import { OneTimeSecret, type IssuedSecret } from "../components/access/OneTimeSecret.tsx";
import { useSharedAccount } from "../components/access/SessionContext.ts";
import { Alert, Button, Chip, ConfirmInline, EmptyState, Meta, Muted, PageHeader, Panel, QueryState, Row, RowList, TabNav, type ChipTone } from "../components/ui.tsx";
import { dateTime } from "../lib/format.ts";
import { useApi } from "../platform/context.tsx";
import { NotFoundPage } from "./NotFoundPage.tsx";

const PEOPLE = ["admin", "people"];
const INVITATIONS = ["admin", "invitations"];
const PAGE_SIZE = 30;
const ADMIN_TABS = [
  { to: "/admin/people", label: "People" },
  { to: "/admin/invitations", label: "Invitations" },
];

function AdminPage({ title, children }: { title: string; children: ReactNode }) {
  const shared = useSharedAccount();
  if (shared === null) return <NotFoundPage />;
  if (shared.account.role !== "admin") {
    return (
      <>
        <PageHeader title="Access forbidden" />
        <Alert tone="bad">You need to be a Stage admin to open this page.</Alert>
      </>
    );
  }
  return (
    <>
      <PageHeader title="Admin" name={`${title} · Admin`} />
      <TabNav label="Admin" items={ADMIN_TABS} className="mb-4" />
      <div className="flex max-w-[880px] flex-col gap-3.5">{children}</div>
    </>
  );
}

function nextPage(rows: readonly { id: number }[]): number | undefined {
  return rows.length === PAGE_SIZE ? rows.at(-1)?.id : undefined;
}

function LoadMore({ more, pending, load }: { more: boolean; pending: boolean; load: () => void }) {
  return more ? (
    <div className="border-t border-line p-3">
      <Button busy={pending} onClick={load}>Load more</Button>
    </div>
  ) : null;
}

type PersonChange = "promote" | "demote" | "disable" | "reset";

const PERSON_CHANGE = {
  promote: { label: "Make admin", description: "They’ll be able to manage people, roles, and invitations." },
  demote: { label: "Make member", description: "They’ll keep access to shared work and lose access to Admin." },
  disable: { label: "Disable access", description: "Their sessions and agent credential will be revoked immediately." },
  reset: { label: "Create reset link", description: "Share the new link privately with this member. Any earlier reset link will stop working." },
};

function PersonRow({ person, lastAdmin, current, onReset }: {
  person: Account;
  lastAdmin: boolean;
  current: boolean;
  onReset: (secret: IssuedSecret) => void;
}) {
  const api = useApi();
  const qc = useQueryClient();
  const shared = useSharedAccount();
  const [pending, setPending] = useState<PersonChange | null>(null);
  const change = useMutation({
    mutationFn: async (action: PersonChange) => {
      if (action === "promote") await api.setAccountRole(person.id, "admin");
      else if (action === "demote") await api.setAccountRole(person.id, "member");
      else if (action === "disable") await api.disableAccount(person.id);
      else {
        const link = await api.issueResetLink(person.id);
        onReset({ value: link.url, expiresAt: link.expiresAt, label: "Reset link" });
      }
      setPending(null);
      if (current && action === "disable") shared?.sessionChanged();
      else if (current) shared?.refresh();
      await qc.invalidateQueries({ queryKey: PEOPLE });
    },
  });
  const roleAction = person.role === "admin" ? "demote" : "promote";
  function ask(action: PersonChange) {
    change.reset();
    setPending(action);
  }
  return (
    <Row className="flex flex-col gap-2">
      <div className="flex flex-col gap-2 md:flex-row md:items-start md:justify-between">
        <div className="min-w-0 flex-1">
          <p className="text-small font-medium [overflow-wrap:anywhere]">{person.displayName}{current ? <span className="text-muted"> (you)</span> : null}</p>
          <p className="text-small text-muted [overflow-wrap:anywhere]">{person.email ?? "No email"}</p>
          <div className="mt-1 flex flex-wrap items-center gap-2">
            <Meta>{person.role === "admin" ? "Admin" : "Member"}</Meta>
            <Chip tone={person.status === "active" ? "ok" : "idle"}>{person.status === "active" ? "Active" : "Disabled"}</Chip>
          </div>
        </div>
        {person.status === "active" && pending === null && !lastAdmin ? (
          <div className="flex flex-wrap gap-2 md:justify-end">
            <Button small onClick={() => ask(roleAction)}>{PERSON_CHANGE[roleAction].label}</Button>
            {person.role === "member" ? <Button small onClick={() => ask("reset")}>Create reset link</Button> : null}
            <Button small onClick={() => ask("disable")}>Disable access</Button>
          </div>
        ) : null}
        {lastAdmin ? <Meta>Last active admin</Meta> : null}
      </div>
      {pending === null ? null : (
        <ConfirmInline confirmLabel={PERSON_CHANGE[pending].label} onConfirm={() => change.mutate(pending)}
          onCancel={() => setPending(null)} danger={pending === "disable"} busy={change.isPending} stackActionsOnPhone>
          <span className="[overflow-wrap:anywhere]">{person.email ?? person.displayName}. {PERSON_CHANGE[pending].description}</span>
        </ConfirmInline>
      )}
      <FormProblem error={change.error} />
    </Row>
  );
}

function People() {
  const api = useApi();
  const shared = useSharedAccount();
  const [secret, setSecret] = useState<IssuedSecret | null>(null);
  const accounts = useInfiniteQuery({
    queryKey: PEOPLE,
    initialPageParam: 0,
    queryFn: ({ pageParam }) => api.accounts(PAGE_SIZE, pageParam),
    getNextPageParam: nextPage,
  });
  const people = accounts.data?.pages.flat() ?? [];
  const activeAdmins = people.filter((person) => person.role === "admin" && person.status === "active");
  const lastAdmin = accounts.hasNextPage ? undefined : activeAdmins.length === 1 ? activeAdmins[0]?.id : undefined;
  return (
    <>
      <OneTimeSecret secret={secret} onClear={() => setSecret(null)}
        hint="Share this single-use link privately with the member." />
      <Panel title="People" flush>
        <QueryState isPending={accounts.isPending} error={accounts.error} hasData={accounts.data !== undefined}>
          {people.length === 0 ? <EmptyState inset>No accounts yet.</EmptyState> : (
            <RowList>
              {people.map((person) => <PersonRow key={person.id} person={person} lastAdmin={lastAdmin === person.id}
                current={shared?.account.id === person.id} onReset={setSecret} />)}
            </RowList>
          )}
        </QueryState>
        <LoadMore more={accounts.hasNextPage} pending={accounts.isFetchingNextPage} load={() => void accounts.fetchNextPage()} />
        {accounts.isLoadingError ? <div className="p-3"><Button onClick={() => void accounts.refetch()}>Try again</Button></div> : null}
      </Panel>
    </>
  );
}

const INVITE_STATUS: Record<Invitation["status"], { label: string; tone: ChipTone }> = {
  pending: { label: "Pending", tone: "idle" },
  used: { label: "Used", tone: "ok" },
  revoked: { label: "Revoked", tone: "idle" },
  expired: { label: "Expired", tone: "idle" },
};

function InviteRow({ invite, inviter, onReplace }: { invite: Invitation; inviter: string; onReplace: (secret: IssuedSecret) => void }) {
  const api = useApi();
  const qc = useQueryClient();
  const [pending, setPending] = useState<"replace" | "revoke" | null>(null);
  const change = useMutation({
    mutationFn: async (action: "replace" | "revoke") => {
      if (action === "replace") {
        const link = await api.replaceInvite(invite.id);
        onReplace({ value: link.url, expiresAt: link.expiresAt, label: "Invitation link" });
      } else await api.revokeInvite(invite.id);
      setPending(null);
      await qc.invalidateQueries({ queryKey: INVITATIONS });
    },
  });
  const { tone, label } = INVITE_STATUS[invite.status];
  const [dateLabel, date] = invite.consumedAt !== null ? ["Used", invite.consumedAt]
    : invite.revokedAt !== null ? ["Revoked", invite.revokedAt] : ["Expires", invite.expiresAt];

  function ask(action: "replace" | "revoke") {
    change.reset();
    setPending(action);
  }
  return (
    <Row className="flex flex-col gap-2">
      <div className="flex flex-col gap-2 md:flex-row md:items-start md:justify-between">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <p className="min-w-0 text-small font-medium [overflow-wrap:anywhere]">{invite.email ?? "Unknown recipient"}</p>
            <Chip tone={tone}>{label}</Chip>
          </div>
          <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 [overflow-wrap:anywhere]">
            <Meta>Invited {dateTime(invite.createdAt)} by {inviter}</Meta>
            <Meta>{dateLabel} {dateTime(date)}</Meta>
          </div>
        </div>
        {pending === null && (invite.status === "pending" || invite.status === "expired") ? (
          <div className="flex flex-wrap gap-2 md:justify-end">
            <Button small onClick={() => ask("replace")}>Replace link</Button>
            {invite.status === "pending" ? <Button small onClick={() => ask("revoke")}>Revoke link</Button> : null}
          </div>
        ) : null}
      </div>
      {pending === null ? null : (
        <ConfirmInline confirmLabel={pending === "replace" ? "Replace link" : "Revoke link"} onConfirm={() => change.mutate(pending)}
          onCancel={() => setPending(null)} danger={pending === "revoke"} busy={change.isPending} stackActionsOnPhone>
          The previous link for <span className="[overflow-wrap:anywhere]">{invite.email}</span> will stop working.
        </ConfirmInline>
      )}
      <FormProblem error={change.error} />
    </Row>
  );
}

function Invitations() {
  const api = useApi();
  const qc = useQueryClient();
  const [secret, setSecret] = useState<IssuedSecret | null>(null);
  const invites = useInfiniteQuery({
    queryKey: INVITATIONS,
    initialPageParam: 0,
    queryFn: ({ pageParam }) => api.invitations(PAGE_SIZE, pageParam),
    getNextPageParam: nextPage,
    refetchInterval: 60_000,
  });
  const rows = invites.data?.pages.flat() ?? [];
  const issuerIds = [...new Set(rows.flatMap(({ issuerUserId }) => issuerUserId === null ? [] : [issuerUserId]))];
  const issuers = useQueries({
    queries: issuerIds.map((id) => ({
      queryKey: [...PEOPLE, id],
      queryFn: async () => (await api.accounts(1, id + 1)).find((account) => account.id === id) ?? null,
    })),
  });
  const names = new Map(issuerIds.map((id, i) => [id, issuers[i]?.data?.displayName ?? `User #${id}`]));
  const form = useFormAction(async (fields) => {
    const link = await api.invite(formText(fields, "email"));
    setSecret({ value: link.url, expiresAt: link.expiresAt, label: "Invitation link" });
    await qc.invalidateQueries({ queryKey: INVITATIONS });
  });
  return (
    <>
      <Panel title="Invite someone">
        <div className="flex flex-col gap-3">
          <Muted>Create a single-use link to send in a private message or email.</Muted>
          <form onSubmit={form.submit} aria-busy={form.pending} className="flex max-w-md flex-col gap-3">
            <EmailField recipient />
            <FormProblem error={form.error} />
            <div><Button type="submit" variant="primary" busy={form.pending}>Create invitation</Button></div>
          </form>
          <OneTimeSecret secret={secret} onClear={() => setSecret(null)} hint="Copy and share this link with the recipient." />
        </div>
      </Panel>
      <Panel title="Invitations" flush>
        <QueryState isPending={invites.isPending} error={invites.error} hasData={invites.data !== undefined}>
          {rows.length === 0 ? <EmptyState inset>No invitations yet.</EmptyState> : (
            <RowList>
              {rows.map((invite) => <InviteRow key={invite.id} invite={invite}
                inviter={invite.issuerUserId === null ? "installation owner" : names.get(invite.issuerUserId) ?? `User #${invite.issuerUserId}`}
                onReplace={setSecret} />)}
            </RowList>
          )}
        </QueryState>
        <LoadMore more={invites.hasNextPage} pending={invites.isFetchingNextPage} load={() => void invites.fetchNextPage()} />
        {invites.isLoadingError ? <div className="p-3"><Button onClick={() => void invites.refetch()}>Try again</Button></div> : null}
      </Panel>
    </>
  );
}

export function AdminPeoplePage() {
  return <AdminPage title="People"><People /></AdminPage>;
}

export function AdminInvitationsPage() {
  return <AdminPage title="Invitations"><Invitations /></AdminPage>;
}
