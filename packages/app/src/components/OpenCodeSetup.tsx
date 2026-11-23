// OpenCode connection card (Settings): one-click Connect to the service on this
// machine, a pairing link or address for another machine, sign-in recovery,
// Disconnect, and the Advanced drawer. Logic lives in lib/opencode.ts.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent, type ReactNode } from "react";
import type { OpenCodeStatus } from "../api/types.ts";
import { invalidateConnectionChange, keys, useHarnesses, useOpenCodeStatus } from "../api/queries.ts";
import { activeOn, isInstanceChanged, isInstanceReturned, newServicePrompt, returnedServicePrompt } from "../lib/harnesses.ts";
import { usePlatform } from "../platform/context.tsx";
import { useHarnessApi, useScopeMachineId, useScopedKey, useSetupCopy } from "../api/harnessScope.tsx";
import { dateTime } from "../lib/format.ts";
import { advancedChanges, isAuthRequired, localNote, openCodeChip, parseTarget, serviceHost, serviceText, signInText, targetReadBack } from "../lib/opencode.ts";
import { AutoCompleteToggle } from "./Completion.tsx";
import { Alert, Button, ConfirmInline, Chip, Drawer, ErrorText, Field, Hint, InlineError, Input, Muted, Panel, CommandBox } from "./ui.tsx";

/** Connect, pair and disconnect change the status, connections and what tasks can start: waits for the status, the rest refreshes behind. */
function useRefresh(): () => Promise<void> {
  const qc = useQueryClient();
  const key = useScopedKey();
  return () => invalidateConnectionChange(qc, key, [keys.openCodeStatus]);
}

/**
 * A connect that may answer 409 instance_changed (a different service) or
 * instance_returned (a replaced one): once confirmed, the same request is
 * resent with newInstance. `confirming` stays true while that resend runs, and
 * `returned` is kept from the conflict, so the confirm's copy does not flip.
 */
function useConnect<T>(run: (arg: T, newInstance: boolean) => Promise<void>, onSuccess: () => Promise<void>, onError?: (err: unknown, arg: T) => void) {
  const [returned, setReturned] = useState(false);
  const mutation = useMutation({
    mutationFn: (v: { arg: T; newInstance: boolean }) => run(v.arg, v.newInstance),
    onSuccess,
    onError: (err, v) => {
      if (isInstanceChanged(err)) setReturned(isInstanceReturned(err));
      onError?.(err, v.arg);
    },
  });
  const confirming = isInstanceChanged(mutation.error) || (mutation.isPending && mutation.variables?.newInstance === true);
  return { mutation, returned, confirming };
}

/** The confirm for a different (or earlier) OpenCode service, or the request's error. */
function ConnectOutcome({ address, connect, confirm }: { address: string; connect: { mutation: { error: unknown; isPending: boolean; reset: () => void }; returned: boolean; confirming: boolean }; confirm: () => void }) {
  const current = activeOn(useHarnesses().list, "opencode", useScopeMachineId());
  const { mutation, returned, confirming } = connect;
  if (!confirming) return <ErrorText error={mutation.error} />;
  return (
    <ConfirmInline confirmLabel={returned ? "Connect again" : "Connect as new service"} busy={mutation.isPending} onConfirm={confirm} onCancel={() => mutation.reset()}>
      {returned ? returnedServicePrompt(address, current) : newServicePrompt(address, current)}
    </ConfirmInline>
  );
}

export function OpenCodeSetup() {
  const status = useOpenCodeStatus();
  const [editing, setEditing] = useState(false);
  const info = status.data;
  const chip = info === undefined ? null : openCodeChip(info);
  return (
    <Panel
      title="OpenCode connection"
      actions={
        chip === null ? undefined : (
          <Chip tone={chip.tone} dot>
            {chip.label}
          </Chip>
        )
      }
    >
      <div className="flex flex-col items-stretch gap-3">
        <ErrorText error={status.error} />
        {info === undefined ? null : info.state === "unconfigured" ? (
          <NotConnected info={info} />
        ) : (
          <Connection info={info} checking={status.isFetching} check={() => void status.refetch()} advanced={() => setEditing(true)} />
        )}
        <AutoCompleteToggle harness="opencode" className="w-full border-t border-line pt-2" />
      </div>
      {editing && info ? <ConnectionEditor info={info} close={() => setEditing(false)} /> : null}
    </Panel>
  );
}

/** A1/A2: Connect to the local service when one answers, then a pairing link or address for another machine. */
function NotConnected({ info }: { info: OpenCodeStatus }) {
  const api = useHarnessApi();
  const copy = useSetupCopy();
  const refresh = useRefresh();
  const connect = useConnect((_: null, newInstance: boolean) => api.connectLocalOpenCode(newInstance), refresh);
  const note = localNote(info);
  if (info.local.available) {
    return (
      <div className="flex flex-col gap-3">
        <p className="text-small font-medium text-fg">OpenCode is running on {copy.machine}</p>
        <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 font-mono text-detail">
          <dt className="text-muted">service</dt>
          <dd className="min-w-0 break-all">{info.local.url}</dd>
          {info.local.version === "" ? null : (
            <>
              <dt className="text-muted">version</dt>
              <dd className="min-w-0 break-all">{info.local.version}</dd>
            </>
          )}
        </dl>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <Button variant="primary" busy={connect.mutation.isPending && !connect.confirming} disabled={connect.confirming} onClick={() => connect.mutation.mutate({ arg: null, newInstance: false })}>
            Connect
          </Button>
          <Hint>signs in with the service's own password</Hint>
        </div>
        <ConnectOutcome address={info.local.url} connect={connect} confirm={() => connect.mutation.mutate({ arg: null, newInstance: true })} />
        <div className="label-caps flex items-center gap-2.5 before:flex-1 before:border-t before:border-line after:flex-1 after:border-t after:border-line">
          or another machine
        </div>
        <TargetForm primary={false} />
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      {note === null ? null : <Muted>{note}</Muted>}
      <Muted>On the machine running OpenCode, run this and paste one link. Links work once and expire in 5 minutes.</Muted>
      <CommandBox command="opencode pair" />
      <TargetForm primary />
    </div>
  );
}

/**
 * The pairing-link-or-address field. A link pairs; an address connects and, when
 * OpenCode asks for a password, reveals one. `linkOnly` takes links alone (pair again).
 */
function TargetForm({ primary, linkOnly = false, pairLabel = "Pair" }: { primary: boolean; linkOnly?: boolean; pairLabel?: string }) {
  const api = useHarnessApi();
  const refresh = useRefresh();
  const [text, setText] = useState("");
  const [password, setPassword] = useState("");
  // The address OpenCode asked a password for; editing the field away from it hides the password.
  const [askedFor, setAskedFor] = useState<string | null>(null);
  const value = text.trim();
  const parsed = parseTarget(value);
  const target = linkOnly && parsed?.kind !== "link" ? null : parsed;
  const done = async () => {
    setText("");
    setPassword("");
    setAskedFor(null);
    await refresh();
  };
  // A link is resent as is after a confirm: Stage kept its redeemed token (links work once).
  const pair = useConnect((link: string, newInstance: boolean) => api.pairOpenCode(link, newInstance), done);
  const connect = useConnect((endpoint: string, newInstance: boolean) => api.saveOpenCode({ endpoint, ...(newInstance ? { newInstance: true } : {}) }), done, (e, endpoint) => {
    if (isAuthRequired(e)) setAskedFor(endpoint);
  });
  const signIn = useConnect(
    (endpoint: string, newInstance: boolean) => api.saveOpenCode({ endpoint, username: "opencode", password, ...(newInstance ? { newInstance: true } : {}) }),
    done,
  );
  const asking = askedFor !== null && askedFor === value && target?.kind === "address";
  const confirming = pair.confirming || connect.confirming || signIn.confirming;
  const edit = (next: string) => {
    setText(next);
    for (const c of [pair, connect, signIn]) if (c.confirming) c.mutation.reset();
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (target === null || asking || confirming) return;
    if (target.kind === "link") pair.mutation.mutate({ arg: value, newInstance: false });
    else connect.mutation.mutate({ arg: value, newInstance: false });
  };
  const submitPassword = (e: FormEvent) => {
    e.preventDefault();
    if (asking && password !== "" && !signIn.confirming) signIn.mutation.mutate({ arg: value, newInstance: false });
  };
  const address = target === null ? value : target.service;
  const readBack = targetReadBack(target);
  return (
    <div className="flex flex-col gap-2">
      <form onSubmit={submit} noValidate className="flex flex-col gap-2">
        <div className="flex flex-wrap items-end gap-2">
          <Field label={linkOnly ? "Pairing link" : "Pairing link or address"} className="min-w-52 flex-1">
            {(id) => (
              <Input
                id={id}
                type="url"
                inputMode="url"
                value={text}
                onChange={(e) => edit(e.target.value)}
                placeholder="paste the link from opencode pair"
                autoComplete="off"
                spellCheck={false}
                className="font-mono"
              />
            )}
          </Field>
          {asking ? null : (
            <Button
              type="submit"
              variant={primary ? "primary" : "secondary"}
              busy={(pair.mutation.isPending || connect.mutation.isPending) && !confirming}
              disabled={target === null || confirming}
            >
              {target?.kind === "address" ? "Connect" : pairLabel}
            </Button>
          )}
        </div>
        <p className="font-mono text-meta text-muted" aria-live="polite">
          {readBack}
        </p>
      </form>
      <ConnectOutcome address={address} connect={pair} confirm={() => pair.mutation.mutate({ arg: value, newInstance: true })} />
      {asking ? (
        <form onSubmit={submitPassword} className="flex flex-col gap-2">
          <Alert tone="need">
            This OpenCode needs a password. Paste a pairing link from <code className="font-mono">opencode pair</code>, or enter the service password.
          </Alert>
          <div className="flex flex-wrap items-end gap-2">
            <Field label="Password" className="min-w-52 flex-1">
              {(id) => (
                <Input id={id} type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
              )}
            </Field>
            <Button type="submit" variant="primary" busy={signIn.mutation.isPending && !signIn.confirming} disabled={password === "" || signIn.confirming}>
              Connect
            </Button>
          </div>
          <ConnectOutcome address={value} connect={signIn} confirm={() => signIn.mutation.mutate({ arg: value, newInstance: true })} />
        </form>
      ) : isAuthRequired(connect.mutation.error) ? null : (
        <ConnectOutcome address={value} connect={connect} confirm={() => connect.mutation.mutate({ arg: value, newInstance: true })} />
      )}
    </div>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="border-b border-line py-1.5 pr-4 font-mono text-meta text-muted">{label}</dt>
      <dd className="min-w-0 border-b border-line py-1.5 font-mono text-detail break-all">{children}</dd>
    </>
  );
}

/** A3/A4: a configured connection, its sign-in recovery when OpenCode refuses it, and its actions. */
function Connection({ info, checking, check, advanced }: { info: OpenCodeStatus; checking: boolean; check: () => void; advanced: () => void }) {
  const browser = info.browserEndpoint || info.endpoint;
  return (
    <div className="flex flex-col gap-3">
      {info.state === "rejected" ? <SignInLost info={info} advanced={advanced} /> : null}
      {info.state === "unavailable" && info.reason ? <InlineError>{info.reason}</InlineError> : null}
      <dl className="grid grid-cols-[max-content_1fr] border-t border-line">
        <Row label="Service">{info.endpoint === "" ? "-" : serviceText(info)}</Row>
        <Row label="Sign-in">{signInText(info)}</Row>
        <Row label="Checked">{info.checkedAt === undefined ? "-" : dateTime(info.checkedAt)}</Row>
      </dl>
      {info.webClient && info.webClient !== browser ? (
        <BrowserSetupHint key={info.webClient + "|" + browser} client={info.webClient} service={browser} />
      ) : null}
      <ConnectionActions checking={checking} check={check} advanced={advanced} />
    </div>
  );
}

/** A4: OpenCode answered but no longer accepts Stage's sign-in; the fix depends on how Stage signed in. */
function SignInLost({ info, advanced }: { info: OpenCodeStatus; advanced: () => void }) {
  const api = useHarnessApi();
  const refresh = useRefresh();
  const reconnect = useConnect((_: null, newInstance: boolean) => api.connectLocalOpenCode(newInstance), refresh);
  const host = <span className="font-mono break-all">{serviceHost(info.endpoint)}</span>;
  switch (info.auth) {
    case "local":
      return (
        <div className="flex flex-col gap-2">
          <Alert
            tone="need"
            actions={
              <Button small busy={reconnect.mutation.isPending && !reconnect.confirming} disabled={reconnect.confirming} onClick={() => reconnect.mutation.mutate({ arg: null, newInstance: false })}>
                Reconnect
              </Button>
            }
          >
            OpenCode changed its password.
          </Alert>
          <ConnectOutcome address={info.local.url || info.endpoint} connect={reconnect} confirm={() => reconnect.mutation.mutate({ arg: null, newInstance: true })} />
        </div>
      );
    case "basic":
      return (
        <Alert
          tone="need"
          actions={
            <Button small onClick={advanced}>
              Advanced
            </Button>
          }
        >
          OpenCode at {host} no longer accepts Stage's password. Update it in Advanced.
        </Alert>
      );
    case "paired":
      return (
        <div className="flex flex-col gap-3">
          <Alert tone="need">
            OpenCode at {host} no longer accepts Stage's session. Run <code className="font-mono">opencode pair</code> there and paste the new link. Tasks
            and history are kept.
          </Alert>
          <TargetForm primary linkOnly pairLabel="Pair again" />
        </div>
      );
    case "none":
      return (
        <div className="flex flex-col gap-3">
          <Alert tone="need">
            OpenCode at {host} now needs a password. Run <code className="font-mono">opencode pair</code> there and paste the link, or set the password in
            Advanced. Tasks and history are kept.
          </Alert>
          <TargetForm primary linkOnly />
        </div>
      );
  }
}

/** Check connection, Advanced, and Disconnect with an inline confirm. */
function ConnectionActions({ checking, check, advanced }: { checking: boolean; check: () => void; advanced: () => void }) {
  const [confirm, setConfirm] = useState(false);
  const disconnect = useDisconnectOpenCode(() => setConfirm(false));
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <Button small busy={checking} onClick={check}>
          Check connection
        </Button>
        <Button small onClick={advanced}>
          Advanced
        </Button>
        <Button small variant="danger" className="ml-auto" disabled={confirm} onClick={() => setConfirm(true)}>
          Disconnect
        </Button>
      </div>
      {confirm ? (
        <ConfirmInline danger confirmLabel="Disconnect" busy={disconnect.isPending} onConfirm={() => disconnect.mutate()} onCancel={() => setConfirm(false)}>
          {OPENCODE_DISCONNECT_PROMPT}
        </ConfirmInline>
      ) : null}
      <ErrorText error={disconnect.error} />
    </div>
  );
}

export const OPENCODE_DISCONNECT_PROMPT = "Disconnect OpenCode? Tasks and history stay. You can connect again any time.";

/** Disconnects OpenCode on the harness scope's machine, then waits for the new status. */
export function useDisconnectOpenCode(onDone?: () => void) {
  const api = useHarnessApi();
  const refresh = useRefresh();
  return useMutation({
    mutationFn: api.disconnectOpenCode,
    onSuccess: async () => {
      onDone?.();
      await refresh();
    },
  });
}

function BrowserSetupHint({ client, service }: { client: string; service: string }) {
  const { storage } = usePlatform();
  const key = "opencode-registration:" + JSON.stringify([client, service]);
  const [hidden, setHidden] = useState(() => storage.get(key) === "seen");
  if (hidden) return null;
  return (
    <Muted>
      On this device, add <code className="break-all">{service}</code> to your OpenCode web client
      once to open sessions there.
      <Button
        small
        variant="ghost"
        onClick={() => {
          storage.set(key, "seen");
          setHidden(true);
        }}
      >
        Got it
      </Button>
    </Muted>
  );
}

/** D: the rarely needed fields. Saves only what changed, so an untouched service URL keeps the sign-in mode. */
function ConnectionEditor({ info, close }: { info: OpenCodeStatus; close: () => void }) {
  const api = useHarnessApi();
  const refresh = useRefresh();
  const [urls, setUrls] = useState({
    endpoint: info.endpoint,
    browserEndpoint: info.browserEndpoint,
    webClient: info.webClient,
  });
  const [replace, setReplace] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const current = activeOn(useHarnesses().list, "opencode", useScopeMachineId());
  // Which confirm (a replaced service returning, or a different one): kept while its resend runs, so the copy does not flip.
  const [returned, setReturned] = useState(false);
  // Compared with the status as the editor opened, not the live poll: an untouched field never reverts a change made elsewhere.
  const [opened] = useState(info);
  const body = advancedChanges(opened, urls, replace ? { username, password } : null);
  // A different service answers 409 instance_changed (a replaced one instance_returned): the same body is resent with newInstance once confirmed.
  const save = useMutation({
    mutationFn: (newInstance: boolean) => api.saveOpenCode(newInstance ? { ...body, newInstance: true } : body),
    onSuccess: async () => {
      await refresh();
      close();
    },
    onError: (err) => {
      if (isInstanceChanged(err)) setReturned(isInstanceReturned(err));
    },
  });
  // The confirm stays up while its resend runs; editing the form drops it.
  const changed = isInstanceChanged(save.error) || (save.isPending && save.variables === true);
  function address(key: keyof typeof urls, label: string, hint: string) {
    return (
      <Field label={label} hint={hint}>
        {(id) => (
          <Input
            id={id}
            type="url"
            value={urls[key]}
            onChange={(e) => {
              setUrls({ ...urls, [key]: e.target.value });
              if (isInstanceChanged(save.error)) save.reset();
            }}
          />
        )}
      </Field>
    );
  }
  return (
    <Drawer
      title="OpenCode advanced"
      onClose={close}
      footer={
        <>
          <Button onClick={close}>Cancel</Button>
          <Button
            variant="primary"
            busy={save.isPending && save.variables === false}
            disabled={!urls.endpoint.trim() || Object.keys(body).length === 0 || changed || save.isPending}
            onClick={() => save.mutate(false)}
          >
            Test and save
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3">
        {address("endpoint", "Service URL", "How Stage reaches OpenCode. Set by Connect or Pair.")}
        {address(
          "browserEndpoint",
          "Address for other devices",
          "Leave empty to use the service URL. Use one your phone can reach, e.g. a LAN or Tailscale name.",
        )}
        {address(
          "webClient",
          "Web client URL",
          "Leave empty to open the web client hosted by the service.",
        )}
        {urls.webClient && urls.webClient !== (urls.browserEndpoint || urls.endpoint) ? (
          <Muted>Add the address for other devices to this web client once on each device.</Muted>
        ) : null}
        <div className="flex flex-col items-start gap-2">
          <Button small variant="ghost" onClick={() => setReplace(!replace)}>
            {replace
              ? "Keep current credentials"
              : info.hasCredentials
                ? "Replace or clear credentials"
                : "Add Basic credentials"}
          </Button>
          {replace ? (
            <>
              <Field label="Username">
                {(id) => (
                  <Input
                    id={id}
                    autoComplete="off"
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                  />
                )}
              </Field>
              <Field label="Password" hint="Leave both fields empty to clear stored credentials.">
                {(id) => (
                  <Input
                    id={id}
                    type="password"
                    autoComplete="new-password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                  />
                )}
              </Field>
            </>
          ) : null}
        </div>
        {changed ? (
          <ConfirmInline
            confirmLabel={returned ? "Connect again" : "Connect as new service"}
            busy={save.isPending}
            onConfirm={() => save.mutate(true)}
            onCancel={() => save.reset()}
          >
            {returned ? returnedServicePrompt(urls.endpoint.trim(), current) : newServicePrompt(urls.endpoint.trim(), current)}
          </ConfirmInline>
        ) : (
          <ErrorText error={save.error} />
        )}
      </div>
    </Drawer>
  );
}
