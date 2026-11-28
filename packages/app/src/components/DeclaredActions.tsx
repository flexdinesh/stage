import { useMutation } from "@tanstack/react-query";
import { useId, useRef, useState, type FormEvent } from "react";
import type { ActionResult, HarnessAction, HarnessStatus, SetupField } from "../api/types.ts";
import { actionBody, clearSecrets, fieldInput, initialValues, type SetupValues } from "../lib/connectionSetup.ts";
import { HarnessFacts } from "./HarnessPresentation.tsx";
import { Button, Checkbox, Combobox, ConfirmInline, ErrorText, ExternalLink, Field, Hint, Input, cx } from "./ui.tsx";

const NO_FIELDS: SetupField[] = [];

export function SetupInputs({ fields, status, values, disabled = false, nativeRequired = true, onChange }: {
  fields: readonly SetupField[];
  status?: HarnessStatus;
  values: SetupValues;
  disabled?: boolean;
  /** False inside another form (the task form's inline add): required stays aria-only, so it never blocks that form. */
  nativeRequired?: boolean;
  onChange: (id: string, value: string | boolean) => void;
}) {
  return fields.map((field) => (
    <SetupInput key={field.id} field={field} value={values[field.id]} disabled={disabled} nativeRequired={nativeRequired}
      savedSecret={status?.fields.some((current) => current.id === field.id && current.set) === true}
      onChange={(value) => onChange(field.id, value)} />
  ));
}

function SetupInput({ field, value, savedSecret, disabled, nativeRequired, onChange }: {
  field: SetupField;
  value: string | boolean | undefined;
  savedSecret: boolean;
  disabled: boolean;
  nativeRequired: boolean;
  onChange: (value: string | boolean) => void;
}) {
  const hintId = useId();
  const description = field.help ? hintId : undefined;
  const text = typeof value === "string" ? value : "";
  const hint = field.help ? <span id={hintId}>{field.help}</span> : undefined;
  const input = fieldInput(field);
  if (field.kind === "bool") {
    return (
      <div className="flex flex-col gap-1">
        <label className="flex min-h-control-sm items-center gap-2 text-ui">
          <Checkbox disabled={disabled} checked={value === true} aria-describedby={description} onChange={(e) => onChange(e.target.checked)} />
          {field.label}
        </label>
        {hint === undefined ? null : <Hint>{hint}</Hint>}
      </div>
    );
  }
  return (
    <Field label={field.label} hint={hint}>
      {(id) => field.kind === "choice" ? (
        <Combobox id={id} listLabel={field.label} disabled={disabled} value={text}
          onChange={onChange}
          options={[
            { value: "", label: "Select…" },
            ...(text && !field.choices?.some((choice) => choice.id === text) ? [{ value: text, label: text }] : []),
            ...(field.choices ?? []).map((choice) => ({ value: choice.id, label: choice.label })),
          ]} />
      ) : (
        <Input id={id} disabled={disabled} required={nativeRequired && field.required && !savedSecret}
          aria-required={!nativeRequired && field.required && !savedSecret ? true : undefined} aria-describedby={description}
          className={input.mono ? "font-mono text-input-mono" : undefined}
          type={input.type}
          value={text} placeholder={field.kind === "secret" && savedSecret ? "Saved" : undefined}
          autoComplete={field.kind === "secret" ? "new-password" : "off"} spellCheck={false}
          onChange={(e) => onChange(e.target.value)} />
      )}
    </Field>
  );
}

type RunAction = (id: string, input: Record<string, unknown>, confirmed: boolean) => Promise<ActionResult>;

/** Shared connection, session and project action forms. The caller supplies the scoped dispatch. */
export function DeclaredActions({ actions, run, className }: { actions: readonly HarnessAction[]; run: RunAction; className?: string }) {
  const visible = actions.filter((action) => action.callers.includes("user"));
  if (visible.length === 0) return null;
  return (
    <div className={cx("flex min-w-0 flex-col divide-y divide-line", className)}>
      {visible.map((action) => <DeclaredActionForm key={action.id} action={action} run={run} />)}
    </div>
  );
}

function DeclaredActionForm({ action, run }: { action: HarnessAction; run: RunAction }) {
  const fields = action.input ?? NO_FIELDS;
  const [values, setValues] = useState<SetupValues>(() => initialValues(fields, []));
  const [confirm, setConfirm] = useState(false);
  const inFlight = useRef(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const reasonId = useId();
  const reason = !action.availability.available ? action.availability.reason || "This action is unavailable." : undefined;
  const missing = fields.some((field) => field.required && field.kind !== "bool" && String(values[field.id] ?? "").trim() === "");
  const mustConfirm = action.destructive || Boolean(action.confirm);
  const mutation = useMutation({
    // Capture the submitted body in this closure so secrets do not become mutation variables.
    mutationFn: (dispatch: () => Promise<ActionResult>) => dispatch(),
    onSuccess: () => setValues(initialValues(fields, [])),
    onSettled: () => { inFlight.current = false; },
  });
  const dispatch = (confirmed: boolean) => {
    if (inFlight.current || reason !== undefined || (mustConfirm && !confirmed)) return;
    const input = actionBody(fields, values);
    inFlight.current = true;
    setConfirm(false);
    setValues((current) => clearSecrets(fields, current));
    mutation.mutate(() => {
      const pending = run(action.id, input, confirmed);
      for (const field of fields) if (field.kind === "secret") delete input[field.id];
      return pending;
    });
    if (confirmed) trigger.current?.focus();
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (reason !== undefined || missing || inFlight.current || confirm) return;
    if (mustConfirm) {
      mutation.reset();
      setConfirm(true);
    } else dispatch(false);
  };
  const result = mutation.data;
  return (
    <form onSubmit={submit} className="flex min-w-0 flex-col gap-2 py-2 first:pt-0 last:pb-0">
      {reason === undefined ? <SetupInputs fields={fields} values={values} disabled={mutation.isPending || confirm}
        onChange={(id, value) => setValues((current) => ({ ...current, [id]: value }))} /> : null}
      <div className="flex flex-wrap items-center gap-2">
        <Button ref={trigger} small type="submit" variant={action.primary ? "primary" : "secondary"}
          busy={mutation.isPending} disabled={reason !== undefined || missing} aria-describedby={reason === undefined ? undefined : reasonId}>
          {action.label}
        </Button>
        {reason === undefined ? null : <Hint className="wrap-anywhere"><span id={reasonId}>{reason}</span></Hint>}
      </div>
      {confirm ? (
        <ConfirmInline danger={action.destructive} confirmLabel={action.label}
          onConfirm={() => dispatch(true)} onCancel={() => { setConfirm(false); trigger.current?.focus(); }}>
          {action.confirm || `Run ${action.label}?`}
        </ConfirmInline>
      ) : null}
      <ErrorText error={mutation.error} />
      <div role="status" className="empty:hidden">
        {result?.message ? <Hint className="wrap-anywhere">{result.message}</Hint> : null}
        {result?.link ? <ExternalLink href={result.link}>Open result</ExternalLink> : null}
      </div>
      <HarnessFacts facts={result?.facts} />
      {result?.journalError ? <ErrorText error={result.journalError} /> : null}
    </form>
  );
}
