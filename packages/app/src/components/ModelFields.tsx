import { useCatalog } from "../api/queries.ts";
import { type CatalogModel, type ModelOption, type RuntimeMode } from "../api/types.ts";
import { optionsText } from "../lib/format.ts";
import { Field, Muted, errorText, Combobox } from "./ui.tsx";

export interface ModelConfig {
  providerInstanceId: string | null;
  model: string | null;
  modelOptions: ModelOption[];
  runtimeMode: RuntimeMode;
}

export function defaultOptions(m: CatalogModel | undefined): ModelOption[] {
  if (m === undefined) return [];
  return m.options.flatMap((o) => (o.default === null ? [] : [{ id: o.id, value: o.default }]));
}

/** Provider → model → per-model option selects, driven by GET /api/catalog. */
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

  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-3">
      {catalogError === null ? null : (
        <p className="col-span-full text-detail text-need-fg">
          Catalog unavailable ({catalogError}); model selection limited to current values.
        </p>
      )}
      <Field label="Provider">
        {(id) => (
          <Combobox
            id={id}
            disabled={disabled}
            value={value.providerInstanceId ?? ""}
            onChange={setProvider}
            listLabel="Providers"
            options={[
              { value: "", label: "T3 default" },
              ...providers.map((p) => ({ value: p.providerInstanceId, label: p.displayName })),
              ...(value.providerInstanceId !== null && provider === undefined
                ? [{ value: value.providerInstanceId, label: `${value.providerInstanceId} (not in catalog)` }]
                : []),
            ]}
          />
        )}
      </Field>
      <Field label="Model">
        {(id) => (
          <Combobox
            id={id}
            disabled={disabled || value.providerInstanceId === null}
            value={value.model ?? ""}
            onChange={setModel}
            listLabel="Models"
            options={[
              { value: "", label: "Provider default" },
              ...(provider?.models ?? []).map((m) => ({ value: m.id, label: m.label })),
              ...(value.model !== null && model === undefined
                ? [{ value: value.model, label: `${value.model} (not in catalog)` }]
                : []),
            ]}
          />
        )}
      </Field>
      {model?.options.map((opt) => {
        const current = value.modelOptions.find((o) => o.id === opt.id);
        const index = current === undefined ? -1 : opt.values.findIndex((v) => v === current.value);
        return (
          <Field key={opt.id} label={opt.id.replace(/([a-z])([A-Z])/g, "$1 $2")}>
            {(id) => (
              <Combobox
                id={id}
                disabled={disabled}
                value={index === -1 ? "" : String(index)}
                onChange={(next) => setOption(opt.id, next === "" ? undefined : opt.values[Number(next)])}
                listLabel={opt.id}
                options={[
                  ...(opt.default === null || index === -1 ? [{ value: "", label: "-" }] : []),
                  ...opt.values.map((v, i) => ({ value: String(i), label: `${String(v)}${v === opt.default ? " (default)" : ""}` })),
                ]}
              />
            )}
          </Field>
        );
      })}
      {model === undefined && value.modelOptions.length > 0 ? (
        <div className="col-span-full">
          <Muted>Options (kept): {optionsText(value.modelOptions)}</Muted>
        </div>
      ) : null}
    </div>
  );
}
