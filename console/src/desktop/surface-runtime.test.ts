import assert from "node:assert/strict";
import { test } from "node:test";
import { FrameScheduler } from "./surface-runtime";

type Frame = FrameRequestCallback;

function withFrames(run: (flush: () => void) => void): void {
  const request = globalThis.requestAnimationFrame;
  const cancel = globalThis.cancelAnimationFrame;
  const frames = new Map<number, Frame>();
  let next = 0;
  globalThis.requestAnimationFrame = ((cb: Frame): number => {
    const id = ++next;
    frames.set(id, cb);
    return id;
  }) as typeof requestAnimationFrame;
  globalThis.cancelAnimationFrame = ((id: number): void => {
    frames.delete(id);
  }) as typeof cancelAnimationFrame;
  const flush = (): void => {
    const queued = [...frames.values()];
    frames.clear();
    for (const frame of queued) frame(0);
  };
  try {
    run(flush);
  } finally {
    globalThis.requestAnimationFrame = request;
    globalThis.cancelAnimationFrame = cancel;
  }
}

test("FrameScheduler keeps only the newest invisible paint and flushes it on return", () => {
  withFrames((flush) => {
    const scheduler = new FrameScheduler();
    const painted: string[] = [];
    scheduler.setVisible(false);
    scheduler.schedule(() => painted.push("old"));
    scheduler.schedule(() => painted.push("new"));
    flush();
    assert.deepEqual(painted, []);
    scheduler.setVisible(true);
    flush();
    assert.deepEqual(painted, ["new"]);
  });
});

test("FrameScheduler cancels a queued paint on teardown", () => {
  withFrames((flush) => {
    const scheduler = new FrameScheduler();
    let painted = false;
    scheduler.schedule(() => {
      painted = true;
    });
    scheduler.cancel();
    flush();
    assert.equal(painted, false);
  });
});
