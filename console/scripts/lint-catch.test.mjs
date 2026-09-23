import assert from "node:assert/strict";
import { test } from "node:test";
import { findSwallows } from "./lint-catch.mjs";

const shapes = (src) => findSwallows(src).map((s) => s.shape);

test("each swallowed shape is rejected", () => {
  assert.deepEqual(shapes("try { a(); } catch { return null; }"), ["catch {"]);
  assert.deepEqual(shapes("try { a(); } catch (e) {\n  // ignore\n}"), ["empty catch body"]);
  assert.deepEqual(shapes("try { a(); } catch (e) { /* ignore */ }"), ["empty catch body"]);
  assert.deepEqual(shapes("p.catch(() => {});"), [".catch(() => {})"]);
  assert.deepEqual(shapes("p.catch(() => undefined);"), [".catch(() => undefined)"]);
  assert.deepEqual(shapes("p.catch((_e) => null);"), [".catch((_e) => null)"]);
});

test("a handled or marked catch passes", () => {
  assert.deepEqual(shapes("try { a(); } catch (e) { report(e); }"), []);
  assert.deepEqual(shapes("try { a(); } catch {\n  // not-a-failure: storage is optional\n}"), []);
  assert.deepEqual(shapes("try { a(); } catch { // reported: by the transport\n  return [];\n}"), []);
  assert.deepEqual(shapes("// not-a-failure: fullscreen is optional\np.catch(() => {});"), []);
  assert.deepEqual(shapes("p.catch((e) => report(e));"), []);
});

test("a marker needs a reason", () => {
  assert.deepEqual(shapes("try { a(); } catch {\n  // not-a-failure:\n}"), ["catch {"]);
});

test("a brace inside a string does not end the body early", () => {
  assert.deepEqual(shapes('try { a(); } catch (e) { log("}"); }'), []);
});
