import { useEffect, useRef, useState, type DragEvent, type ReactNode } from "react";
import { api, attachmentUrl } from "../api/client.ts";
import { IMAGE_TYPES, MAX_IMAGE_BYTES, MAX_IMAGES, type Attachment } from "../api/types.ts";
import { IconClose, IconImage } from "./icons.tsx";
import { Button, cx, errorText } from "./ui.tsx";

/** One image in an editable set: uploaded (`attachment`), uploading, or failed (`error`). */
interface ImageItem {
  key: string;
  name: string;
  src: string;
  attachment: Attachment | null;
  error: string | null;
  /** Object URL to revoke when the item goes away. */
  blob: string | null;
}

const fromAttachment = (a: Attachment): ImageItem => ({
  key: `a${a.id}`,
  name: a.name,
  src: attachmentUrl(a.id),
  attachment: a,
  error: null,
  blob: null,
});

const isImageType = (t: string) => IMAGE_TYPES.some((x) => x === t);

let keySeq = 0;

/**
 * Editable image set. Files upload as staged attachments as soon as they are added; `ids` are the
 * uploaded ones in order. Removing an upload made here deletes it; removing one of `initial` only
 * drops it from `ids` (the save applies it).
 */
export function useImageSet(initial: readonly Attachment[] = []) {
  const [items, setItems] = useState<ImageItem[]>(() => initial.map(fromAttachment));
  const [notice, setNotice] = useState<string | null>(null);
  const initialIds = useRef(new Set(initial.map((a) => a.id)));
  const blobs = useRef(new Set<string>());

  useEffect(() => {
    const live = blobs.current;
    return () => live.forEach((u) => URL.revokeObjectURL(u));
  }, []);

  const patch = (key: string, p: Partial<ImageItem>) =>
    setItems((prev) => prev.map((it) => (it.key === key ? { ...it, ...p } : it)));

  const add = (files: readonly File[]) => {
    const skipped: string[] = [];
    let room = MAX_IMAGES - items.length;
    const added: { item: ImageItem; file: File }[] = [];
    for (const file of files) {
      if (!isImageType(file.type)) skipped.push(`${file.name}: not PNG, JPEG, GIF or WebP`);
      else if (file.size > MAX_IMAGE_BYTES) skipped.push(`${file.name}: over 10 MB`);
      else if (room <= 0) skipped.push(`${file.name}: ${MAX_IMAGES} images max`);
      else {
        room--;
        const blob = URL.createObjectURL(file);
        blobs.current.add(blob);
        keySeq++;
        added.push({ file, item: { key: `f${keySeq}`, name: file.name, src: blob, attachment: null, error: null, blob } });
      }
    }
    setNotice(skipped.length === 0 ? null : `Skipped ${skipped.join("; ")}`);
    if (added.length === 0) return;
    setItems((prev) => [...prev, ...added.map((a) => a.item)]);
    for (const { item, file } of added) {
      api.uploadImage(file).then(
        (attachment) => patch(item.key, { attachment }),
        (err: unknown) => patch(item.key, { error: errorText(err) ?? "upload failed" }),
      );
    }
  };

  const remove = (key: string) => {
    const it = items.find((x) => x.key === key);
    if (it === undefined) return;
    setItems((prev) => prev.filter((x) => x.key !== key));
    if (it.blob !== null) {
      URL.revokeObjectURL(it.blob);
      blobs.current.delete(it.blob);
    }
    // Staged uploads made here are deleted now; the task's own are removed by the save.
    if (it.attachment !== null && !initialIds.current.has(it.attachment.id)) {
      api.deleteAttachment(it.attachment.id).catch(() => undefined);
    }
  };

  /** Forget everything (after a follow-up sent the images; the server deleted them). */
  const clear = () => {
    items.forEach((it) => {
      if (it.blob !== null) {
        URL.revokeObjectURL(it.blob);
        blobs.current.delete(it.blob);
      }
    });
    setItems([]);
    setNotice(null);
  };

  const ids = items.flatMap((it) => (it.attachment === null ? [] : [it.attachment.id]));
  const uploading = items.some((it) => it.attachment === null && it.error === null);
  const failed = items.some((it) => it.error !== null);
  return { items, ids, uploading, failed, notice, add, remove, clear };
}

export type ImageSet = ReturnType<typeof useImageSet>;

const ACCEPT = IMAGE_TYPES.join(",");

const hasFiles = (e: DragEvent) => e.dataTransfer.types.includes("Files");

/**
 * Drop target around `children` (the prompt or message box) plus the thumbnail strip, an Add image
 * picker and the n/8 count. Drops outside the target are swallowed so a missed drop never navigates
 * the tab to the image.
 */
export function ImageDropzone({
  set,
  children,
  disabled = false,
  label = "Images",
}: {
  set: ImageSet;
  children: ReactNode;
  disabled?: boolean;
  label?: string;
}) {
  const [dragging, setDragging] = useState(false);
  const depth = useRef(0);
  const input = useRef<HTMLInputElement>(null);
  const full = set.items.length >= MAX_IMAGES;

  useEffect(() => {
    const swallow = (e: globalThis.DragEvent) => {
      if (e.dataTransfer?.types.includes("Files")) e.preventDefault();
    };
    window.addEventListener("dragover", swallow);
    window.addEventListener("drop", swallow);
    return () => {
      window.removeEventListener("dragover", swallow);
      window.removeEventListener("drop", swallow);
    };
  }, []);

  const onDragEnter = (e: DragEvent) => {
    if (disabled || !hasFiles(e)) return;
    depth.current++;
    setDragging(true);
  };
  const onDragLeave = (e: DragEvent) => {
    if (disabled || !hasFiles(e)) return;
    depth.current = Math.max(0, depth.current - 1);
    if (depth.current === 0) setDragging(false);
  };
  const onDragOver = (e: DragEvent) => {
    if (disabled || !hasFiles(e)) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "copy";
  };
  const onDrop = (e: DragEvent) => {
    if (disabled || !hasFiles(e)) return;
    e.preventDefault();
    depth.current = 0;
    setDragging(false);
    set.add(Array.from(e.dataTransfer.files));
  };

  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div
        className="relative"
        onDragEnter={onDragEnter}
        onDragLeave={onDragLeave}
        onDragOver={onDragOver}
        onDrop={onDrop}
      >
        {children}
        {dragging ? (
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center border border-dashed border-accent bg-surface/90 font-mono text-detail text-fg">
            {full ? `${MAX_IMAGES} images max` : "Drop images to attach"}
          </div>
        ) : null}
      </div>
      {set.items.length > 0 ? (
        <ul aria-label={label} className="flex flex-wrap gap-2">
          {set.items.map((it) => (
            <li key={it.key} className="relative">
              <img
                src={it.src}
                alt={it.name}
                title={it.error ?? it.name}
                className={cx(
                  "size-14 border bg-panel object-cover",
                  it.error === null ? "border-line-strong" : "border-bad",
                  it.attachment === null && "opacity-60",
                )}
              />
              {it.attachment === null && it.error === null ? (
                <span
                  role="status"
                  aria-label={`Uploading ${it.name}`}
                  className="absolute inset-0 m-auto size-3 animate-spin border-[1.5px] border-fg border-t-transparent"
                />
              ) : null}
              {disabled ? null : (
                <button
                  type="button"
                  aria-label={`Remove ${it.name}`}
                  onClick={() => set.remove(it.key)}
                  className="absolute -top-1.5 -right-1.5 flex size-[18px] items-center justify-center border border-line-strong bg-surface text-muted hover:text-fg"
                >
                  <IconClose size={10} />
                </button>
              )}
            </li>
          ))}
        </ul>
      ) : null}
      <div className="flex flex-wrap items-center gap-2">
        <input
          ref={input}
          type="file"
          accept={ACCEPT}
          multiple
          hidden
          onChange={(e) => {
            set.add(Array.from(e.target.files ?? []));
            e.target.value = "";
          }}
        />
        <Button small variant="ghost" className="-ml-2" disabled={disabled || full} onClick={() => input.current?.click()}>
          <IconImage size={12} />
          Add image
        </Button>
        <span className="font-mono text-meta-sm text-muted">
          {set.items.length === 0 ? "or drop PNG, JPEG, GIF, WebP · 10 MB each" : `${set.items.length}/${MAX_IMAGES}`}
        </span>
      </div>
      {set.notice === null ? null : <p className="text-meta text-bad">{set.notice}</p>}
      {set.items
        .filter((it) => it.error !== null)
        .map((it) => (
          <p key={it.key} className="text-meta text-bad">
            {it.name}: {it.error}
          </p>
        ))}
    </div>
  );
}

/** Read-only thumbnails; each opens the full image in a new tab. */
export function ImageStrip({ attachments, label = "Images" }: { attachments: readonly Attachment[]; label?: string }) {
  if (attachments.length === 0) return null;
  return (
    <ul aria-label={label} className="flex flex-wrap gap-2">
      {attachments.map((a) => (
        <li key={a.id}>
          <a href={attachmentUrl(a.id)} target="_blank" rel="noreferrer" title={a.name} className="block hover:opacity-85">
            <img src={attachmentUrl(a.id)} alt={a.name} loading="lazy" className="size-14 border border-line-strong bg-panel object-cover" />
          </a>
        </li>
      ))}
    </ul>
  );
}
