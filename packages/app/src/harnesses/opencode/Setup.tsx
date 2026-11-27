// OpenCode connection card (Settings): one-click Connect to the service on this
// machine, a pairing link or address for another machine, sign-in recovery,
// Disconnect, and the Advanced drawer. Logic lives in opencode.ts.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createContext, useContext, useState, type FormEvent } from "react";
import { invalidateConnectionChange, useHarnesses } from "../../api/queries.ts";
import type { HarnessStatus, ReplacedConnection } from "../../api/types.ts";
import { activeOn } from "../../lib/harnesses.ts";
import { usePlatform } from "../../platform/context.tsx";
import { useHarnessApi, useScopeMachineId, useScopedKey, useSetupCopy } from "../../api/harnessScope.tsx";
import { AutoCompleteToggle } from "../../components/Completion.tsx";
import { NewTaskDefaults } from "../../components/NewTaskDefaults.tsx";
import { CheckConnection, ConfirmConnectionAction, HarnessFacts, HarnessFix, HarnessStatusChip, ReplacedConnectionNotice } from "../../components/HarnessPresentation.tsx";
import { Alert, Button, ConfirmInline, Drawer, ErrorText, Field, Hint, Input, Muted, Panel, QueryState } from "../../components/ui.tsx";
import { OPENCODE_KIND, openCodeApi, openCodeDetails } from "./api.ts";
import {
  advancedChanges,
  isAuthRequired,
  isInstanceChanged,
  isInstanceReturned,
  localNote,
  newServicePrompt,
  parseTarget,
  returnedServicePrompt,
  serviceHost,
  targetReadBack,
} from "./opencode.ts";
import { OPENCODE_STATUS_KEY, useOpenCodeStatus } from "./queries.ts";
import type { OpenCodeStatus, OpenCodeStatusWire } from "./types.ts";

const ReplacementContext = createContext<(replaced: ReplacedConnection | null) => void>(() => {});

/** Connect, pair and disconnect change the status, connections and what tasks can start: waits for the status, the rest refreshes behind. */
function useRefresh(): (reply?: HarnessStatus) => Promise<void> {
  const qc = useQueryClient();
  const key = useScopedKey();
  const onReplaced = useContext(ReplacementContext);
  return (reply) => {
    if (reply !== undefined) onReplaced(reply.replaced ?? null);
    return invalidateConnectionChange(qc, key, [OPENCODE_STATUS_KEY]);
  };
}

/**
 * A connect that may answer 409 instance_changed (a different service) or
 * instance_returned (a replaced one): once confirmed, the same request is
 * resent with newInstance. `confirming` stays true while that resend runs, and
 * `returned` is kept from the conflict, so the confirm's copy does not flip.
 */
function useConnect<T>(run: (arg: T, newInstance: boolean) => Promise<HarnessStatus>, onSuccess: (reply: HarnessStatus) => Promise<void>, onError?: (err: unknown, arg: T) => void) {
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
  const current = activeOn(useHarnesses().list, OPENCODE_KIND, useScopeMachineId());
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
  const [replaced, setReplaced] = useState<ReplacedConnection | null>(null);
  const api = useHarnessApi();
  const key = useScopedKey()(OPENCODE_STATUS_KEY);
  const qc = useQueryClient();
  const check = async () => {
    const checked = await api.harnessStatus<OpenCodeStatusWire>(OPENCODE_KIND, { refresh: true });
    qc.setQueryData(key, openCodeDetails(checked));
  };
  const presentation = info?.presentation;
  return (
    <ReplacementContext.Provider value={setReplaced}>
      <Panel title="OpenCode connection" actions={presentation === undefined ? undefined : <HarnessStatusChip status={presentation} />}>
        <QueryState isPending={status.isPending} error={info === undefined ? status.error : null} hasData={info !== undefined}>
          {info === undefined ? null : (
            <div className="flex min-w-0 flex-col gap-3">
              <ReplacedConnectionNotice replaced={replaced} />
              <HarnessFix fix={info.state === "unconfigured" ? undefined : presentation?.fix} reason={presentation?.reason} />
              <HarnessFacts facts={info.state === "unconfigured" && info.local.available
                ? [{ id: "localService", label: "Local service", value: info.local.url, copyable: true }, ...(presentation?.facts ?? [])]
                : presentation?.facts} />
              {info.state === "unconfigured" ? (
                <>
                  <NotConnected info={info} />
                  <CheckConnection run={check} />
                </>
              ) : <Connection info={info} check={check} advanced={() => setEditing(true)} />}
              <AutoCompleteToggle harness={OPENCODE_KIND} className="w-full border-t border-line pt-2" />
            </div>
          )}
        </QueryState>
        {info === undefined && status.error !== null ? <CheckConnection run={check} /> : null}
        {editing && info ? <ConnectionEditor info={info} close={() => setEditing(false)} /> : null}
      </Panel>
      <NewTaskDefaults harness={OPENCODE_KIND} />
    </ReplacementContext.Provider>
  );
}

/** A1/A2: Connect to the local service when one answers, then a pairing link or address for another machine. */
function NotConnected({ info }: { info: OpenCodeStatus }) {
  const api = openCodeApi(useHarnessApi());
  const copy = useSetupCopy();
  const refresh = useRefresh();
  const connect = useConnect((_: null, newInstance: boolean) => api.connectLocal(newInstance), refresh);
  const note = localNote(info);
  if (info.local.available) {
    return (
      <div className="flex flex-col gap-3">
        <p className="text-small font-medium text-fg">OpenCode is running on {copy.machine}</p>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <Button variant="primary" busy={connect.mutation.isPending && !connect.confirming} disabled={connect.confirming} onClick={() => connect.mutation.mutate({ arg: null, newInstance: false })}>
            Connect local service
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
      <HarnessFix fix={info.presentation?.fix ?? { text: "On the machine running OpenCode, generate a pairing link. Links work once and expire in 5 minutes.", command: "opencode pair" }} />
      <TargetForm primary />
    </div>
  );
}

/**
 * The pairing-link-or-address field. A link pairs; an address connects and, when
 * OpenCode asks for a password, reveals one. `linkOnly` takes links alone (pair again).
 */
function TargetForm({ primary, linkOnly = false, pairLabel = "Pair with link" }: { primary: boolean; linkOnly?: boolean; pairLabel?: string }) {
  const api = openCodeApi(useHarnessApi());
  const refresh = useRefresh();
  const [text, setText] = useState("");
  const [password, setPassword] = useState("");
  // The address OpenCode asked a password for; editing the field away from it hides the password.
  const [askedFor, setAskedFor] = useState<string | null>(null);
  const value = text.trim();
  const parsed = parseTarget(value);
  const target = linkOnly && parsed?.kind !== "link" ? null : parsed;
  const done = async (reply: HarnessStatus) => {
    setText("");
    setPassword("");
    setAskedFor(null);
    await refresh(reply);
  };
  // A link is resent as is after a confirm: Stage kept its redeemed token (links work once).
  const pair = useConnect((link: string, newInstance: boolean) => api.pair(link, newInstance), done);
  const connect = useConnect((endpoint: string, newInstance: boolean) => api.save({ endpoint, ...(newInstance ? { newInstance: true } : {}) }), done, (e, endpoint) => {
    if (isAuthRequired(e)) setAskedFor(endpoint);
  });
  const signIn = useConnect(
    (credentials: { endpoint: string; password: string }, newInstance: boolean) => api.save({ ...credentials, username: "opencode", ...(newInstance ? { newInstance: true } : {}) }),
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
    if (target === null || asking || confirming || pair.mutation.isPending || connect.mutation.isPending) return;
    if (target.kind === "link") {
      pair.mutation.mutate({ arg: value, newInstance: false });
      setText("");
    } else connect.mutation.mutate({ arg: value, newInstance: false });
  };
  const submitPassword = (e: FormEvent) => {
    e.preventDefault();
    if (!asking || password === "" || signIn.confirming || signIn.mutation.isPending) return;
    signIn.mutation.mutate({ arg: { endpoint: value, password }, newInstance: false });
    setPassword("");
  };
  const lastLink = pair.mutation.variables?.arg;
  const pairedService = lastLink === undefined ? value : (parseTarget(lastLink)?.service ?? value);
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
      <ConnectOutcome address={pairedService} connect={pair} confirm={() => {
        if (lastLink !== undefined) pair.mutation.mutate({ arg: lastLink, newInstance: true });
      }} />
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
          <ConnectOutcome address={value} connect={signIn} confirm={() => {
              const arg = signIn.mutation.variables?.arg;
              if (arg !== undefined) signIn.mutation.mutate({ arg, newInstance: true });
            }} />
        </form>
      ) : isAuthRequired(connect.mutation.error) ? null : (
        <ConnectOutcome address={value} connect={connect} confirm={() => connect.mutation.mutate({ arg: value, newInstance: true })} />
      )}
    </div>
  );
}

/** A configured connection, sign-in recovery, and its actions. */
function Connection({ info, check, advanced }: { info: OpenCodeStatus; check: () => Promise<unknown>; advanced: () => void }) {
  const browser = info.browserEndpoint || info.endpoint;
  const disconnect = useDisconnectOpenCode();
  return (
    <div className="flex min-w-0 flex-col gap-3">
      {info.state === "rejected" ? <SignInLost info={info} advanced={advanced} /> : null}
      {info.webClient && info.webClient !== browser ? (
        <BrowserSetupHint key={info.webClient + "|" + browser} client={info.webClient} service={browser} />
      ) : null}
      <ConfirmConnectionAction action={{
        prompt: OPENCODE_DISCONNECT_PROMPT,
        busy: disconnect.isPending,
        error: disconnect.error,
        run: (done) => disconnect.mutate(undefined, { onSuccess: done }),
        reset: disconnect.reset,
      }}>
        <CheckConnection run={check} />
        {info.state === "rejected" && info.auth === "basic" ? null : <Button small onClick={advanced}>Advanced</Button>}
      </ConfirmConnectionAction>
    </div>
  );
}

/** A4: OpenCode answered but no longer accepts Stage's sign-in; the fix depends on how Stage signed in. */
function SignInLost({ info, advanced }: { info: OpenCodeStatus; advanced: () => void }) {
  const api = openCodeApi(useHarnessApi());
  const refresh = useRefresh();
  const reconnect = useConnect((_: null, newInstance: boolean) => api.connectLocal(newInstance), refresh);
  const host = <span className="font-mono break-all">{serviceHost(info.endpoint)}</span>;
  switch (info.auth) {
    case "local":
      return (
        <div className="flex flex-col gap-2">
          <Alert
            tone="need"
            actions={
              <Button small busy={reconnect.mutation.isPending && !reconnect.confirming} disabled={reconnect.confirming} onClick={() => reconnect.mutation.mutate({ arg: null, newInstance: false })}>
                Connect local service
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
          <TargetForm primary linkOnly />
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

export const OPENCODE_DISCONNECT_PROMPT = "Disconnect OpenCode? Tasks and history stay. You can connect again any time. It stays listed as disconnected until you remove it.";

/** Disconnects OpenCode on the harness scope's machine, then waits for the new status. */
export function useDisconnectOpenCode(onDone?: () => void) {
  const api = openCodeApi(useHarnessApi());
  const refresh = useRefresh();
  return useMutation({
    mutationFn: api.disconnect,
    onSuccess: async (reply) => {
      onDone?.();
      await refresh(reply);
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
  const api = openCodeApi(useHarnessApi());
  const refresh = useRefresh();
  const [urls, setUrls] = useState({
    endpoint: info.endpoint,
    browserEndpoint: info.browserEndpoint,
    webClient: info.webClient,
  });
  const [replace, setReplace] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const current = activeOn(useHarnesses().list, OPENCODE_KIND, useScopeMachineId());
  // Which confirm (a replaced service returning, or a different one): kept while its resend runs, so the copy does not flip.
  const [returned, setReturned] = useState(false);
  // Compared with the status as the editor opened, not the live poll: an untouched field never reverts a change made elsewhere.
  const [opened] = useState(info);
  const body = advancedChanges(opened, urls, replace ? { username, password } : null);
  // A different service answers 409 instance_changed (a replaced one instance_returned): the same body is resent with newInstance once confirmed.
  const save = useMutation({
    mutationFn: (newInstance: boolean) => api.save(newInstance ? { ...body, newInstance: true } : body),
    onSuccess: async (reply) => {
      await refresh(reply);
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
