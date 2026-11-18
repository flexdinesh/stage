import { useCatalog } from "../api/queries.ts";
import {
  RUNTIME_MODES,
  type CatalogModel,
  type CatalogProvider,
  type ModelOption,
  type RuntimeMode,
} from "../api/types.ts";
import { optionsText } from "../lib/format.ts";
import { Field, Muted, Segmented, Select, errorText } from "./ui.tsx";

export interface ModelConfig {
  providerInstanceId: string | null;
  model: string | null;
  modelOptions: ModelOption[];
  runtimeMode: RuntimeMode;
}

const RUNTIME_LABELS: Record<RuntimeMode, string> = {
  "approval-required": "approval",
  "auto-accept-edits": "auto-edits",
  auto: "auto",
  "full-access": "full-access",
};

const allowedModes = (p: CatalogProvider | undefined): readonly RuntimeMode[] => p?.runtimeModes ?? RUNTIME_MODES;

export function defaultOptions(m: CatalogModel | undefined): ModelOption[] {
  if (m === undefined) return [];
  return m.options.flatMap((o) => (o.default === null ? [] : [{ id: o.id, value: o.default }]));
}

/** Provider → model → per-model option selects → runtime mode, driven by GET /api/catalog. */
export function ModelFields({
  value,
  onChange,
  disabled = false,
}: {
  value: ModelConfig;
  onChange: (next: ModelConfig) => void;
  disabled?: boolean;
}) {
  const catalog = useCatalog();
  const providers = catalog.data?.providers ?? [];
  const provider = providers.find((p) => p.providerInstanceId === value.providerInstanceId);
  const model = provider?.models.find((m) => m.id === value.model);
  const modes = allowedModes(provider);

  const setProvider = (id: string) => {
    const next = providers.find((p) => p.providerInstanceId === id);
    const first = next?.models[0];
    onChange({
      providerInstanceId: id === "" ? null : id,
      model: first?.id ?? null,
      modelOptions: defaultOptions(first),
      runtimeMode: value.runtimeMode,
    });
  };

  const setModel = (id: string) => {
    const next = provider?.models.find((m) => m.id === id);
    onChange({ ...value, model: id === "" ? null : id, modelOptions: defaultOptions(next) });
  };

  const setOption = (optionId: string, v: string | boolean | undefined) => {
    const rest = value.modelOptions.filter((o) => o.id !== optionId);
    onChange({ ...value, modelOptions: v === undefined ? rest : [...rest, { id: optionId, value: v }] });
  };

  const catalogError = errorText(catalog.error);
  const modeList = modes.includes(value.runtimeMode) ? modes : [...modes, value.runtimeMode];

  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-3">
      {catalogError === null ? null : (
        <p className="col-span-full text-detail text-need-fg">
          Catalog unavailable ({catalogError}); model selection limited to current values.
        </p>
      )}
      <Field label="Provider">
        {(id) => (
          <Select id={id} disabled={disabled} value={value.providerInstanceId ?? ""} onChange={(e) => setProvider(e.target.value)}>
            <option value="">T3 default</option>
            {providers.map((p) => (
              <option key={p.providerInstanceId} value={p.providerInstanceId}>
                {p.displayName}
              </option>
            ))}
            {value.providerInstanceId !== null && provider === undefined ? (
              <option value={value.providerInstanceId}>{value.providerInstanceId} (not in catalog)</option>
            ) : null}
          </Select>
        )}
      </Field>
      <Field label="Model">
        {(id) => (
          <Select
            id={id}
            disabled={disabled || value.providerInstanceId === null}
            value={value.model ?? ""}
            onChange={(e) => setModel(e.target.value)}
          >
            <option value="">Provider default</option>
            {(provider?.models ?? []).map((m) => (
              <option key={m.id} value={m.id}>
                {m.label}
              </option>
            ))}
            {value.model !== null && model === undefined ? (
              <option value={value.model}>{value.model} (not in catalog)</option>
            ) : null}
          </Select>
        )}
      </Field>
      {model?.options.map((opt) => {
        const current = value.modelOptions.find((o) => o.id === opt.id);
        const index = current === undefined ? -1 : opt.values.findIndex((v) => v === current.value);
        return (
          <Field key={opt.id} label={opt.id.replace(/([a-z])([A-Z])/g, "$1 $2")}>
            {(id) => (
              <Select
                id={id}
                disabled={disabled}
                value={index === -1 ? "" : String(index)}
                onChange={(e) => setOption(opt.id, e.target.value === "" ? undefined : opt.values[Number(e.target.value)])}
              >
                {opt.default === null || index === -1 ? <option value="">-</option> : null}
                {opt.values.map((v, i) => (
                  <option key={String(v)} value={String(i)}>
                    {String(v)}
                    {v === opt.default ? " (default)" : ""}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        );
      })}
      {model === undefined && value.modelOptions.length > 0 ? (
        <div className="col-span-full">
          <Muted>Options (kept): {optionsText(value.modelOptions)}</Muted>
        </div>
      ) : null}
      {modes.includes(value.runtimeMode) ? null : <p role="status" className="col-span-full text-small text-bad">Selected permission mode is unavailable for this provider. Choose a supported mode before launching.</p>}
      <fieldset disabled={disabled} className="col-span-full min-w-0 disabled:opacity-55">
        <legend className="label-caps mb-1.5">Runtime</legend>
        <Segmented
          label="Runtime mode"
          options={modeList.map((m) => ({ value: m, label: RUNTIME_LABELS[m] }))}
          value={value.runtimeMode}
          onChange={(runtimeMode) => onChange({ ...value, runtimeMode })}
        />
      </fieldset>
    </div>
  );
}
