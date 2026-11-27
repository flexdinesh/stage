import assert from "node:assert/strict";
import { test } from "node:test";
import {
  journalEntryMarkdown,
  journalEntryUrl,
  parseStageRef,
  projectMarkdown,
  projectUrl,
  scheduleMarkdown,
  scheduleUrl,
  splitStageRefs,
  taskMarkdown,
  taskUrl,
  treeMarkdown,
  treeUrl,
} from "../src/lib/refs.ts";

const origin = "http://localhost:7380";
const link = (path) => origin + path;
const deep = (path) => `stage:/${path}`;

test("copied links name themselves and escape brackets in titles", () => {
  assert.equal(taskUrl(link, 12), "http://localhost:7380/tasks/12");
  assert.equal(taskMarkdown(link, 12, "Fix [login] flake"), "[stage task #12: Fix \\[login\\] flake](http://localhost:7380/tasks/12)");
  assert.equal(taskMarkdown(link, 3, "a\\b\n  c"), "[stage task #3: a\\\\b c](http://localhost:7380/tasks/3)");
  assert.equal(treeUrl(link, 4), "http://localhost:7380/trees/4");
  assert.equal(treeMarkdown(link, 4, "Ship [auth]"), "[stage tree #4: Ship \\[auth\\]](http://localhost:7380/trees/4)");
  assert.equal(treeMarkdown(deep, 4, "Ship"), "[stage tree #4: Ship](stage://trees/4)");
  assert.equal(scheduleUrl(link, 7), "http://localhost:7380/schedules/7");
  assert.equal(scheduleMarkdown(link, 7, "Nightly [deps]"), "[stage schedule #7: Nightly \\[deps\\]](http://localhost:7380/schedules/7)");
  assert.equal(projectUrl(link, 3), "http://localhost:7380/projects/3");
  assert.equal(projectMarkdown(deep, 3, "Stage"), "[stage project #3: Stage](stage://projects/3)");
  assert.equal(journalEntryUrl(link, "task", 12, 45), "http://localhost:7380/tasks/12?entry=45");
  assert.equal(journalEntryUrl(deep, "tree", 4, 45), "stage://trees/4?entry=45");
  assert.equal(journalEntryMarkdown(link, "task", 12, 45), "[stage note #45 on task #12](http://localhost:7380/tasks/12?entry=45)");
  assert.equal(journalEntryMarkdown(link, "tree", 4, 46), "[stage note #46 on tree #4](http://localhost:7380/trees/4?entry=46)");
});

test("stage links match by path on any host", () => {
  assert.deepEqual(parseStageRef("http://localhost:7380/tasks/12"), { kind: "task", id: 12 });
  assert.deepEqual(parseStageRef("https://stage.tail.ts.net/tasks/7?view=transcript#answer"), { kind: "task", id: 7 });
  assert.deepEqual(parseStageRef("http://10.0.0.2:7380/sessions/codex%3Aab%25c"), { kind: "session", id: "codex:ab%c" });
  assert.deepEqual(parseStageRef("https://proxy.lan/stage/tasks/9"), { kind: "task", id: 9 });
  assert.deepEqual(parseStageRef("http://x/trees/4?task=9"), { kind: "tree", id: 4 });
  assert.deepEqual(parseStageRef("http://x/schedules/7"), { kind: "schedule", id: 7 });
  assert.deepEqual(parseStageRef("https://phone.lan/projects/3?state=done"), { kind: "project", id: 3 });
  assert.deepEqual(parseStageRef("http://x/tasks/12?entry=45#journal-45"), { kind: "journal", owner: "task", ownerId: 12, entryId: 45 });
  assert.deepEqual(parseStageRef("http://x/trees/4?view=notes&entry=45"), { kind: "journal", owner: "tree", ownerId: 4, entryId: 45 });
  assert.deepEqual(parseStageRef("http://x/tasks/12?entry=0"), { kind: "task", id: 12 });
  assert.deepEqual(parseStageRef("http://x/schedules/7?entry=45"), { kind: "schedule", id: 7 });
  assert.deepEqual(parseStageRef("http://x/connections/2/sessions/t-1"), { kind: "session", id: "t-1", connectionId: 2 });
  for (const bad of [
    "http://x/tasks/abc",
    "http://x/tasks/0",
    "http://x/projects/0",
    "https://github.com/orgs/x/projects/3",
    "https://github.com/users/x/projects/3",
    "https://proxy.lan/stage/schedules/7",
    "http://x/schedules/x",
    "http://x/connections/0/sessions/t",
    "http://x/",
    "/tasks/1",
    "t3code://threads/e/t",
    "nope",
    "http://x/sessions/%E0%A4%A",
    "stage://",
    "stage://tasks",
    "stage://projects/1/x",
    "stage://journal/1",
    "stage://localhost/tasks/1/x",
    "stagex://tasks/1",
    "mygo://localhost/tasks/1",
  ]) {
    assert.equal(parseStageRef(bad), null, bad);
  }
});

test("desktop deep links read as stage links", () => {
  assert.deepEqual(parseStageRef("stage://tasks/12"), { kind: "task", id: 12 });
  assert.deepEqual(parseStageRef("stage://localhost/tasks/12"), { kind: "task", id: 12 });
  assert.deepEqual(parseStageRef("stage-dev://sessions/codex%3Aab"), { kind: "session", id: "codex:ab" });
  assert.deepEqual(parseStageRef("Stage://Tasks/3?view=transcript"), { kind: "task", id: 3 });
  assert.deepEqual(parseStageRef("stage://trees/4"), { kind: "tree", id: 4 });
  assert.deepEqual(parseStageRef("stage-dev://schedules/7"), { kind: "schedule", id: 7 });
  assert.deepEqual(parseStageRef("stage://localhost/projects/3"), { kind: "project", id: 3 });
  assert.deepEqual(parseStageRef("stage://tasks/12?entry=45"), { kind: "journal", owner: "task", ownerId: 12, entryId: 45 });
  assert.deepEqual(parseStageRef("stage://connections/2/sessions/codex%3Aab"), { kind: "session", id: "codex:ab", connectionId: 2 });
  assert.equal(taskUrl(deep, 5), "stage://tasks/5");
  const md = taskMarkdown(deep, 5, "Ship");
  assert.deepEqual(splitStageRefs(`see ${md} or stage-dev://sessions/t-1.`), [
    { kind: "text", text: "see " },
    { kind: "ref", ref: { kind: "task", id: 5 }, text: md, href: "stage://tasks/5" },
    { kind: "text", text: " or " },
    { kind: "ref", ref: { kind: "session", id: "t-1" }, text: "stage-dev://sessions/t-1", href: "stage-dev://sessions/t-1" },
    { kind: "text", text: "." },
  ]);
});

test("splitStageRefs pulls markdown and bare stage links out of text", () => {
  const md = taskMarkdown(link, 12, "Fix [login]");
  const text = `See ${md} and http://phone.lan:7380/sessions/t-1. Also https://github.com/o/r/pull/3 stays.`;
  assert.deepEqual(splitStageRefs(text), [
    { kind: "text", text: "See " },
    { kind: "ref", ref: { kind: "task", id: 12 }, text: md, href: "http://localhost:7380/tasks/12" },
    { kind: "text", text: " and " },
    { kind: "ref", ref: { kind: "session", id: "t-1" }, text: "http://phone.lan:7380/sessions/t-1", href: "http://phone.lan:7380/sessions/t-1" },
    { kind: "text", text: ". Also https://github.com/o/r/pull/3 stays." },
  ]);
  assert.deepEqual(splitStageRefs("no links"), [{ kind: "text", text: "no links" }]);
  const note = journalEntryMarkdown(link, "tree", 4, 45);
  assert.deepEqual(splitStageRefs(`${note}, stage://schedules/7 and http://x/projects/3.`), [
    { kind: "ref", ref: { kind: "journal", owner: "tree", ownerId: 4, entryId: 45 }, text: note, href: "http://localhost:7380/trees/4?entry=45" },
    { kind: "text", text: ", " },
    { kind: "ref", ref: { kind: "schedule", id: 7 }, text: "stage://schedules/7", href: "stage://schedules/7" },
    { kind: "text", text: " and " },
    { kind: "ref", ref: { kind: "project", id: 3 }, text: "http://x/projects/3", href: "http://x/projects/3" },
    { kind: "text", text: "." },
  ]);
  assert.deepEqual(splitStageRefs("[docs](https://example.com/a)"), [{ kind: "text", text: "[docs](https://example.com/a)" }]);
  assert.deepEqual(splitStageRefs("board https://github.com/orgs/x/projects/3."), [{ kind: "text", text: "board https://github.com/orgs/x/projects/3." }]);
});
