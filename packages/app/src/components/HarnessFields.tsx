import type { Capabilities, ExecutionConfig, HarnessChoice, HarnessOption } from "../api/types.ts";
import { optionsFor } from "../lib/capabilities.ts";
import { optionApplies, optionKind, optionLabel, optionValue, setOption as withOption, shownOptions } from "../lib/options.ts";
import { Button, Field, FieldGroup, Combobox, Input, Segmented } from "./ui.tsx";

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
}: {
  config: ExecutionConfig;
  caps: Capabilities | undefined;
  onChange: (next: ExecutionConfig) => void;
  disabled: boolean;
  /** Model and its options only (a connection's new-task pin: mode and permission start at the harness default). */
  modelOnly?: boolean;
  /** Wraps the fields in a ruled group under this label ("Harness · T3"); nothing at all when no field renders. */
  title?: string;
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
  const fields = [
    choices("Model", "model", caps?.models ?? []),
    ...shownOptions(options, config.options).map((o) => (
      <OptionField key={`option:${o.id}`} option={o} config={config} disabled={disabled} onChange={(v) => setOption(o.id, v)} />
    )),
    ...stray.map((o) => (
      <div key={`stray:${o.id}`} className="text-small">
        {o.id} = {String(o.value)}{" "}
        <Button small disabled={disabled} onClick={() => setOption(o.id, undefined)}>
          Clear option
        </Button>
      </div>
    )),
    modelOnly ? null : choices("Permission", "permission", model?.permissions ?? caps?.permissions ?? []),
    modelOnly ? null : choices(caps?.modeLabel || "Interaction mode", "mode", caps?.modes ?? []),
  ].filter((f) => f !== null);
  if (fields.length === 0 && title !== undefined) return null;
  const grid = <div className="grid min-w-0 grid-cols-1 gap-3 md:grid-cols-2">{fields}</div>;
  if (title === undefined) return grid;
  return (
    <section className="border-t border-line pt-3">
      <FieldGroup label={title}>{grid}</FieldGroup>
    </section>
  );
}

/** One launch option, by kind: a choice in a combobox, a boolean as on/off, a text as an input (empty: unset). */
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
      const on = optionValue(o, config.options) === true;
      return (
        <FieldGroup label={label} start>
          <Segmented
            label={label}
            value={on ? "on" : "off"}
            onChange={(next) => onChange(next === "on")}
            options={[
              { value: "off", label: "Off", disabled },
              { value: "on", label: "On", disabled },
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
                  ? [{ value: "unavailable", label: `${String(current.value)} (unavailable)`, disabled: true }]
                  : []),
                ...o.values.map((v, i) => ({ value: String(i), label: String(v) })),
              ]}
            />
          )}
        </Field>
      );
    }
  }
}
