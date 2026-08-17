// Coalesces live repaint work and retains the newest hidden update.

export class FrameScheduler {
  private visible = true;
  private frame = 0;
  private pending: (() => void) | null = null;

  schedule(paint: () => void): void {
    this.pending = paint;
    if (!this.visible || this.frame) return;
    this.frame = requestAnimationFrame(() => {
      this.frame = 0;
      const next = this.pending;
      this.pending = null;
      next?.();
    });
  }

  setVisible(visible: boolean): void {
    if (this.visible === visible) return;
    this.visible = visible;
    if (!visible || !this.pending || this.frame) return;
    this.frame = requestAnimationFrame(() => {
      this.frame = 0;
      const next = this.pending;
      this.pending = null;
      next?.();
    });
  }

  cancel(): void {
    if (this.frame) cancelAnimationFrame(this.frame);
    this.frame = 0;
    this.pending = null;
  }
}
