import type { Capabilities, HarnessModel, HarnessOption } from "../api/types.ts";

/** What a config may set: the harness-wide options, then the model's own. */
export function optionsFor(caps: Pick<Capabilities, "options"> | undefined, model: Pick<HarnessModel, "options"> | undefined): HarnessOption[] {
  return [...(caps?.options ?? []), ...(model?.options ?? [])];
}
