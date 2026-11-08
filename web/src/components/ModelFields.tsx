import { useCatalog } from "../api/queries.ts";
import {
  RUNTIME_MODES,
  type CatalogModel,
  type CatalogProvider,
  type ModelOption,
  type RuntimeMode,
} from "../api/types.ts";
import { optionsText, pick } from "../lib/format.ts";
import { Field, Muted, Select, errorText } from "./ui.tsx";

export interface ModelConfig {
  providerInstanceId: string | null;
  model: string | null;
  modelOptions: ModelOption[];
  runtimeMode: RuntimeMode;
}

const allowedModes = (p: CatalogProvider | undefined): readonly RuntimeMode[] => p?.runtimeModes ?? RUNTIME_MODES;

function fitMode(current: RuntimeMode, allowed: readonly RuntimeMode[]): RuntimeMode {
  if (allowed.includes(current)) return current;
  if (allowed.includes("full-access")) return "full-access";
  return allowed[0] ?? current;
}

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
      runtimeMode: fitMode(value.runtimeMode, allowedModes(next)),
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

  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
      {catalogError === null ? null : (
        <p className="text-xs text-amber-700 sm:col-span-2 dark:text-amber-400">
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
          <Field key={opt.id} label={opt.id}>
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
        <div className="sm:col-span-2">
          <Muted>Options (kept): {optionsText(value.modelOptions)}</Muted>
        </div>
      ) : null}
      <Field label="Runtime mode">
        {(id) => (
          <Select
            id={id}
            disabled={disabled}
            value={value.runtimeMode}
            onChange={(e) => onChange({ ...value, runtimeMode: pick(RUNTIME_MODES, e.target.value) ?? value.runtimeMode })}
          >
            {(modes.includes(value.runtimeMode) ? modes : [...modes, value.runtimeMode]).map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </Select>
        )}
      </Field>
    </div>
  );
}
