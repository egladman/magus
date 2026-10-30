// share.test.ts - the share picker's default lifetime, as the panel draws it.

import assert from "node:assert/strict";
import { test } from "node:test";
import { mountSharePanel } from "./share";

test("the picker opens on 15 minutes, the first entry and auth.DefaultShareTTL", () => {
  const panel = mountSharePanel();
  panel.open();
  const pressed = document.querySelectorAll('#console-sharepanel [aria-pressed="true"]');
  assert.equal(pressed.length, 1);
  assert.equal(pressed[0]?.textContent, "15 minutes");
  const first = document.querySelector("#console-sharepanel .pf-v6-c-toggle-group__button");
  assert.ok(first === pressed[0]);
  panel.close();
});
