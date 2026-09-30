import type { AppManifest } from "./manifest";
import { dashboard } from "./dashboard/app";
import { activity } from "./activity/app";
import { runs } from "./runs/app";
import { logs } from "./logs/app";
import { graph } from "./graph/app";
import { diff } from "./diff/app";
import { notes } from "./notes/app";
import { tools } from "./tools/app";
import { shortcuts } from "./shortcuts/app";
import { settings } from "./settings/app";

// Every launcher tile, in the launcher's order: what magus is doing now, what just happened, one
// run, then the workspace and its toolchain, then the apps you consult rather than work in. apps.test.ts fails when a
// directory under apps/ is missing here, or this names one that is gone.
export const APPS: readonly AppManifest[] = [
  dashboard,
  activity,
  runs,
  logs,
  graph,
  diff,
  notes,
  tools,
  shortcuts,
  settings,
];
