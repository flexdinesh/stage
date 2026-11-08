export function PagePlaceholder({ title }: { title: string }) {
  return (
    <section>
      <h1 className="text-xl font-semibold">{title}</h1>
      <p className="mt-2 text-sm text-neutral-500">Not implemented yet.</p>
    </section>
  );
}
