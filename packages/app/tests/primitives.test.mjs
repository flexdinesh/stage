// Raw controls and visual recipes live in components/ui/ (see DESIGN.md "Primitives"). Feature code composes the
// primitives, so a hand-rolled copy (its own button, popover, confirm or row recipe) drifts from the canon. Each rule
// names the primitive to use; ALLOW lists the few deliberate exceptions, each with its reason.
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { test } from "node:test";

const src = new URL("../src/", import.meta.url).pathname;
const OWNERS = [join("components", "ui") + "/", join("components", "ui.tsx")];

function files(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory() ? files(join(dir, e.name)) : /\.(ts|tsx)$/.test(e.name) ? [join(dir, e.name)] : [],
  );
}

const RULES = [
  {
    id: "button",
    pattern: /<button\b/,
    fix: "use Button, IconButton, TextButton, RowButton, DisclosureButton or PickerButton",
    allow: {
      "components/Sidebar.tsx": "nav row: navRowClass sizing and the pin button share one hover row",
      "components/TaskGroups.tsx": "stat cell: a tone-filled filter cell with its inset state edge (StatCount)",
      "components/Notifications.tsx": "toast stack '+N more': a full-width toast-shaped button",
      "components/Layout.tsx": "sidebar search field: a button that looks like the Input it opens",
    },
  },
  {
    id: "native-input",
    pattern: /<(input|select|textarea)\b/,
    fix: "use Input, Textarea, Checkbox, Radio, Segmented or Combobox",
    allow: {
      "components/Images.tsx": "hidden file input behind a Button (no visible control)",
      "components/CommandPalette.tsx": "the palette's borderless search line",
    },
  },
  {
    id: "choice",
    pattern: /type="(checkbox|radio)"/,
    fix: "use Checkbox or Radio (inside ChoiceRow for a labelled row)",
  },
  {
    id: "popover",
    pattern: /\bpopover="|popoverTarget|showPopover|hidePopover|togglePopover/,
    fix: "use useMenu + Menu for actions, usePopover + PopoverPanel for a panel",
  },
  {
    id: "confirm",
    pattern: /\bwindow\.confirm\b|(^|[^.\w])confirm\(/,
    fix: "use ConfirmInline (inline Waiting Yellow confirm with Cancel focused)",
  },
  {
    id: "portal",
    pattern: /\bcreatePortal\b/,
    fix: "use FloatingPortal for floating UI, Drawer or ModalLayer for modals",
    allow: {
      "components/StageLinks.tsx": "mounts ref chips into rendered markdown hosts, not floating UI",
    },
  },
  {
    id: "inline-error",
    pattern: /text-(meta|small)[^"]*\btext-bad\b|\btext-bad\b[^"]*text-(meta|small)/,
    fix: "use InlineError for a one-line error, ErrorText or Alert tone=\"bad\" for a message",
  },
  {
    id: "panel-header",
    pattern: /min-h-8[^"]*bg-panel|bg-panel[^"]*min-h-8/,
    fix: "use Panel or PanelHeader",
  },
  {
    id: "row",
    pattern: /border-b border-line[^"]*\bpx-3\b[^"]*last:border-b-0/,
    fix: "use Row inside RowList, or rowClass where the element can't be an li",
  },
  {
    id: "need-edge",
    pattern: /border-l-need|inset_2px_0_var\(--need\)/,
    fix: "use needEdgeClass(need) or <Row need>",
  },
  {
    id: "disclosure",
    pattern: /-rotate-90/,
    fix: "use DisclosureChevron or DisclosureButton",
  },
  {
    id: "muted-prose",
    pattern: /<p className="text-small text-muted"/,
    fix: "use Muted for muted prose, EmptyState for 'nothing here'",
  },
  {
    id: "shadow",
    pattern: /\bshadow-(sm|md|lg|xl|2xl|\[(?!inset))/,
    fix: "no elevation shadows (DESIGN.md: flat, hairlines); the palette is the one exception",
    allow: {
      "components/CommandPalette.tsx": "the palette floats over the app: the one sanctioned shadow",
    },
  },
  {
    id: "rounded",
    pattern: /\brounded(?!-none)\b/,
    fix: "corners are square (DESIGN.md)",
  },
];

function scan() {
  const found = [];
  const used = new Set();
  for (const file of files(src)) {
    const rel = relative(src, file);
    if (OWNERS.some((o) => rel === o || rel.startsWith(o))) continue;
    if (rel === join("pages", "DevUiPage.tsx")) continue;
    readFileSync(file, "utf8")
      .split("\n")
      .forEach((line, i) => {
        for (const rule of RULES) {
          if (!rule.pattern.test(line)) continue;
          if (rule.allow?.[rel] !== undefined) {
            used.add(`${rule.id} ${rel}`);
            continue;
          }
          found.push(`${rel}:${i + 1}: [${rule.id}] ${rule.fix}: ${line.trim()}`);
        }
      });
  }
  return { found, used };
}

test("feature code composes the ui primitives instead of hand-rolling them", () => {
  assert.deepEqual(scan().found, []);
});

test("every allowlist entry is still needed", () => {
  const { used } = scan();
  const stale = RULES.flatMap((r) => Object.keys(r.allow ?? {}).map((f) => `${r.id} ${f}`)).filter((k) => !used.has(k));
  assert.deepEqual(stale, [], "remove allowlist entries that no longer match");
});

test("the rules catch the hand-rolled recipes they name", () => {
  const samples = {
    button: '<button type="button" onClick={go}>',
    "native-input": '<input type="text" />',
    choice: '<input type="checkbox" className="accent-fg" />',
    popover: '<div popover="auto" id={id}>',
    confirm: 'if (!window.confirm("Delete?")) return;',
    portal: "createPortal(menu, document.body)",
    "inline-error": '<p className="font-mono text-meta text-bad">{msg}</p>',
    "panel-header": '<div className="flex min-h-8 items-center border-b border-line bg-panel px-3 py-1">',
    row: '<li className="border-b border-line px-3 py-2 last:border-b-0">',
    "need-edge": 'className={cx("border-l-2", need ? "border-l-need" : "border-l-transparent")}',
    disclosure: '<IconChevronDown className={cx(!open && "-rotate-90")} />',
    "muted-prose": '<p className="text-small text-muted">No tasks yet.</p>',
    shadow: 'className="border bg-surface shadow-lg"',
    rounded: 'className="rounded-md border"',
  };
  for (const rule of RULES) {
    assert.ok(rule.pattern.test(samples[rule.id]), `${rule.id} matches its sample`);
  }
  assert.ok(!RULES.find((r) => r.id === "shadow").pattern.test('shadow-[inset_0_-2px_var(--c)]'), "inset state edges are not shadows");
  assert.ok(!RULES.find((r) => r.id === "confirm").pattern.test("onConfirm={() => remove.mutate()}"), "onConfirm props are fine");
});
