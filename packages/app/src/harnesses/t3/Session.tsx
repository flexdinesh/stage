import { Meta } from "../../components/ui.tsx";
import type { SessionSlotProps } from "../types.ts";

export function T3ThreadDetails({ activity }: SessionSlotProps) {
  const details = activity?.details;
  if (typeof details !== "object" || details === null) return null;

  const fields = [
    { label: "Provider instance", value: "providerInstance" in details ? details.providerInstance : undefined },
    { label: "Model", value: "model" in details ? details.model : undefined },
    { label: "Runtime mode", value: "runtimeMode" in details ? details.runtimeMode : undefined },
  ].flatMap(({ label, value }) => typeof value === "string" && value.trim() ? [{ label, value }] : []);
  if (fields.length === 0) return null;

  return (
    <dl aria-label="T3 thread details" className="flex min-w-0 basis-full flex-wrap gap-x-4 gap-y-1">
      {fields.map(({ label, value }) => (
        <div className="min-w-0" key={label}>
          <dt className="sr-only">{label}</dt>
          <dd><Meta className="wrap-anywhere">{value}</Meta></dd>
        </div>
      ))}
    </dl>
  );
}
