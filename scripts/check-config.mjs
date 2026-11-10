import fs from "node:fs";
import assert from "node:assert/strict";
const config = JSON.parse(fs.readFileSync("monitor.config.json", "utf8"));
const workflow = fs.readFileSync(".github/workflows/pages.yml", "utf8");
const cron = workflow.match(/cron:\s*["']([^"']+)["']/)?.[1];
assert.equal(
  cron,
  config.schedule,
  "Workflow cron must match monitor.config.json",
);
assert.equal(config.timezone, "UTC");
assert.deepEqual(Object.keys(config.enabled).sort(), ["bugcrowd", "hackerone"]);
assert.ok(Object.values(config.enabled).every((v) => typeof v === "boolean"));
assert.match(config.repository, /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/);
assert.match(config.workflow, /^[A-Za-z0-9_.-]+\.yml$/);
console.log("Workflow schedule and collector configuration match.");
