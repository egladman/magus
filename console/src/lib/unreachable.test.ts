// unreachable.test.ts - the one throw below the entry names the value that reached it, so a
// member added to a union surfaces as a message and not a bare stack.

import assert from "node:assert/strict";
import { test } from "node:test";
import { unreachable } from "./unreachable";

test("unreachable throws naming the value that reached it", () => {
  assert.throws(() => unreachable("late" as never), /unreachable: late/);
});
