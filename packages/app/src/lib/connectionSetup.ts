// Generic harness connection setup forms (components/HarnessConnections.tsx):
// form values from a setup schema and the request bodies they send.
import type { HarnessStatus, SetupField } from "../api/types.ts";

export type SetupValues = Record<string, string | boolean>;
type FieldStates = HarnessStatus["fields"];

/** Form values: current non-secret settings; secrets and flags start empty. */
export function initialValues(fields: SetupField[], current: FieldStates): SetupValues {
  const out: SetupValues = {};
  for (const f of fields) {
    const v = current.find((s) => s.id === f.id);
    out[f.id] = f.kind === "bool" ? false : f.kind === "secret" ? "" : (v?.value ?? "");
  }
  return out;
}

/** PUT settings body: changed text fields (cleared: null resets), entered secrets, checked flags. */
export function settingsBody(fields: SetupField[], current: FieldStates, values: SetupValues): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  for (const f of fields) {
    const st = current.find((s) => s.id === f.id);
    const v = values[f.id];
    if (v === undefined) continue;
    if (f.kind === "bool") {
      if (v === true) body[f.id] = true;
    } else if (f.kind === "secret") {
      if (v !== "") body[f.id] = v;
    } else if (v !== (st?.value ?? "")) {
      body[f.id] = v === "" ? null : v;
    }
  }
  return body;
}

/** Action input body: entered values and checked flags. */
export function actionBody(fields: SetupField[], values: SetupValues): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  for (const f of fields) {
    const v = values[f.id];
    if (f.kind === "bool" ? v === true : v !== undefined && v !== "") body[f.id] = v;
  }
  return body;
}
