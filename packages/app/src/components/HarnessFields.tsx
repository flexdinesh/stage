import type { Capabilities, ExecutionConfig, HarnessChoice } from "../api/types.ts";
import { optionsFor } from "../lib/capabilities.ts";
import { Button, Field, Combobox } from "./ui.tsx";

export const harnessDefaults: ExecutionConfig = {
  model: "",
  options: [],
  mode: "",
  permission: "",
};

export function incompatibleSettings(
  config: ExecutionConfig,
  caps: Capabilities | undefined,
): string[] {
  if (!caps) return [];
  const model = caps.models?.find((m) => m.id === config.model && m.available);
  const issues: string[] = [];
  if (config.model && !model) issues.push("model");
  if (config.mode && !caps.modes?.some((m) => m.id === config.mode && m.available))
    issues.push("mode");
  if (
    config.permission &&
    !(model?.permissions ?? caps.permissions)?.some(
      (p) => p.id === config.permission && p.available,
    )
  )
    issues.push("permission");
  const allowed = optionsFor(caps, model);
  for (const o of config.options) {
    if (!allowed.some((v) => v.id === o.id && v.values.includes(o.value)))
      issues.push(o.id);
  }
  return issues;
}

export function HarnessFields({
  config,
  caps,
  onChange,
  disabled,
  modelOnly = false,
}: {
  config: ExecutionConfig;
  caps: Capabilities | undefined;
  onChange: (next: ExecutionConfig) => void;
  disabled: boolean;
  /** Model and its options only (a connection's new-task pin: mode and permission start at the harness default). */
  modelOnly?: boolean;
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
      <Field label={label} {...(unavailable?.reason ? { hint: unavailable.reason } : {})}>
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
    onChange({
      ...config,
      options: [
        ...config.options.filter((o) => o.id !== key),
        ...(value === undefined ? [] : [{ id: key, value }]),
      ],
    });
  return (
    <div className="grid min-w-0 grid-cols-1 gap-3 md:grid-cols-2">
      {choices("Model", "model", caps?.models ?? [])}
      {modelOnly ? null : choices(caps?.modeLabel || "Interaction mode", "mode", caps?.modes ?? [])}
      {modelOnly ? null : choices("Permission", "permission", model?.permissions ?? caps?.permissions ?? [])}
      {options.map((o) => {
        const current = config.options.find((v) => v.id === o.id);
        const idx = o.values.findIndex((v) => v === current?.value);
        return (
          <Field key={o.id} label={o.id}>
            {(id) => (
              <Combobox
                id={id}
                disabled={disabled}
                value={current && idx < 0 ? "unavailable" : String(idx)}
                onChange={(next) => setOption(o.id, o.values[Number(next)])}
                listLabel={o.id}
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
      })}
      {config.options
        .filter((o) => !options.some((v) => v.id === o.id))
        .map((o) => (
          <div key={o.id} className="text-small">
            {o.id} = {String(o.value)}{" "}
            <Button small disabled={disabled} onClick={() => setOption(o.id, undefined)}>
              Clear option
            </Button>
          </div>
        ))}
    </div>
  );
}
