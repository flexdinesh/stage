// Generic harness connection setup forms (harnesses/generic.tsx):
// form values from a setup schema and the request bodies they send.
import type { HarnessAction, HarnessStatus, SetupField } from "../api/types.ts";

export type SetupValues = Record<string, string | boolean>;
type FieldStates = HarnessStatus["fields"];

/** Form values: current non-secret settings; secrets and flags start empty. */
export function initialValues(fields: SetupField[], current: FieldStates): SetupValues {
  const out: SetupValues = {};
  for (const f of fields) {
    const v = current.find((s) => s.id === f.id);
    out[f.id] = f.kind === "bool" ? v?.value === "true" : f.kind === "secret" ? "" : (v?.value ?? "");
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
      if (v !== (st?.value === "true")) body[f.id] = v;
    } else if (f.kind === "secret") {
      if (v !== "") body[f.id] = v;
    } else if (v !== (st?.value ?? "")) {
      body[f.id] = v === "" ? null : v;
    }
  }
  return body;
}

/** How a text-like field's input renders: its type, and monospace for a `path` (the harness checks it). */
export function fieldInput(field: Pick<SetupField, "kind">): { type: "text" | "url" | "password"; mono: boolean } {
  return { type: field.kind === "secret" ? "password" : field.kind === "url" ? "url" : "text", mono: field.kind === "path" };
}

/** Action input body: entered values and checked flags. */
export function actionBody(fields: SetupField[], values: SetupValues): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  for (const f of fields) {
    const v = values[f.id];
    if (f.kind === "bool" ? v === true || (f.required && v === false) : v !== undefined && v !== "") body[f.id] = v;
  }
  return body;
}

/** A submitted secret field empties immediately, including when a request fails. */
export function clearSecrets(fields: readonly SetupField[], values: SetupValues): SetupValues {
  const next = { ...values };
  for (const f of fields) if (f.kind === "secret") next[f.id] = "";
  return next;
}

/** Static declarations supply identity and schema. Live setup status can further restrict availability. */
export function availableSetupActions(actions: readonly HarnessAction[], states: HarnessStatus["actions"]): HarnessAction[] {
  return actions.filter((action) => action.target === "connection" && action.callers.includes("user")).map((action) => {
    const state = states.find((entry) => entry.id === action.id);
    if (!action.availability.available || state?.available !== false) return action;
    return { ...action, availability: { available: false, reason: state.reason || "This action is unavailable." } };
  });
}

/**
 * Task toolbar split: a user action without input runs from a toolbar
 * button (confirmed inline when it declares a confirmation); one with input
 * keeps its form.
 */
export function splitToolbarActions<A extends Pick<HarnessAction, "callers" | "input">>(actions: readonly A[]): { buttons: A[]; forms: A[] } {
  const buttons: A[] = [];
  const forms: A[] = [];
  for (const action of actions) {
    if (!action.callers.includes("user")) continue;
    (action.input === undefined || action.input.length === 0 ? buttons : forms).push(action);
  }
  return { buttons, forms };
}
