import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { OpenCodeStatus } from "../api/types.ts";
import { useHarnesses, useOpenCodeStatus } from "../api/queries.ts";
import { isInstanceChanged, isInstanceReturned, newServicePrompt, returnedServicePrompt } from "../lib/harnesses.ts";
import { PreviousConnections } from "./PreviousConnections.tsx";
import { useApi, usePlatform } from "../platform/context.tsx";
import { dateTime } from "../lib/format.ts";
import { AutoCompleteToggle } from "./Completion.tsx";
import { Alert, Button, Chip, Drawer, ErrorText, Field, Input, Panel } from "./ui.tsx";

export function OpenCodeSetup() {
  const status = useOpenCodeStatus();
  const [editing, setEditing] = useState(false);
  const info = status.data;
  return (
    <Panel
      title="OpenCode connection"
      actions={
        info ? (
          <Chip
            tone={info.state === "connected" ? "ok" : info.state === "unavailable" ? "bad" : "idle"}
            dot
          >
            {info.state}
          </Chip>
        ) : undefined
      }
    >
      <div className="flex flex-col items-start gap-2">
        {info?.endpoint ? <code className="break-all text-meta">{info.endpoint}</code> : null}
        {info?.reason ? <p className="text-small">{info.reason}</p> : null}
        {info?.checkedAt ? (
          <p className="text-meta text-muted">Checked {dateTime(info.checkedAt)}</p>
        ) : null}
        {info?.webClient && info.webClient !== (info.browserEndpoint || info.endpoint) ? (
          <BrowserSetupHint
            key={info.webClient + "|" + (info.browserEndpoint || info.endpoint)}
            client={info.webClient}
            service={info.browserEndpoint || info.endpoint}
          />
        ) : null}
        <ErrorText error={status.error} />
        <div className="flex flex-wrap gap-2">
          <Button small disabled={!info} onClick={() => setEditing(true)}>
            {info?.endpoint ? "Edit connection" : "Connect OpenCode"}
          </Button>
          <Button
            small
            variant="ghost"
            busy={status.isFetching}
            onClick={() => void status.refetch()}
          >
            Check connection
          </Button>
        </div>
        <AutoCompleteToggle harness="opencode" className="mt-1 w-full border-t border-line pt-2" />
        <PreviousConnections harness="opencode" className="mt-1 w-full border-t border-line pt-3" />
      </div>
      {editing && info ? <ConnectionEditor info={info} close={() => setEditing(false)} /> : null}
    </Panel>
  );
}

function BrowserSetupHint({ client, service }: { client: string; service: string }) {
  const { storage } = usePlatform();
  const key = "opencode-registration:" + JSON.stringify([client, service]);
  const [hidden, setHidden] = useState(() => storage.get(key) === "seen");
  if (hidden) return null;
  return (
    <div className="text-small text-muted">
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
    </div>
  );
}
function ConnectionEditor({ info, close }: { info: OpenCodeStatus; close: () => void }) {
  const api = useApi();
  const qc = useQueryClient();
  const [urls, setUrls] = useState({
    endpoint: info.endpoint,
    browserEndpoint: info.browserEndpoint,
    webClient: info.webClient,
  });
  const [replace, setReplace] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const locked = (key: string) => info.environmentFields.includes(key);
  const current = useHarnesses().activeConnection("opencode");
  // Which confirm (a replaced service returning, or a different one): kept while its resend runs, so the copy does not flip.
  const [returned, setReturned] = useState(false);
  // A different service answers 409 instance_changed (a replaced one instance_returned): the same body is resent with newInstance once confirmed.
  const save = useMutation({
    mutationFn: (newInstance: boolean) =>
      api.saveOpenCode({
        ...(!locked("endpoint") ? { endpoint: urls.endpoint } : {}),
        ...(!locked("browserEndpoint") ? { browserEndpoint: urls.browserEndpoint } : {}),
        ...(!locked("webClient") ? { webClient: urls.webClient } : {}),
        ...(replace ? { username, password } : {}),
        ...(newInstance ? { newInstance: true } : {}),
      }),
    onSuccess: async () => {
      await qc.invalidateQueries();
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
      <Field label={label} hint={locked(key) ? "Managed by the Stage server environment." : hint}>
        {(id) => (
          <Input
            id={id}
            type="url"
            value={urls[key]}
            disabled={locked(key)}
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
      title="Connect OpenCode"
      onClose={close}
      footer={
        <>
          <Button onClick={close}>Cancel</Button>
          <Button
            variant="primary"
            busy={save.isPending && save.variables === false}
            disabled={!urls.endpoint.trim() || changed || save.isPending}
            onClick={() => save.mutate(false)}
          >
            Test and save
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3">
        {address(
          "endpoint",
          "Service URL",
          "Address of the existing OpenCode V2 service as reached by Stage.",
        )}
        {address(
          "browserEndpoint",
          "Browser service URL",
          "Leave empty to use the service URL. Use an address reachable from your phone.",
        )}
        {address(
          "webClient",
          "Web client URL",
          "Leave empty to open the web client hosted by the service.",
        )}
        {urls.webClient && urls.webClient !== (urls.browserEndpoint || urls.endpoint) ? (
          <p className="text-small text-muted">
            Add the browser service URL to this web client once on each device. Task detail also
            offers Open on service.
          </p>
        ) : null}
        {locked("credentials") ? (
          <p className="text-small">Credentials are managed by the Stage server environment.</p>
        ) : (
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
        )}
        {changed ? (
          <Alert tone="bad">
            <p>{returned ? returnedServicePrompt(urls.endpoint.trim(), current) : newServicePrompt(urls.endpoint.trim(), current)}</p>
            <div className="mt-2 flex flex-wrap gap-2">
              <Button small variant="primary" busy={save.isPending} onClick={() => save.mutate(true)}>
                {returned ? "Connect again" : "Connect as new service"}
              </Button>
              <Button small variant="ghost" autoFocus disabled={save.isPending} onClick={() => save.reset()}>
                Cancel
              </Button>
            </div>
          </Alert>
        ) : (
          <ErrorText error={save.error} />
        )}
      </div>
    </Drawer>
  );
}
