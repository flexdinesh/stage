---
name: stage
description: Ink workbench for queueing and watching T3 Code threads; ruled lists where only state carries color.
colors:
  accent: "#0b0d14"
  on-accent: "#ffffff"
  fg: "#0b0d14"
  muted: "#5b6170"
  bg: "#ffffff"
  surface: "#ffffff"
  panel: "#f6f7f9"
  line: "rgba(11, 13, 20, 0.11)"
  line-strong: "rgba(11, 13, 20, 0.2)"
  scrim: "rgba(11, 13, 20, 0.28)"
  need: "#d9a900"
  need-fg: "#7a5c00"
  run: "#0369a1"
  done: "#0f766e"
  ok: "#15803d"
  bad: "#b3261e"
  idle: "#6b7280"
  accent-dark: "#eef0f5"
  on-accent-dark: "#0b0d14"
  fg-dark: "#e6e8ee"
  muted-dark: "#9aa1b0"
  bg-dark: "#1a1d23"
  surface-dark: "#1f2229"
  panel-dark: "#252830"
  line-dark: "rgba(220, 226, 235, 0.11)"
  line-strong-dark: "rgba(220, 226, 235, 0.22)"
  scrim-dark: "rgba(0, 0, 0, 0.55)"
  need-dark: "#e3c341"
  need-fg-dark: "#efd36a"
  run-dark: "#5ec4f0"
  done-dark: "#4fd1c5"
  ok-dark: "#5fd38a"
  bad-dark: "#ec8378"
  idle-dark: "#9aa1b0"
typography:
  headline:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "24px"
    fontWeight: 700
    lineHeight: 1.25
    letterSpacing: "-0.025em"
  headline-wide:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "26px"
    fontWeight: 700
    lineHeight: 1.25
    letterSpacing: "-0.025em"
  title:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "16px"
    fontWeight: 600
    lineHeight: 1.45
  row-title:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "14.5px"
    fontWeight: 500
    lineHeight: 1.45
  body:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "14.5px"
    fontWeight: 400
    lineHeight: 1.45
    letterSpacing: "-0.005em"
  body-sm:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.45
  button:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "13px"
    fontWeight: 500
  button-sm:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "12px"
    fontWeight: 500
  stat:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "16px"
    fontWeight: 700
    fontFeature: "tnum"
  label:
    fontFamily: "JetBrains Mono Variable, ui-monospace, monospace"
    fontSize: "11px"
    fontWeight: 400
    letterSpacing: "0.12em"
  meta:
    fontFamily: "JetBrains Mono Variable, ui-monospace, monospace"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.5
  meta-line:
    fontFamily: "JetBrains Mono Variable, ui-monospace, monospace"
    fontSize: "12.5px"
    fontWeight: 400
rounded:
  none: "0px"
spacing:
  xs: "4px"
  sm: "8px"
  row: "10px"
  md: "12px"
  stack: "14px"
  lg: "16px"
  xl: "24px"
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.on-accent}"
    typography: "{typography.button}"
    rounded: "{rounded.none}"
    padding: "0 12px"
    height: "32px"
  button-secondary:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.fg}"
    typography: "{typography.button}"
    rounded: "{rounded.none}"
    padding: "0 12px"
    height: "32px"
  button-secondary-hover:
    backgroundColor: "{colors.panel}"
  button-ghost:
    textColor: "{colors.muted}"
    typography: "{typography.button}"
    rounded: "{rounded.none}"
    padding: "0 12px"
    height: "32px"
  button-ghost-hover:
    backgroundColor: "{colors.panel}"
    textColor: "{colors.fg}"
  button-danger:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.bad}"
    typography: "{typography.button}"
    rounded: "{rounded.none}"
    padding: "0 12px"
    height: "32px"
  button-small:
    typography: "{typography.button-sm}"
    padding: "0 8px"
    height: "24px"
  input:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.fg}"
    rounded: "{rounded.none}"
    padding: "0 10px"
    height: "32px"
  chip:
    typography: "{typography.meta}"
    rounded: "{rounded.none}"
    padding: "1px 7px"
  chip-need:
    textColor: "{colors.need-fg}"
  chip-run:
    textColor: "{colors.run}"
  chip-done:
    textColor: "{colors.done}"
  chip-ok:
    textColor: "{colors.ok}"
  chip-bad:
    textColor: "{colors.bad}"
  chip-idle:
    textColor: "{colors.idle}"
  panel-header:
    backgroundColor: "{colors.panel}"
    textColor: "{colors.fg}"
    typography: "{typography.label}"
    padding: "4px 12px"
    height: "32px"
  kbd:
    backgroundColor: "{colors.panel}"
    textColor: "{colors.muted}"
    rounded: "{rounded.none}"
    padding: "0 4px"
  top-bar:
    backgroundColor: "{colors.bg}"
    padding: "0 24px"
    height: "48px"
  bottom-tabs:
    backgroundColor: "{colors.bg}"
    height: "calc(56px + env(safe-area-inset-bottom))"
  task-row:
    padding: "8px 12px"
  drawer:
    backgroundColor: "{colors.bg}"
    width: "580px"
  drawer-wide:
    width: "680px"
---

# Design System: stage

## Overview

**Creative North Star: "The Ink Workbench"**

stage is a workbench, not a dashboard of cards. Every view is a stack of ruled, square-cornered panels on a white (light) or deep slate (dark) ground, each headed by a tinted bar with an uppercase mono label, each holding hairline-separated rows. The chrome is ink and hairline only: black-on-white primary actions in light mode, near-white-on-near-black in dark. Color is withheld until a task's state earns it, so the one yellow on screen is always the thing waiting on the user.

Density is high and deliberate: 14.5px body, 32px controls (40px on phone), 8-10px row padding, 14px between panels. Geist carries titles, actions and prose; JetBrains Mono carries everything that is metadata (project names, models, times, counts in rows, labels, chips, keycaps). The direction refuses the rounded-card SaaS dashboard and the neon-terminal costume: it reads like an instrument panel drawn with a ruler, lit only where attention is owed.

Motion is quiet: 150ms color, opacity and border transitions on state change, a pulsing skeleton while loading, a square spinner on busy buttons, and no entrance choreography.

**Key Characteristics:**
- Zero radius on every element, including chips, markers, spinners and focus outlines.
- 1px hairlines (11% ink) separate rows and regions; 20% ink outlines controls.
- Ink accent that inverts per theme; no brand hue in chrome (the mark's yellow top step is a needs-input marker, not brand color).
- Six state tones (need, run, done, ok, bad, idle) drawn as tinted borders, square markers and colored text.
- Uppercase JetBrains Mono panel headers on a light-gray bar.
- Drawers and full-screen sheets instead of modals.

## Colors

An ink-and-paper neutral system with a strictly semantic state palette; nothing else is colored.

### Primary
- **Ink** (`accent` / `accent-dark`): the primary action color. Black (#0b0d14) on white in light mode, near-white (#eef0f5) on deep slate in dark. Fills primary buttons (New task, Start, Open in T3 on a needs-input row), the active nav underline, the selected segmented option, the brand glyph, text selection, focus outlines and the history bar chart. Paired with `on-accent` for text on it.

### Neutral
- **Paper** (`bg`, `surface`): the ground and panel bodies. Both pure white in light; in dark the ground is #1a1d23 and panel bodies lift one step to #1f2229.
- **Bench Gray** (`panel` / `panel-dark`): panel header bars, drawer footers, keycaps, hover fills on secondary and ghost buttons, the palette footer.
- **Ink Text** (`fg` / `fg-dark`): all primary text and row titles.
- **Slate** (`muted` / `muted-dark`): metadata, labels, placeholders, empty-state copy, inactive tabs.
- **Hairline** (`line` / `line-dark`): row separators, panel borders, bar borders.
- **Strong Hairline** (`line-strong` / `line-strong-dark`): control outlines (inputs, secondary buttons, segmented groups, search trigger), drawer edge, the rule between tree levels, dashed not-started tree chips and cells.
- **Scrim** (`scrim` / `scrim-dark`): behind drawers, sheets and the palette.

### State
- **Waiting Yellow** (`need`, text `need-fg`): needs input, waiting threads, T3 pairing that waits on the user (unpaired, expired). The marker and border are the bright yellow; text uses the darker `need-fg` because the bright yellow fails as text on white.
- **Running Sky** (`run`): launching, running, queued, starting threads.
- **Settle Teal** (`done`): done (run finished) and awaiting settle in T3; T3 threads labelled `completed` read "done".
- **Settled Green** (`ok`): settled tasks and threads (a settled thread shows "settled" whatever its run status), T3 connected.
- **Fault Red** (`bad`): failed, interrupted, T3 unreachable or rejected, errors, danger buttons.
- **Idle Gray** (`idle`): backlog, cancelled, idle, rolled back.

### Named Rules
**The State Earns Color Rule.** Hue appears only on a state: a chip, a square marker, a stat count, an alert, the needs-input row edge, or the mark's top step while anything needs input. Chrome, navigation, links and primary buttons stay ink.

**The Two-Token Tone Rule.** Every tone sets a marker/border color and a text color. Borders mix the tone at 45-55% into transparent, fills at 8-14%; the need chip fills at 14% so yellow reads at a glance. Never invent a tone outside the six.

**The One Yellow Edge Rule.** User-approved: a task row in needs input carries a 2px Waiting Yellow left edge and makes its primary action "Open in T3". It is the only colored row edge in the system; every other row keeps a transparent 2px edge so titles align. The same edge marks a needs-input toast, an inbox row whose task or thread is still waiting, and the Needs input panel while something still waits on the user.

## Typography

**Display Font:** Geist Variable (with ui-sans-serif, system-ui)
**Body Font:** Geist Variable
**Label/Mono Font:** JetBrains Mono Variable (with ui-monospace)

**Character:** A tight, slightly negative-tracked grotesk for anything a person reads as a sentence or presses, set against a mono that marks everything machine-shaped. The split is functional, not decorative: if it came from T3 or a clock, it is mono.

### Hierarchy
Sizes are role tokens (`--fs-*` on `:root`, exposed as `text-label`, `text-meta-sm`, `text-meta`, `text-detail`, `text-small`, `text-ui`, `text-body`, `text-title`, `text-headline`, `text-tab`, `text-input*` utilities). Values below are laptop; below 768px every role steps up about 1px (body 15.5px, meta 13px, label 11.5px, title 17px, headline 24px). Never write a raw `text-[Npx]`.

- **Headline** (700, 26px on laptop, 24px on phone, 1.25, -0.025em): the page H1 only. Balanced wrapping, breaks long task titles.
- **Title** (600, 16px): drawer and sheet titles.
- **Row Title** (500, 14.5px): task and thread titles in rows; underline on hover.
- **Body** (400, 14.5px, 1.45, -0.005em): base text. Inputs, selects and textareas use `text-input*` (14px, mono 13px on laptop) and are 16px on phone so iOS Safari never zooms on focus.
- **Body Small** (400, 13px): alerts, details, empty states, button labels (500).
- **Stat** (700, 16px, tabular): stat strip counts.
- **Label** (JetBrains Mono 400, 11px, 0.12em, uppercase): panel headers (in Ink Text), field labels and section labels (in Slate).
- **Meta** (JetBrains Mono 400, 12px, 1.5 line): row metadata, chips, hints, timestamps. Small counts and panel meta sit at 11.5px (`text-meta-sm`), detail values at 12.5px mono (`text-detail`), read-only prompts at 13px mono with relaxed leading.

### Named Rules
**The Mono Is Metadata Rule.** JetBrains Mono for project names, models, times, ids, counts, chips, labels, keycaps; Geist for titles, actions and prose. Counts, times and step numbers use tabular numerals.

**The Meta Below Rule.** A page's context line sits directly below the H1 in 12px mono Slate, led by a bold Ink Text `//`. Nothing sits above a heading: no eyebrow, no kicker.

## Layout

A single centered column, max 1240px, with 24px side gutters on laptop and 12px on phone (24px top padding on laptop, 16px on phone). The 48px sticky top bar holds the mark, text tabs (active tab gets a 2px ink underline; Dashboard carries the needs-input count chip), the Ctrl K search trigger, the T3 status control, the theme toggle and the primary New task button. Below 768px the top bar compacts to 52px with 40px controls and a 56px five-cell bottom tab bar (Stage, Trees, Projects, Sessions, More; More holds Schedules, History, Snippets and Settings) takes over navigation, with a square yellow marker on Stage when anything needs input; page content reserves 96px of bottom padding plus the safe-area inset for it.

Pages stack: PageHeader (H1, `//` meta line, actions right-aligned, 16px below), then panels separated by 14px. The Dashboard puts a one-row stat strip (16px below the header) above the groups in fixed order: Needs input, Trees, Running, Completed, Failed, Ready, Backlog, Scheduled, Settled (Trees and Scheduled have no stat cell). Consecutive empty groups collapse into one ruled list with the label left and the empty sentence right. Detail pages split at 1024px into a fluid main column and a 340px side column (300px on History).

Rows are grids: 118px state column, fluid title (task rows lead it with the task number), optional row chip ("needs #3 #5", a tree, a schedule, "by agent"), right-aligned actions on laptop; on phone the title leads, the state chip floats right and actions wrap below. Spacing steps are 4, 8, 10, 12, 14, 16, 24px.

### Named Rules
**The Ruled List Rule.** One bordered panel per state or concern, rows separated by hairlines. Never a grid of cards.

**The Drawer Not Modal Rule.** Secondary flows (new task, edit, filters, More) open in a right drawer, 580px or 680px wide, that becomes a full-screen sheet below 768px, with Esc, a focus trap and focus restore.

**The Safe Area Rule.** The page sets `viewport-fit=cover`, so anything pinned to a screen edge on phone pads itself with `env(safe-area-inset-*)`: the phone top bar (top), bottom tabs, task detail action bar and toasts (bottom), and every drawer sheet and the command palette (header top, footer or body bottom, sides in landscape). Primary actions never sit on the home indicator.

## Elevation & Depth

Flat. Depth comes from tone and line, not shadow: a Bench Gray header bar over a Paper body, hairline borders, a one-step-lighter surface in dark mode, and a scrim behind overlays. The single cast shadow belongs to the command palette, the one centered floating overlay on laptop. Selection and focus use inset bars and rings, never lift.

### Shadow Vocabulary
- **Palette overlay** (`box-shadow: 0 16px 48px rgba(0,0,0,0.28)`): command palette on laptop only.
- **Selected stat bar** (`box-shadow: inset 0 -2px var(--c)`): the active stat strip cell, in its tone.
- **Active palette item** (`box-shadow: inset 2px 0 var(--accent)`): the highlighted palette result, over a 7% ink fill.
- **Input focus ring** (`box-shadow: 0 0 0 1px var(--accent)`): with an ink border on focused inputs and selects.

### Named Rules
**The Flat Bench Rule.** Surfaces never cast shadows. Only the floating palette may; everything else separates by hairline and bar tint.

## Shapes

Every corner is square (0px): buttons, inputs, chips, panels, drawers, keycaps, skeleton bars, the busy spinner, and state markers. Borders are always 1px; the only 2px strokes are the active-tab underline, the stat selection bar, the focus outline and the needs-input row edge. State markers are 6px squares (7px in alerts, 8px on the phone tab); the brand mark is a 16px stepped-bars glyph (see Brand mark). Tree panel cells are bordered boxes; not-started (backlog) members use a dashed border (cells and tree view state chips), the viewed member adds a 1px Ink Text outline offset 1px, the selected tree view row a 1px Ink Text outline inset 2px.

Icons are authored 16px SVGs on a 16-unit grid: 1.5 stroke, square caps, miter joins, currentColor, so they share the sharp geometry of the grid.

### Named Rules
**The Zero Radius Rule.** No element rounds a corner. A circle is never a state marker; the marker is a square.

## Components

### Buttons
Compact, flat, and ink-led.
- **Shape:** square (0px), 1px border, 32px tall with 12px side padding; small is 24px with 8px padding and 12px labels. On phone (below 768px) default is 40px with 14px padding and small is 32px, for thumb-sized targets.
- **Primary:** Ink fill and border, `on-accent` text, 500 weight. One per region: New task, Start/Restart, Open in T3 when the task needs input, drawer submit.
- **Hover / Focus:** primary drops to 85% opacity; secondary and ghost fill Bench Gray; 150ms transition; 2px ink focus outline offset 2px. Disabled at 45% opacity. Busy shows a square spinning border.
- **Secondary:** Paper fill, Strong Hairline border, Ink Text. The default.
- **Ghost:** transparent border, Slate text; for close buttons and panel-header actions.
- **Danger:** Paper fill, Fault Red text and 45% red border, 8% red hover fill; Delete only, pushed to the far right (an Archive or Unarchive secondary sits just before it).

### Chips
- **Style:** inline mono label (`text-meta`, 1.5 line, 1px 7px padding) with a tone-tinted 1px border, a faint tone fill and tone text. The `dot` variant prefixes a 6px square marker.
- **State:** task and thread states map onto the six tones; plain chips (Slate border, Ink Text) carry model, runtime, workspace, tags, `tracked session`, `manual`, needs and the tree link.
- **Pull request:** a PR's state is a state, so it takes a tone, never a seventh: open Running Sky (in flight), merged Settled Green (landed), closed, draft and any unknown state Idle Gray (an unmerged close reads like cancelled, not a fault). The row chip is the `dot` variant labelled `repo#num` (repository name without the owner, tabular), `owner/repo #num · state` in its title, and it opens the PR in a new tab.

### Cards / Containers (Panel)
- **Corner Style:** square.
- **Background:** Paper body, Bench Gray header bar (min 32px, 4px 12px padding) with an uppercase mono label in Ink Text, mono meta count beside it, actions right.
- **Shadow Strategy:** none (Flat Bench Rule).
- **Border:** 1px Hairline outside, Hairline under the header.
- **Internal Padding:** 12px; flush for row lists.

### Inputs / Fields
- **Style:** 32px (40px on phone; small selects 24px, 32px on phone), Paper fill, Strong Hairline border, square, 10px padding, 14px text (16px on phone); textareas switch to 13px mono (16px on phone). Selects and comboboxes carry an authored Slate chevron (`.select-chevron`, per-theme `--chevron`). Enabled buttons, selects and labels show a pointer cursor; disabled ones `not-allowed`.
- **Focus:** border and a 1px ring both go Ink.
- **Error / Disabled:** errors render as a red Alert below; disabled at 55% opacity. Labels sit above in the Label style.

### Project combobox
Every project picker (task form, snippet scope, Dashboard filters) is a type-to-filter combobox that looks like a select at rest: same height, border, chevron and focus ring. Focus or click opens the list with the current label selected, so typing replaces it. Matches are case-insensitive substrings of the title or workspace path, ranked title prefix, title substring, path only; ties and the empty query sort by recent activity. A pinned first option ("All projects", "Global") leads while the query is empty. The list drops under the input (left-aligned, at least 280px, right-aligned when it would overflow), at most 344px tall, and flips above near the bottom of the visible area, including over a phone keyboard. Flat (Strong Hairline, Paper, no shadow); rows show the title at 13px over a 1-line mono Slate path, matched text bold, archived projects with a plain chip; 44px+ rows on phone. The active row gets the palette's 7% ink fill and 2px inset ink bar; the laptop footer shows keycaps (↑↓ move, ↵ select, Esc close). Enter selects and never submits the form; Esc closes the list (not the drawer) and restores the label, as do Tab and blur. No match shows a muted "No matching projects" line.

### Segmented control
Joined mono buttons inside one Strong Hairline border, hairline dividers between options; the pressed option fills Ink. Default is 32px tall overall (40px on phone), matching buttons and inputs it sits beside; small (compact controls) is 26px (34px on phone). Used for runtime mode, time window, theme, project sort/archive filter.

### Brand mark
Three stepped square bars, widest at the bottom, on the 16-unit grid; no rounded corners, no circle.
- **Top bar glyph** (both bars): 16px, bars 3 units tall with 1-unit gaps (x 1/3/5, widths 14/10/6) in `currentColor`: bottom solid, middle at 72%, top step at 44%. While anything needs input the top step fills Waiting Yellow (`need`), 150ms fill transition, none with reduced motion. Followed by the bold `stage` wordmark, 8px gap. Decorative (`aria-hidden`): the Dashboard count chip and the phone Stage tab marker announce the state.
- **Tab favicon:** `favicon.svg` (idle: full-bleed #0b0d14 tile, white bars, top step at 44%) swaps to `favicon-need.svg` (top step #e3c341) while anything needs input. Bars are 2 units tall with 2-unit gaps so they hold at 16px. Fixed ink tile in both themes.
- **App icons:** `logo.svg` is the brand source: square #0b0d14 tile, white bottom bar, middle at 72%, static #e3c341 top step (outside app chrome, so the yellow is brand, not state). Bars sit inside the maskable 80% safe zone; the OS applies its own mask.
- **Raster provenance:** every PNG in `apps/web/public/icons` is generated from `logo.svg` (run in `apps/web/public`): `magick -background none -density 96 logo.svg -resize 192x192 PNG32:icons/icon-192.png` (same at 512 for `icon-512.png`); `magick -background '#0b0d14' -density 96 logo.svg -resize 512x512 -flatten -alpha off PNG24:icons/icon-maskable-512.png` (same at 180 for `apple-touch-icon.png`). The desktop app icon (`desktop/app/icon/icon.png`, 1024px, and `icon.icns`) is rendered from the same `logo.svg`; the commands are in [docs/desktop.md](docs/desktop.md#app-icon).

### Navigation

- Laptop: Stage, Trees, Projects, Schedules, Snippets, Settings. Smaller widths fold the final three into More.
- Phone: four equal tabs, Stage, Trees, Projects, More. More contains Schedules, Snippets, Settings and theme. Keep existing sizes, borders and active indicators.

### Pull to reload
On phones and tablets, in browser and installed PWA, a single-finger downward pull from the document top reloads the current URL when released at 80 CSS pixels. Below that distance it cancels. Controls, focused editors, nested scrollers, drawers and the palette keep their gestures; horizontal intent, multitouch and navigation cancel the pull.

A downward pull moves page content with increasing resistance, bounded below 112px; the 80px trigger reveals 56px of space beneath the header. Header, bottom tabs and fixed task actions stay anchored. A single pointer-transparent, square-cornered Paper/Strong Hairline indicator sits below the active header. Geist copy changes from “Pull to reload” to “Release to reload”, then “Reloading…” with the square busy spinner. On release or cancellation, content settles back over 180ms; the reloading indicator remains visible until navigation completes. State changes announce politely; reduced motion skips the settling animation and keeps the spinner still. Full reload clears unsaved drafts and requires connectivity.

### Stat strip (signature)
One bordered row of state counts that doubles as the Dashboard filter (`?state=`). Each cell shows a square tone marker (only when the count is non-zero), a bold tabular count and a mono label; zero counts go Slate. The selected cell fills its tone at 10% with a 2px inset bottom bar and colors the count. Cells: Needs input, Running, Awaiting settle, Failed, Ready (idle tone: backlog tree members whose needs settled, waiting for Start), Backlog (untimed backlog not ready), Settled. Counts include tree members even where their rows roll up into a tree row; a group whose members roll up says so in its header meta (`4 · 3 in trees`), and an otherwise empty group reads "3 in trees, shown in the Trees group.". Laptop cells sit left with a "click a count to filter" hint right from 1280px (hidden below so seven cells fit); phone cells split the width evenly with short labels, and the phone strip omits Settled (six cells fit 360px; Settled stays reachable from its collapsible group). The last group, Settled (last 7 days), renders collapsed behind a chevron header (`aria-expanded`); filtering to it expands it. It interleaves tasks, external T3 threads (subagents excluded) and completed trees (Tree rows, all members settled in the window) in one list, newest settle first; its count covers tasks and threads, and a completed tree's members leave the list for its row.

The Dashboard's Trees group (after Needs input, before Running; no stat cell, hidden while empty) lists active trees as Tree rows. Members of a listed tree leave Running, Done, Failed and Backlog; members that need input or are ready also keep their own row in Needs input or Ready, carrying a tree chip.

### Task row (signature)
State chip (backlog tree members read `ready` or `blocked`, idle tone), row title with a mono meta line (project, model, relative time, tags), one optional plain row chip, and right-aligned small actions. The row chip is, in order: `archived`, `tree done` or `tree archived` (plain; the tree icon for the tree ones, linking the tree) on a closed row (Project detail Closed and All views); the start time while timed; on the Dashboard, the tree (authored tree icon and the tree title, truncated at 22ch, linking `/trees/:id`) for needs-input and ready breakouts; "needs #3 #5" while blocked; the schedule; "by agent". Blocked rows get no Start (Start early lives on task detail and the tree view). The task number leads the title as an unlinked mono 12px Slate tabular `#51` (6px gap, never wrapped apart), so needs chips ("needs #55") read against the rows they name; it sits outside the title link so the hover underline stays on the title. A settled row's meta line shows the settle time, not the launch time, followed by the elapsed run time: `settled 3h ago · ran 35m` (exact times on hover; `ran` is omitted until the run span is known). Needs-input rows take the One Yellow Edge; Open in T3 is the primary action. Last errors print as a truncated red mono line under the title.

On the Dashboard (every state group, external needs-input threads included) the project gets its own column: laptop rows put a fixed 150px project cell right after the 118px state chip (mono 12px, Ink Text, medium weight, linked, truncated with the full name in `title`) so project names align down the whole page, and the project leaves the meta line. Phone rows print the project as a mono 12px Ink Text line above the title. The column stays when the Dashboard is filtered to one project; Project detail rows keep the project in the meta line.

Closing work: the Dashboard never lists closed work (archived tasks, done or archived trees and their members). Project detail's Tasks panel header carries a small `Open | Closed | All` segmented control (Closed: done or archived) beside the state select. Task detail's action row ends with Archive (secondary; standalone tasks only, disabled with "Stop the task first" while active) or Unarchive when archived, then Delete (danger, far right) only while the task has never started; an archived task adds a plain `archived` header chip. The palette lists closed tasks and trees too, each with a plain chip (`archived`, `tree done`, `done`).

### Last word
A session's latest assistant reply, a read-only glance (T3 stays the place to talk).
- **Row preview:** needs-input and done rows only (task rows, external thread rows, session rows). One line of Geist 13px Slate prose with an ellipsis, full preview in `title`, no marker or label. Laptop: under the meta line in the title column. Phone: its own full-width line between the title block and the actions, so it is not squeezed beside the state chip.
- **Panel:** "Last word" Panel right under Prompt in the main column on task detail (above Created tasks). Header meta is the reply's relative time (mono, exact time on hover), plus "· earlier run" when the reply is not from the thread's latest run; Copy in the header. The body renders sanitized markdown (`.prose-reply`): Geist 14.5px at 1.55 leading, 75ch measure, headings at body or title size (never competing with the page H1), square Slate list markers, inline code and code blocks in mono on the Bench Gray panel with hairline borders (code blocks scroll sideways, prose wraps anywhere), hairline tables and rules, and a 1px Strong Hairline quote rule. Raw HTML shows as literal text. A truncated excerpt ends with a hairline-ruled mono footer, "Excerpt. Full reply in T3", linking to the thread. Hidden until a reply has been read.

### Prompt

First task-detail panel, above the final reply. Preserve mono text, Copy, six-line clamp and Show all / Show less. Ordinary tasks retain launch notes, images and handoff. Adopted tasks read only their first own T3 user message, skipping inherited history, with bounded full-text expansion. Keep loading, empty, offline and retry states.

### Transcript

Conversations open in T3. Task detail contains prompt, final reply and journal, without a conversation tab or composer.

### Alert
Tinted callout: tone border at 50%, tone fill at 9% over surface, a 7px square marker, 13px text, actions right. `need` for waiting-on-you, `bad` for errors.

### Start control and schedules
The task drawer's Start segmented control (Now / Backlog / At time / Repeat; Backlog / At time when editing a backlog task; Backlog only for a new task joining a tree with needs, Now / Backlog / At time for a new tree root (schedules take no tree); hidden when editing a task with needs; there is no After task: order lives in trees) decides what the single primary footer button does, and its label follows: "Save & start", "Save to backlog", "Save · starts Nov 12 02:00", "Save schedule". At time shows date and time inputs, a zone select (browser default) and a mono "starts in" line. Repeat shows a preset segmented control (Hourly, Daily, Weekdays (Mon-Fri on phone), Weekly, Cron) with its minute, time, weekday or raw cron field, a zone select and a Bench Gray mono box with the summary and the next three fires from the server. On phone the segmented controls fill the width in equal cells with short labels.

A backlog tree member (or a new task joining a tree from the tree view's Add task, whose drawer opens with a plain Alert "Joins tree X, needing #3. It waits in backlog until it settles.") shows a `TREE START` select: "Inherit (tree: auto)" (names the tree's mode when known), "Auto: starts when its needs settle", "Manual: reads ready, you start it". Inherit saves as no override.

Schedule rows share the task row grid: a plain "repeat" chip (idle "paused" when paused) with the authored repeat icon, title, mono summary, zone and "next in", then Run now and Pause/Resume. The last run is plain meta ("last #131 finished") unless it failed, was interrupted or blocks the next fire: then a "last #208" state chip fills the column before the actions (phone: the state word, tinted, in the meta line), and a blocking run adds a tinted "skips while #208 runs" or "skips until #214 is answered" after "next in" (not while paused). A healthy schedule row carries no state color. A timed backlog task shows a plain chip with the authored clock icon ("at 02:00") in the row chip slot and its Start reads "Start now"; a spawned task shows a plain repeat chip linking its schedule. Stage adds a "Scheduled · next 24h" group between Backlog and Settled (timed tasks leave Backlog for it); it has no stat strip cell. Schedule detail: Run now (primary), Pause, Edit, Delete (danger, inline confirm); Runs in the main column, When / Config / Prompt panels in the side column.

### Tree view
`/trees/:id`: H1 tree title, then an Ink Text mono `// Project: stage` line (`Projects: alpha · beta` when spanning projects; sorted distinct current-member project names, wrapping without truncation; `No member projects` when empty), then `7 tasks · 3 levels · 2 projects · auto · created Nov 14 · active 6m ago` (created is a date anchor, `today 10:20` / `yesterday 16:20` / `Nov 14` / `Oct 3, 2025`; active is relative in Ink Text; exact times on hover; a `settled` chip once every member settled; a plain `done · 2 not run · 1 cancelled` or `archived` chip once closed), actions right: Auto | Manual segmented (tree start mode), More (popover: Rename (inline form above the H1), Add existing task, Add session, a Strong Hairline divider, Mark done, Archive tree, Dissolve tree in red, Delete tree in red only when the server permits deletion; Dissolve and Delete open an inline bad Alert confirm, "Delete this tree and its 4 tasks? None has started. This can't be undone."; Mark done and Archive apply at once and, while a member is launching, running or needs input, sit disabled with a mono Slate reason under the label, "#4 is running"), Add task (secondary; "Add task after #4" while a node is selected, opening the New task drawer prefilled with the tree and that need), Start ready `n` (primary, disabled at 0). Below lg the actions wrap under the meta line. A done or archived tree drops Auto | Manual, Add task, Start ready and the structural menu items (and the status it already has); a secondary Reopen (or Unarchive) takes Start ready's slot, a mono Slate line under the header reads "Marked done Nov 16. Remaining tasks won't start." (or "Archived Nov 16. ..."), graph rows lose Start and Retry (Open in T3 stays), and the side panel's node controls read only ("Reopen the tree to change needs.", no Remove from tree). The state change in place is the feedback: no toast.

- **Graph list:** the `TREE` panel is one ruled row per member in level order (roots first; a level orders by the mean position of its needs, then id), a Strong Hairline rule between levels. Laptop columns share one subgrid: level (Slate mono `L2`, first row of a level only), state chip (`NodeChip`; ready / blocked idle, dashed while backlog), mono `#id` and the title (Geist 500 14px, truncated; Ink Text mono project on its own line beneath the title for every task, including single-project trees and phones; project names wrap anywhere), needs (mono `#3 #5`, a failed need in red with "blocked by failed #5" on hover; `root` for a root), time in state right-aligned (Ink Text mono; Waiting Yellow text while it needs input), then one action: Open in T3 (needs input) > Start (ready; a blocked node's Start arms "Start before #3, #5 settle", which keeps its needs) > Retry (failed) > Open in app. A Paper column header row (Slate Label) tops the list. Rows are at least 52px and grow for wrapped project names; phone rows print `#id` title, then project, then chip · `needs #3` · time in state · action right, with no level label.
- **Time in state:** "running 18m", "waiting 6m" (needs input), "done 22h ago", "settled Nov 15" (date anchor), "failed 3h ago", "ready 3h", "queued 2d" (blocked or plain backlog). Counted from the task's `stateSince` (latest event into its state); exact time on hover, plus `ran 35m` for done and settled.
- **Row states:** needs input takes the One Yellow Edge (inset, so columns stay aligned); the selected row a 1px Ink Text outline inset 2px.
- **Layout:** a small List | Graph segmented control sits in the `TREE` panel header (List by default; Graph at `?layout=graph`, kept next to `?view=notes`). There is no hover effect on rows. The panel meta reads "click a task to edit" (list, laptop), "steps run top to bottom · click a task to edit" (graph, laptop) or "tap a task to open it" (phone). On lg+ the title selects the row or box; below lg it opens the task.
- **Graph:** a top-down layered graph in the same level order as the list. Each level is a band with an 88px step gutter: Ink Label `STEP 2`, then a mono line `now` (Sky; any member launching, running, needing input or ready, and the band takes a 5% Sky tint), `done` (every member settled) or `waits` (Slate), plus `3 parallel` when the level has several members; Hairline between bands. Boxes are 224x80, flat Surface with a Strong Hairline (Sky while running or launching, dashed while backlog, One Yellow Edge while it needs input, the selected box the inset Ink outline): `NodeChip` and time in state (mono, Waiting Yellow while it needs input) on top, mono `#id` and the title (Geist 500, truncated, full title on hover) below, then the Ink Text mono project on a third line for every task (truncated with the full name on hover and included in the accessible task label; the tree header retains the full names); no actions (they stay in the list, side panel and task detail). Arrows run from each need's bottom centre to its dependent's top centre: solid Ink 1.5px once the need settled, dashed Slate while it has not, dashed Red when it failed. Rows centre on the widest level; wider graphs scroll horizontally (phone included). Boxes sit in the DOM in level order, so Tab follows the execution order.
- **Side panel:** lg+ only, 340px. With nothing selected it is the tree's `ACTIVITY` (below). The selected node (`TASK #4` header with a ghost close back to Activity): its chip, title link and project, then the Tree node controls (see Tree panel), Open task and Remove from tree.
- **Activity:** `ACTIVITY` reads the durable event journal, newest first, twelve per page with Load older entries. Ruled rows use mono timestamps, immutable entry links, event labels and task/source-session links; moving events link both trees. Task links open task detail even after a member leaves. Compact text expands inline; truncated entries fetch the full record. A muted history-start notice explains that tree events begin at journal rollout and earlier task history remains on each task. Below lg it sits under the `TREE` panel.
- **Views:** Tasks | Notes below the header, Tasks by default, Notes at `?view=notes`. Tasks retains the graph and side panel. Notes fills the content width with the shared journal. The note draft survives switching tabs.
- **States:** five pulsing skeleton rows while loading; only a deleted or unknown tree is a 404 page ("Tree not found", Back to trees). Empty and dissolved trees retain their archived identity, notes and history. Dissolve detaches tasks, archives the tree, and stays on its page. A completed tree shows the settled chip; a done or archived tree its plain chip. Delete is shown only when server eligibility permits it, including protection for recorded work by departed members.

### Journals
Tree Notes and the task detail main column share one ruled `NOTES` panel. The composer leads with a snippet-aware textarea, an optional category select (Uncategorized, Finding, Decision, Blocker, Outcome) and one primary Append note button. Tree composition defaults to This tree, with current tasks available in a Save note to select; task detail saves to that task. A short hint explains ownership: task notes move with the task, tree notes stay. Closed work accepts notes without reopening or scheduling. Notes are append-only, with a 16 KiB UTF-8 byte limit and an inline byte count. Submission failures preserve the draft and retry key; only success clears it. A task that leaves during composition requires choosing the destination again.

Below the composer, Notes | Events | All defaults to authored notes. A category select (All categories, Finding, Decision, Blocker, Outcome), literal text search and (tree only) task-ID filters apply with Filter; Clear resets them. Entries are newest first, twenty per page, with Load older entries. Each row shows its permanent entry link, timestamp (exact on hover), category or event label, owner, attempt when present, and available source task/session and moved-tree links. Markdown uses the existing sanitized renderer. Previews are bounded to 600 runes; Read full entry fetches the immutable record and Show preview collapses it. Deep links use `?entry=<id>#journal-<id>` (plus `view=notes` on trees) and load that entry even outside the current page or filters.

Loading uses pulsing bars; errors keep the composer mounted and offer Retry. A stale tree cursor explains the membership change and offers Reload entries from the first page. An empty filtered list invites a note or a filter change. The event view identifies the start of tree history. New task and adoption drawers offer Initial note; task editing has no mutable notes field. Schedules keep Notes template, copied into each spawned task as its initial journal note. Duplicates do not copy journal entries.


`/trees` lists trees as Tree rows under an Active | Done | Archived | All segmented control (Active: open with a member not settled; Done: marked done, or every member settled; Archived; All), most recent activity first, with an empty line per view.

### Tree row
A tree in a ruled list (Trees page, Dashboard Trees and Settled groups). Laptop grid: title (Geist 500 body, linked) with an Ink Text mono `Project: stage` line under it (`Projects: alpha · beta` for several, sorted distinct current-member names, wrapping anywhere; `No member projects` when empty), then the Slate `treeMeta` mono line (and a settled chip with its time once complete; a plain `done · ...` or `archived` chip, closed time on hover, once closed, whose only action is a secondary Open), a time column (lg+, right-aligned mono: `active 6m ago` in Ink Text over `created Nov 14` in Slate; the list's sort key and the tree's age, exact times on hover), a 240px state column, a 128px action column. Below lg the times print as one mono line under the meta (`active 6m ago · created Nov 14`). The state column is the segmented state bar over a mono line: the next step left ("1 needs input" in need text, "2 ready", "1 failed" in red, "2 to settle in T3", "3 running", "waiting on needs", "all settled") and `3/7 settled` right. Action: View tasks (primary, opens the tree) when a member needs input, "Start ready n" (primary small, starts every ready member) when any is ready, else a secondary Open. Phone: title and action on top, the bar full width below.

Segmented state bar: an 8px tall row of square cells with 1px gaps, one cell per member (one stretch per state above 30 members), finished work first: settled (ok), done, running, needs input, failed, ready (idle fill), not-started backlog (hollow, Strong Hairline outline), cancelled (idle at 45%). One `role="img"` label carries the counts ("7 tasks: 1 settled, 1 running, ...").

### Tree panel
Task detail puts a `TREE` panel at the top of the side column (where Run order sat) for a tree member, ghost "Open tree" in the header. Body: the tree title (linked, with the plain closed chip when done or archived; then its node controls read only) with its mono meta and a second mono line `created Nov 14 · active 6m ago`, then the mini level view: one row of cells per level, each a 24px (32px on phone) Strong Hairline cell with a 6px tone square and mono `#id`, dashed while backlog, linking the task; the viewed task gets the Ink outline. Under it the shared Tree node controls, each a hairline section with a Label header:
- `NEEDS · n`: one line per need (`#id` title, its chip, a ghost remove while backlog) and a cycle-safe "Add a need…" select; a root says "None. It is a root: it starts when you start it." Outside backlog: "Needs change only while it is in backlog."
- `DEPENDENTS · n`: one line per dependent with its chip.
- Backlog only: `START MODE` (Inherit / Auto / Manual segmented, mono line naming the tree's mode and what it means), `WORKTREE BASE` (only with needs; see Predecessor base), the Hand off context checkbox row (only with needs).
- Footer: Remove from tree (danger small, inline confirm naming what happens to its dependents).
Lines open the task here (they select the node in the tree view). A task outside any tree gets a `TREE` panel reading "Not in a tree. Add it to one so it can need other tasks, or be needed." with a small "Add to tree" in the header (an archived task reads "Not in a tree. Unarchive it to add it to one." without the button): a drawer with a radio list (New tree first, then active trees with their meta); New tree adds a title (the task's title) and an Auto | Manual start mode; the task joins as a root. An adopted task shows a plain `tracked session` chip in the header chips, has no Start or Duplicate, and its Prompt panel reads the session's first message from T3.

### Handoff
A task's handoff lives in its Prompt panel, because it is part of the message sent; one section per need. Backlog with handoff on: one mono Slate line under the prompt, "+ on launch: handoff from #4, #5 (final reply, PRs, branch, thread link)". Launched: one hairline-ruled section per need under the prompt (and images), each with a Label `HANDOFF FROM #4`, mono Slate `sent HH:MM` (exact time on hover), a small "Copy handoff" button right, a mono char-count line, and a collapsed native `<details>` ("Handoff text") opening the block in the same mono pre as the prompt (max 384px, scrolls). A skipped need prints one mono Slate line, "Handoff skipped: <reason>". No color: it is context, not state. The panel's Copy still copies the prompt only. The Hand off context checkbox row sits in the Tree panel (and the tree view side panel) while backlog, and in the task drawer when editing a task with needs.

### Predecessor base
A task with needs can start its worktree from a need's branch. The Tree panel (and tree view side panel) `WORKTREE BASE` select reads "Stable base (origin/main)" or "#12's branch" for each need in the same project (another project's branch cannot be used), saving at once while backlog; a mono line explains the fallback ("A need's branch stacks on its unmerged work; falls back to origin/main.", or that its needs run in other projects). A root workspace says there is no base to pick. The task drawer's Workspace segmented control keeps a third cell, "#12's branch", for a task with needs. With it selected, Base ref reads "Fallback base ref" with the hint "Used when #12 has no usable branch."; Branch is unchanged. Task detail: the Config Workspace line reads "worktree from #12's branch · fallback origin/main" before launch, then "worktree from feat/api (#12)" or "worktree from origin/main · fallback" with a mono Slate "fell back: <reason>" line under it; the header chip follows; a backlog task adds "+ on launch: worktree from #12's branch (fallback origin/main)" under the prompt. No color: it is config, not state.

### Pull requests

Historical artifact links are a plain list on task detail, with no live PR state or badges.

### Runs panel

Task run span remains in metadata. Detailed harness runs are opened in T3.

### Sessions

Explicit Track session drawer from Stage and Project; Add session from a tree. Paste a T3 link or ID, Preview session, inspect title/project/state, then confirm. Optional title and initial note for a new owner. Show actionable eligibility and lookup errors in place; keep input during retries. No session list.

### Session batch (Created tasks)

Task detail has a compact Batch panel and View batch drawer (`?batch=1`). The drawer groups tasks by tree, then independent tasks. Retain Start and Edit, selection order, and New tree from selection with Parallel or Chain. By-agent row links open that task's batch. Closed or moved tasks retain their identity and current grouping.

### Image dropzone

Backlog task prompts accept image drops and Add image. Preserve thumbnails, upload/error states, the 8-image limit and read-only image links on launched tasks. No conversation composer.

### Notifications bell
A secondary 32px button (40px on phone) in the top bar (between search and the T3 status control on laptop; before search on phone) with an authored bell icon. Unread count sits inside as a mono chip: plain, or need-toned when an unread needs-input item is still waiting. Zero unread shows the bell alone, square.

### Needs input panel

Keep a compact yellow-edged needs-input alert with Open in T3 and Stop. Native T3 handles questions and permission approvals. Notifications retain live waiting state and offer Open in T3, without a question form.

### Checkbox
16px square native checkbox: Strong Hairline border on Paper; checked fills Ink with the authored check in On-Accent; focus-visible adds a 1px Paper gap and 1px Ink ring; disabled at 55%. Used in a ruled list (Settings Notifications events) and as the standalone "Hand off context" row (Tree panel, tree view side panel, task drawer): whole row is the label, 44px tall on phone (32px laptop), Bench Gray hover, checkbox, 6px square state marker, Geist label, mono Slate hint right.

### Settings notifications
One panel, two sections split by a hairline. `EVENTS · all devices`: the global event filter (Needs input, Failed, Ready, Done, Settled; Ready in the idle tone, hint "needs settled, start it"), each toggle saving at once (optimistic; a failed save reverts and shows a red Alert); a muted line explains that unchecked events are dropped, or reads "All events off. Nothing is recorded or pushed." `THIS DEVICE`: per-device enable, test and disable; the header chip shows this device's state. In the browser it is Web Push ("Web Push to this browser for the checked events.", success "Test notification sent."). On desktop the same section, chip and buttons drive native notifications: intro "System notifications from the Stage app for the checked events, when Stage is not in front."; unsupported reads "This system has no notification service Stage can use."; blocked is a plain Alert, "Notifications from Stage are turned off in system settings. Allow them there, then try again.", with small Open System Settings (when the host can open them) and Try again; on adds a ghost "System settings" after Send test and Disable; when the host cannot open its settings, a muted "Allow notifications for Stage in your desktop's settings." follows; success reads "Test notification shown." No new components, tokens or colors.

### T3 status
A square secondary control (32px, 40px on phone) in both top bars, linking to Settings. Only the icon carries hue: an authored green check when connected, the authored chain link in `need-fg` when unpaired or the pairing expired (pairing waits on the user), a red X when T3 is unreachable, rejected the pairing, or the status call fails, a 6px Idle Gray square while checking. Its `aria-label` names the state ("T3 not paired", "T3 unreachable"). On pointer devices, hover or keyboard focus reveals a 280px details card right-aligned 4px under it: flat Paper, Strong Hairline, no shadow, a Bench Gray "T3 CONNECTION" header with the state chip (`connected`, `unpaired`, `expired`, `unreachable`, `rejected`, in the same tones), mono rows for T3 (`controlbox · v0.1.9`), URL, last full sync and token expiry (`expired 13h ago` once past), the probe or last error in red (2 lines max) when present, and a muted "Click to open settings" footer. 150ms opacity fade, none with reduced motion. Touch devices never show the card; a tap goes to Settings.

### T3 setup
Connection recovery appears above the Dashboard without replacing planning.
Use the existing compact Alert and inline pairing/retry actions in every connection
state, including before first sync. Settings remains the connection destination.

### Project workspaces
Title-only creation works offline. Project detail uses the existing ruled Workspace
panel for binding status, rename/archive and explicit unbind confirmation.
Bind workspace opens the shared Drawer, full screen on phone. Show external title,
full wrapping mono path and any existing Stage owner. Selection requires explicit
confirmation; never preselect by title or path. Register existing directory is a
separate action. Missing/changed bindings show a concrete repair action.
The task form offers Save to backlog when launch needs a binding.

### Notifications drawer
Right drawer (full-screen sheet on phone), one row per task or external thread across the last 30 days. Current needs-input groups lead in their own ruled panel; other groups sit under Recent · Today / Recent · Yesterday / Earlier (Label on Bench Gray), newest notification first. Titles lead with the current-state chip right, a mono project/latest-notification time line below, then an updates disclosure and Open in app. Current needs input alone earns the yellow edge and primary Open in T3 action. Chips reuse the shared task-state colours: yellow needs input, sky running/launching, teal done, green settled, red failed/interrupted, gray inactive. Unknown current state reads unavailable; a changed or unavailable state labels its historical event separately as Last notification. A `ready` notification (a manual tree task whose needs settled; idle tone) whose task is still in backlog reads as a `ready` chip (not "backlog", and no "Last notification" line) and gets a primary small Start (starts the task; errors show as a red mono line).

Expanding updates reveals a newest-first vertical notification history: hairline connector, square semantic markers, coloured mono event labels, and relative times with exact timestamps on hover. Older updates page within the row; loading uses skeletons and errors offer Retry history. History includes the recorded events (needs input, ready, done, failed, settled). Expansion survives live state changes. Unread rows show a 6px Ink square before a 600-weight title; opening the drawer reads everything through the global newest event ID, but the markers stay until it closes. Arrivals while open become read on close. Load more pages task/thread groups after server aggregation. The footer notes when push is off on this device (desktop: "Notifications are off on this device").

### Toast
In-app notification while the app is visible: bottom-right stack, 360px wide on laptop; full width above the bottom tabs on phone. Flat (Strong Hairline border, Paper surface, no shadow), fades in over 150ms (no movement; none with reduced motion). Event chip and a ghost close on top, title (2 lines max), mono project and time, then View and Open in app (a ready toast whose task is still in backlog leads with Start). Needs-input toasts take the yellow edge and stay until acted on; the rest (ready, done, failed, settled) dismiss after 8s, paused on hover or focus. At most three; older ones collapse into a mono "+n more · open notifications" strip that opens the drawer. Events that arrived while the app was hidden never toast; the bell count covers them. While the drawer is open, toasts are off.

### Snippet row
On the Snippets page, one panel per scope (Global first, then each project with snippets), with an "Expand all / Collapse all" ghost action in the header. Each row: a 20px chevron button (`aria-expanded`, rotated -90° when collapsed), the name as Row Title, and a 2-line mono Slate preview. Expanded, the full body prints at 13px mono with whitespace kept, followed by a small Copy button. Edit and Delete sit right on laptop and wrap below on phone.

### Snippet picker
Typing `/` at a word start (or `template:`) in the task prompt, notes opens a list of matching snippets. Matches are global plus the task's project. On laptop it sits under the caret, 360px wide, and flips above near the viewport bottom. On phone it drops 4px under the line holding the `/`, as wide as the field, and never flips: its top stays fixed while the query is typed and moves only with the `/` when the form scrolls, so it reads as attached to the trigger. It aims for 296px (header plus about 4.5 rows, the cut row hinting the list scrolls), capped by the room above the keyboard (tracking `visualViewport`); fewer matches shrink it from the bottom. When the `/` sits too close to the keyboard, the form scrolls once on open, only by the missing room and never past the top of the scroll container; it never re-scrolls while filtering. Rows are 44px+ and the 40px header carries a ghost Close button (no Esc on a phone). Flat (Strong Hairline border, Paper, no shadow), a 28px Bench Gray header ("SNIPPETS" Label plus count), rows with the name at 13px, a plain scope chip right and a 1-line mono preview. The active row gets the palette's 7% ink fill and 2px inset ink bar, and the footer shows keycaps (↑↓ move, ↵ insert, Esc close) on laptop. Enter or Tab inserts the snippet in place of the token and keeps native undo; Esc closes it until the token ends.

### Command palette

Ctrl K lists Stage pages, tasks and projects. Enter opens a task in Stage, Ctrl Enter opens T3, Alt C copies the task reference. Retain dense keyboard-first rows and existing focus treatment.

### Copy link

Task headers carry Copy link (self-describing markdown) and URL. Laptop task rows expose Copy link on hover/focus; the palette uses Alt C. Preserve copy feedback and the polite live region. Generated references point to tasks.

### Ref chip

Task references render as compact state/title chips. Historical session references are resolved explicitly: open the owning task when available, otherwise native T3 (remote desktop uses its web URL). Unknown references preserve their original text. Prompts, final replies, journals and handoffs keep this behavior.

### Desktop host
The desktop app renders the same UI; its differences are restrained and live in `components/Host.tsx` (only when the platform has `host`).
- **Connection screen** (before the server first answers, or when it speaks another API): Paper ground, a single 560px column from about 14% of the window height. The static brand glyph and wordmark (top step at 44%, no needs-input state yet), then the PageHeader (H1 names the state, `//` meta line the detail), a flush `STAGE SERVER` Panel (identity as meta; a remote's name with its URL as meta) with three ruled mono step rows (`components/Steps.tsx`, shared with T3 setup), `service`, `server`, `api`: a 6px square marker in its tone (idle gray waiting, ok green, bad red) or the 8px square run-tone spinner on the active step, the step name in Ink Text and a mono note right (`waiting`, `starting`, `checking`, a version, `not running` in red). Below: the fix as a bad Alert, problems as a ruled list (Geist message over a mono Slate fix), a remedy command in a Bench Gray mono box with a small Copy, one primary action (Start Stage server, Restart Stage server or Retry) with a secondary "Switch server" beside it whenever more than one server is saved (it opens the "Switch server" drawer: the server rows of the top-bar menu, picking one switches), and a ghost "Show details" that reveals the `SERVER LOG` Panel (mono pre, scrolls at 288px) and a small "Copy diagnostics". The starting screen waits 400ms before it appears so a fast start shows only the Paper ground. No entrance motion; the spinner stops with reduced motion.
- **Host banner** (after the app first ran on the active server): Alerts stacked at the top of the page column above the PageHeader, 14px above it. Reconnecting is a plain Alert with "Retry now"; stopped, failed or (remote) unreachable is a bad Alert with its primary action and the remedy command as a mono line; a remote's reconnecting and unreachable Alerts add a ghost "Switch server"; a terminal-started server and a server version that differs from the app are plain notes with a ghost Dismiss (skew adds "Restart Stage server"). The app stays mounted underneath so drafts survive.
- **Settings "Desktop app" panel**: the Checkbox row "Open Stage at login" (mono hint "starts hidden, with the server"; this machine's server only), Details rows (App, Server naming the active server, its version and, for this machine, "per-user service" or "started from a terminal", Socket or a remote's URL, Identity) and a "Copy diagnostics" button. The Notifications panel's `THIS DEVICE` controls native notifications (see Settings notifications).
- **OS notifications and badge**: the resident app shows the checked events as system notifications while Stage is not in front (toasts when it is) and keeps the needs-input count on the Dock or launcher icon; the page draws neither. With more than one server saved, a notification's title starts with the server's name and a click switches to that server first; the badge sums every server.
- **Servers** (only when the platform has `host.servers`; `components/Servers.tsx`, logic in `lib/host.ts`): a 6px square marker carries only the active server's state (Settled Green connected, Running Sky connecting or reconnecting, Fault Red unreachable, incompatible or failed); every other server is Idle Gray, since its state is unknown until it is active. A server's needs-input count is the yellow part: a need chip.
  - **Settings `SERVERS` Panel** (left column, under T3 connection; meta `3 saved`, small "Add server" in the header): ruled rows of the marker, the name in Geist 500, a plain `active` chip, a need `dot` chip `3 need input`, and a mono Slate line `this machine · <socket>` (the socket while it is active) or the URL, plus ` · unreachable` (red) or another state note on the active row. Actions right (wrapping under the name on phone): Switch (secondary, not on the active row), Edit (ghost) and Remove (danger), remotes only. Remove confirms inline: a bad Alert "Remove Studio? Stage switches to this machine first. Its notifications stop on this device; the server keeps its data." with Remove and Cancel. A mono Slate footer: "One server at a time. Remotes: trusted network, no sign-in."
  - **Add / Edit drawer** (right drawer, "Add server" or "Edit Studio"): a muted intro naming the trusted network and `stage serve --listen 0.0.0.0:7380` (inline mono), Name, URL (mono input, hint `http(s)://host[:port], no path`), a secondary "Test connection", then a flush `CONNECTION` Panel (meta: the tested URL or `not tested`) with the shared step rows `reachable` (`answers` / `no answer`), `api` (`v2`, or `refused` / `not a Stage server` / another version in red) and `identity` (`stage · 1.4.0`); the reachable step spins while testing. A failed test adds a bad Alert (the hint, the error as a mono line) and, for a refused hostname, the remedy box `STAGE_ALLOWED_ORIGINS=<url>` with Copy. Editing the URL drops the result. Footer: primary "Save server" (Save when editing), Cancel and a mono Slate "saves without switching"; saving never requires a test, and the host's refusals (duplicate name or URL, bad URL) show as a red Alert.
  - **Top-bar server menu** (only with more than one server saved): a secondary mono button (marker, the active server's name truncated at 11rem, chevron) before search on laptop; on phone between the bell and search, at most 7.5rem and without the chevron (on the narrowest phones only the marker and a letter fit). It discloses a flat panel (Strong Hairline, Paper, no shadow), 320px right-aligned 4px under it on laptop and full width under the bar on phone: a Bench Gray `SERVERS` header with the count, the ruled server rows (marker, name at 13px Geist 500, the mono where line and a non-connected state note, the need count chip, a check on the active one) and a "Manage servers" row to Settings. `aria-expanded`; Esc closes and returns focus to the button; a click or focus outside closes it.
  - **A switch** drops the page's data and opens `/` (a notification click then opens its target); the app unmounts until the new server answers, so its connection screen shows after the usual 400ms.
- **Remote server**: the connection screen shows steps `server` and `api`, primary Retry (also for `incompatible`: the app cannot restart a remote), secondary "Switch server"; `unreachable` reads `unreachable` in red on the server step, and a refused hostname adds the `STAGE_ALLOWED_ORIGINS=<url>` remedy box. Local server controls stay hidden for remote servers. Open in T3 uses remote T3 web URLs; this machine and browsers prefer native T3 links.
- Native menus, window chrome, `Cmd/Ctrl` accelerators and window persistence belong to the OS; the page draws none of them.

## Do's and Don'ts

### Do:
- **Do** keep every corner at 0px, including chips, markers, spinners and focus outlines.
- **Do** separate rows and regions with 1px Hairline and outline controls with Strong Hairline.
- **Do** color only what a state earns: chips, square markers, stat counts, alerts, the needs-input edge.
- **Do** draw tones with the two-token pattern: border at 45-55% tone, fill at 8-14%, text in the tone's text color.
- **Do** place the `//` meta line directly below the H1 in 12px mono Slate with a bold Ink Text `//`.
- **Do** head every panel with the 32px Bench Gray bar and an uppercase 11px mono label at 0.12em.
- **Do** set titles, actions and prose in Geist; set metadata, labels, chips, counts and keycaps in JetBrains Mono with tabular numerals.
- **Do** draw icons as authored 16px SVGs: 1.5 stroke, square caps, miter joins, currentColor.
- **Do** open secondary flows in a 580px (or 680px) right drawer that becomes a full-screen sheet below 768px.
- **Do** keep motion to 150ms color, opacity and border transitions on state change.
- **Do** give needs-input rows (and still-waiting needs-input toasts, inbox rows and the Needs input panel) the 2px Waiting Yellow left edge and a primary Open in T3; it is the only colored row edge.

### Don't:
- **Don't** round any corner.
- **Don't** give chrome, navigation, links or primary buttons a hue; the accent is ink and inverts per theme.
- **Don't** set text in the bright need yellow; use `need-fg`.
- **Don't** put an eyebrow or kicker above a heading.
- **Don't** use a text glyph as an icon; draw an SVG. Keycap legends inside a Kbd are key names, not icons.
- **Don't** lay lists out as card grids or shadowed tiles; one ruled panel per state.
- **Don't** cast shadows from surfaces; only the floating command palette has one. Toasts stay flat.
- **Don't** use circles or dots as state markers; markers are squares.
- **Don't** add colored left edges to any row other than needs input.
- **Don't** add entrance animations or choreography.
- **Don't** dress it as a neon terminal (glow, green-on-black, scanlines) or as a rounded-card SaaS dashboard.

The inbox includes a Waiting in T3 group for current external sessions with no recorded events. Rows keep title, project, needs-input badge and Open in T3, without unread markers, event times or history controls. They disappear when resolved or replaced by a recorded notification group.
