import type { Capabilities, ExecutionConfig, HarnessChoice, HarnessOption } from "../api/types.ts";
import { hasOverrides, optionAccepts, optionApplies, optionKind, optionLabel, optionValueLabel, optionsFor, setOption as withOption, shownOptions } from "../lib/options.ts";
import { Alert, Button, Field, FieldGroup, Combobox, Input, Segmented, Skeleton, SkeletonBar, errorText } from "./ui.tsx";

export { incompatibleSettings } from "../lib/options.ts";

export const harnessDefaults: ExecutionConfig = {
  model: "",
  options: [],
  mode: "",
  permission: "",
};

export function HarnessFields({
  config,
  caps,
  onChange,
  disabled,
  modelOnly = false,
  title,
  loading = false,
  error,
  onRetry,
  allowReset = false,
}: {
  config: ExecutionConfig;
  caps: Capabilities | undefined;
  onChange: (next: ExecutionConfig) => void;
  disabled: boolean;
  /** Model and its options only (a connection's new-task pin: mode and permission start at the harness default). */
  modelOnly?: boolean;
  /** Wraps the fields and load state in a ruled group under this label ("Harness · T3"). */
  title?: string;
  loading?: boolean;
  error?: unknown;
  onRetry?: () => void;
  /** Offers to clear all overrides in the task or schedule form. */
  allowReset?: boolean;
}) {
  const model = caps?.models?.find((m) => m.id === config.model);
  const options = optionsFor(caps, model);
  const choices = (
    label: string,
    field: "model" | "permission" | "mode",
    list: HarnessChoice[],
  ) => {
    const current = config[field];
    if (!list.length && !current) return null;
    const unavailable = list.find((c) => c.id === current && !c.available);
    return (
      <Field key={`choice:${field}`} label={label} {...(unavailable?.reason ? { hint: unavailable.reason } : {})}>
        {(id) => (
          <Combobox
            id={id}
            disabled={disabled}
            value={current}
            onChange={(next) => onChange({ ...config, [field]: next })}
            listLabel={label}
            options={[
              { value: "", label: "Harness default" },
              ...(current && !list.some((c) => c.id === current && c.available)
                ? [{ value: current, label: `${current} (unavailable)`, disabled: true }]
                : []),
              ...list
                .filter((c) => c.available)
                .map((c) => ({
                  value: c.id,
                  label: c.label || c.id,
                  ...("group" in c && typeof c.group === "string" ? { tag: c.group } : {}),
                })),
            ]}
          />
        )}
      </Field>
    );
  };
  const setOption = (key: string, value: string | boolean | undefined) =>
    onChange({ ...config, options: withOption(config.options, options, key, value) });
  // The harness's order: model, its launch options (each by kind, shown when its dependsOn holds), permission, mode.
  const stray = config.options.filter((o) => !options.some((v) => v.id === o.id && optionApplies(v, config.options, options)));
  const failure = errorText(error);
  const fields = loading || (caps === undefined && failure !== null) ? [] : [
    choices("Model", "model", caps?.models ?? []),
    ...shownOptions(options, config.options).map((o) => (
      <OptionField key={`option:${o.id}`} option={o} config={config} disabled={disabled} onChange={(v) => setOption(o.id, v)} />
    )),
    ...stray.map((o) => (
      <div key={`stray:${o.id}`} className="text-small">
        {o.id} = {optionValueLabel(undefined, o.value)}{" "}
        <Button small disabled={disabled} onClick={() => setOption(o.id, undefined)}>
          Clear option
        </Button>
      </div>
    )),
    modelOnly ? null : choices("Permission", "permission", model?.permissions ?? caps?.permissions ?? []),
    modelOnly ? null : choices(caps?.modeLabel || "Interaction mode", "mode", caps?.modes ?? []),
  ].filter((f) => f !== null);
  const grid = "grid min-w-0 grid-cols-1 gap-3 md:grid-cols-2";
  if (title === undefined) return <div className={grid}>{fields}</div>;
  const overridden = hasOverrides(config);
  if (fields.length === 0 && !loading && failure === null && !(allowReset && overridden)) return null;
  return (
    <section className="border-t border-line pt-3">
      <FieldGroup label={title}>
        {failure === null ? null : (
          <Alert tone="bad" actions={onRetry === undefined ? undefined : <Button small onClick={onRetry}>Retry</Button>}>
            <span className="break-words">Could not load harness settings. {failure}</span>
          </Alert>
        )}
        {loading ? (
          <Skeleton label="Loading harness settings" className={grid}>
            <div aria-hidden className="flex flex-col gap-1.5">
              <SkeletonBar className="h-3 w-16" />
              <SkeletonBar className="h-control w-full" />
            </div>
            <div aria-hidden className="flex flex-col gap-1.5">
              <SkeletonBar className="h-3 w-24" />
              <SkeletonBar className="h-control w-full" />
            </div>
          </Skeleton>
        ) : fields.length > 0 ? <div className={grid}>{fields}</div> : null}
        {allowReset && (overridden || fields.length > 0) ? (
          <Button small variant="ghost" className="-ml-2 self-start" disabled={disabled || !overridden} onClick={() => onChange(harnessDefaults)}>
            Reset to harness defaults
          </Button>
        ) : null}
      </FieldGroup>
    </section>
  );
}

/** One launch option, by kind: a choice in a combobox, a three-state boolean, or a text input. */
function OptionField({
  option: o,
  config,
  disabled,
  onChange,
}: {
  option: HarnessOption;
  config: ExecutionConfig;
  disabled: boolean;
  onChange: (value: string | boolean | undefined) => void;
}) {
  const label = optionLabel(o);
  const current = config.options.find((v) => v.id === o.id);
  switch (optionKind(o)) {
    case "boolean": {
      const selected = current === undefined ? "default" : current.value === true ? "on" : current.value === false ? "off" : "unavailable";
      return (
        <FieldGroup label={label} start>
          <Segmented
            label={label}
            value={selected}
            onChange={(next) => onChange(next === "default" ? undefined : next === "on")}
            options={[
              { value: "default", label: "Harness default", disabled },
              { value: "on", label: optionValueLabel(o, true), disabled: disabled || !optionAccepts(o, true) },
              { value: "off", label: optionValueLabel(o, false), disabled: disabled || !optionAccepts(o, false) },
            ]}
          />
        </FieldGroup>
      );
    }
    case "text":
      return (
        <Field label={label}>
          {(id) => (
            <Input
              id={id}
              disabled={disabled}
              value={typeof current?.value === "string" ? current.value : ""}
              placeholder={typeof o.default === "string" ? o.default : "Harness default"}
              onChange={(e) => onChange(e.target.value)}
            />
          )}
        </Field>
      );
    case "choice": {
      const idx = o.values.findIndex((v) => v === current?.value);
      return (
        <Field label={label}>
          {(id) => (
            <Combobox
              id={id}
              disabled={disabled}
              value={current && idx < 0 ? "unavailable" : String(idx)}
              onChange={(next) => onChange(o.values[Number(next)])}
              listLabel={label}
              options={[
                { value: "-1", label: "Harness default" },
                ...(current && idx < 0
                  ? [{ value: "unavailable", label: `${optionValueLabel(o, current.value)} (unavailable)`, disabled: true }]
                  : []),
                ...o.values.map((v, i) => ({ value: String(i), label: optionValueLabel(o, v) })),
              ]}
            />
          )}
        </Field>
      );
    }
  }
}
