import type { Capabilities, ExecutionConfig, HarnessChoice } from "../api/types.ts";
import { Button, Field, Select } from "./ui.tsx";

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
  for (const o of config.options) {
    if (!model?.options?.some((v) => v.id === o.id && v.values.includes(o.value)))
      issues.push(o.id);
  }
  return issues;
}

export function HarnessFields({
  config,
  caps,
  onChange,
  openCode,
  disabled,
}: {
  config: ExecutionConfig;
  caps: Capabilities | undefined;
  onChange: (next: ExecutionConfig) => void;
  openCode: boolean;
  disabled: boolean;
}) {
  const model = caps?.models?.find((m) => m.id === config.model);
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
          <Select
            id={id}
            disabled={disabled}
            value={current}
            onChange={(e) => onChange({ ...config, [field]: e.target.value })}
          >
            <option value="">Harness default</option>
            {current && !list.some((c) => c.id === current && c.available) ? (
              <option value={current} disabled>
                {current} (unavailable)
              </option>
            ) : null}
            {list
              .filter((c) => c.available)
              .map((c) => (
                <option key={c.id} value={c.id}>
                  {"group" in c && typeof c.group === "string" ? c.group + " / " : ""}
                  {c.label || c.id}
                </option>
              ))}
          </Select>
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
      {choices(openCode ? "Agent" : "Interaction mode", "mode", caps?.modes ?? [])}
      {choices("Permission", "permission", model?.permissions ?? caps?.permissions ?? [])}
      {(model?.options ?? []).map((o) => {
        const current = config.options.find((v) => v.id === o.id);
        const idx = o.values.findIndex((v) => v === current?.value);
        return (
          <Field key={o.id} label={o.id}>
            {(id) => (
              <Select
                id={id}
                disabled={disabled}
                value={current && idx < 0 ? "unavailable" : String(idx)}
                onChange={(e) => setOption(o.id, o.values[Number(e.target.value)])}
              >
                <option value="-1">Harness default</option>
                {current && idx < 0 ? (
                  <option value="unavailable" disabled>
                    {String(current.value)} (unavailable)
                  </option>
                ) : null}
                {o.values.map((v, i) => (
                  <option key={i} value={i}>
                    {String(v)}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        );
      })}
      {config.options
        .filter((o) => !model?.options?.some((v) => v.id === o.id))
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
