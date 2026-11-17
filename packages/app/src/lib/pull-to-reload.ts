export type PullPhase = "idle" | "pulling" | "armed" | "reloading";

type Point = { identifier: number; clientX: number; clientY: number };

/** One touch sequence. A cancelled gesture cannot re-arm until a new touch starts. */
export class PullToReloadGesture {
  phase: PullPhase = "idle";
  offset = 0;
  private origin: Point | null = null;

  start(point: Point): void {
    if (this.phase === "reloading") return;
    this.origin = point;
    this.phase = "idle";
    this.offset = 0;
  }

  /** True only when this gesture owns a downward pull. */
  move(point: Point): boolean {
    const origin = this.origin;
    if (origin === null) return false;
    if (point.identifier !== origin.identifier) {
      this.cancel();
      return false;
    }
    const dx = Math.abs(point.clientX - origin.clientX);
    const dy = point.clientY - origin.clientY;
    // Claim a downward move early, before the browser commits native scrolling.
    if (this.phase === "idle" && Math.max(dx, Math.abs(dy)) < 8) return dy > dx;
    if (dy <= 0 || dx >= dy) {
      this.cancel();
      return false;
    }
    this.phase = dy >= 80 ? "armed" : "pulling";
    // Reveal the indicator at the trigger distance, with increasing resistance beyond it.
    this.offset = 112 * dy / (dy + 80);
    return true;
  }

  release(point: Point): boolean {
    if (this.origin === null) return false;
    this.move(point);
    const reload = this.origin !== null && this.phase === "armed";
    this.cancel();
    if (reload) this.phase = "reloading";
    return reload;
  }

  cancel(): void {
    this.origin = null;
    this.phase = "idle";
    this.offset = 0;
  }
}
