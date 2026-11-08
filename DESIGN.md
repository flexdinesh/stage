---
name: t3-stage
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
  muted-dark: "#8b92a3"
  bg-dark: "#0c0e13"
  surface-dark: "#11141a"
  panel-dark: "#161a22"
  line-dark: "rgba(230, 235, 245, 0.1)"
  line-strong-dark: "rgba(230, 235, 245, 0.2)"
  scrim-dark: "rgba(0, 0, 0, 0.55)"
  need-dark: "#e3c341"
  need-fg-dark: "#efd36a"
  run-dark: "#5ec4f0"
  done-dark: "#4fd1c5"
  ok-dark: "#5fd38a"
  bad-dark: "#e5736a"
  idle-dark: "#8b92a3"
typography:
  headline:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "22px"
    fontWeight: 700
    lineHeight: 1.25
    letterSpacing: "-0.025em"
  headline-wide:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "24px"
    fontWeight: 700
    lineHeight: 1.25
    letterSpacing: "-0.025em"
  title:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "15px"
    fontWeight: 600
    lineHeight: 1.45
  row-title:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "13.5px"
    fontWeight: 500
    lineHeight: 1.45
  body:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "13.5px"
    fontWeight: 400
    lineHeight: 1.45
    letterSpacing: "-0.005em"
  body-sm:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "12.5px"
    fontWeight: 400
    lineHeight: 1.45
  button:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "12.5px"
    fontWeight: 500
  button-sm:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "11.5px"
    fontWeight: 500
  stat:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "15px"
    fontWeight: 700
    fontFeature: "tnum"
  label:
    fontFamily: "JetBrains Mono Variable, ui-monospace, monospace"
    fontSize: "10.5px"
    fontWeight: 400
    letterSpacing: "0.12em"
  meta:
    fontFamily: "JetBrains Mono Variable, ui-monospace, monospace"
    fontSize: "11.5px"
    fontWeight: 400
    lineHeight: "18px"
  meta-line:
    fontFamily: "JetBrains Mono Variable, ui-monospace, monospace"
    fontSize: "12px"
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
    height: "56px"
  task-row:
    padding: "8px 12px"
  drawer:
    backgroundColor: "{colors.bg}"
    width: "580px"
  drawer-wide:
    width: "680px"
---

# Design System: t3-stage

## Overview

**Creative North Star: "The Ink Workbench"**

t3-stage is a workbench, not a dashboard of cards. Every view is a stack of ruled, square-cornered panels on a white (light) or near-black (dark) ground, each headed by a tinted bar with an uppercase mono label, each holding hairline-separated rows. The chrome is ink and hairline only: black-on-white primary actions in light mode, near-white-on-near-black in dark. Color is withheld until a task's state earns it, so the one yellow on screen is always the thing waiting on the owner.

Density is high and deliberate: 13.5px body, 32px controls, 8-10px row padding, 14px between panels. Geist carries titles, actions and prose; JetBrains Mono carries everything that is metadata (project names, models, times, counts in rows, labels, chips, keycaps). The direction refuses the rounded-card SaaS dashboard and the neon-terminal costume: it reads like an instrument panel drawn with a ruler, lit only where attention is owed.

Motion is quiet: 150ms color, opacity and border transitions on state change, a pulsing skeleton while loading, a square spinner on busy buttons, and no entrance choreography.

**Key Characteristics:**
- Zero radius on every element, including chips, markers, spinners and focus outlines.
- 1px hairlines (11% ink) separate rows and regions; 20% ink outlines controls.
- Ink accent that inverts per theme; no brand hue in chrome.
- Six state tones (need, run, done, ok, bad, idle) drawn as tinted borders, square markers and colored text.
- Uppercase JetBrains Mono panel headers on a light-gray bar.
- Drawers and full-screen sheets instead of modals.

## Colors

An ink-and-paper neutral system with a strictly semantic state palette; nothing else is colored.

### Primary
- **Ink** (`accent` / `accent-dark`): the primary action color. Black (#0b0d14) on white in light mode, near-white (#eef0f5) on near-black in dark. Fills primary buttons (New task, Start, Open in T3 on a needs-input row), the active nav underline, the selected segmented option, the brand square, text selection, focus outlines and the history bar chart. Paired with `on-accent` for text on it.

### Neutral
- **Paper** (`bg`, `surface`): the ground and panel bodies. Both pure white in light; in dark the ground is #0c0e13 and panel bodies lift one step to #11141a.
- **Bench Gray** (`panel` / `panel-dark`): panel header bars, drawer footers, keycaps, hover fills on secondary and ghost buttons, the palette footer.
- **Ink Text** (`fg` / `fg-dark`): all primary text and row titles.
- **Slate** (`muted` / `muted-dark`): metadata, labels, placeholders, empty-state copy, inactive tabs.
- **Hairline** (`line` / `line-dark`): row separators, panel borders, bar borders.
- **Strong Hairline** (`line-strong` / `line-strong-dark`): control outlines (inputs, secondary buttons, segmented groups, search trigger), drawer edge, run order connectors, dashed backlog steps.
- **Scrim** (`scrim` / `scrim-dark`): behind drawers, sheets and the palette.

### State
- **Waiting Yellow** (`need`, text `need-fg`): needs input, waiting threads. The marker and border are the bright yellow; text uses the darker `need-fg` because the bright yellow fails as text on white.
- **Running Sky** (`run`): launching, running, queued, starting threads.
- **Settle Teal** (`done`): done (run finished) and awaiting settle in T3; T3 threads labelled `completed` read "done".
- **Settled Green** (`ok`): settled tasks and threads (a settled thread shows "settled" whatever its run status), T3 connected.
- **Fault Red** (`bad`): failed, interrupted, T3 offline, errors, danger buttons.
- **Idle Gray** (`idle`): backlog, cancelled, idle, rolled back.

### Named Rules
**The State Earns Color Rule.** Hue appears only on a state: a chip, a square marker, a stat count, an alert, or the needs-input row edge. Chrome, navigation, links and primary buttons stay ink.

**The Two-Token Tone Rule.** Every tone sets a marker/border color and a text color. Borders mix the tone at 45-55% into transparent, fills at 8-14%; the need chip fills at 14% so yellow reads at a glance. Never invent a tone outside the six.

**The One Yellow Edge Rule.** User-approved: a task row in needs input carries a 2px Waiting Yellow left edge and makes "Open in T3" primary. It is the only colored row edge in the system; every other row keeps a transparent 2px edge so titles align.

## Typography

**Display Font:** Geist Variable (with ui-sans-serif, system-ui)
**Body Font:** Geist Variable
**Label/Mono Font:** JetBrains Mono Variable (with ui-monospace)

**Character:** A tight, slightly negative-tracked grotesk for anything a person reads as a sentence or presses, set against a mono that marks everything machine-shaped. The split is functional, not decorative: if it came from T3 or a clock, it is mono.

### Hierarchy
- **Headline** (700, 22px, 24px from 640px up, 1.25, -0.025em): the page H1 only. Balanced wrapping, breaks long task titles.
- **Title** (600, 15px): drawer and sheet titles.
- **Row Title** (500, 13.5px): task and thread titles in rows; underline on hover.
- **Body** (400, 13.5px, 1.45, -0.005em): base text, inputs at 13px.
- **Body Small** (400, 12.5px): alerts, details, empty states, button labels (500).
- **Stat** (700, 15px, tabular): stat strip counts.
- **Label** (JetBrains Mono 400, 10.5px, 0.12em, uppercase): panel headers (in Ink Text), field labels and section labels (in Slate).
- **Meta** (JetBrains Mono 400, 11.5px, 18px line): row metadata, chips, hints, timestamps. Detail values sit at 12px mono, prompts and textareas at 12.5px mono with relaxed leading.

### Named Rules
**The Mono Is Metadata Rule.** JetBrains Mono for project names, models, times, ids, counts, chips, labels, keycaps; Geist for titles, actions and prose. Counts, times and step numbers use tabular numerals.

**The Meta Below Rule.** A page's context line sits directly below the H1 in 12px mono Slate, led by a bold Ink Text `//`. Nothing sits above a heading: no eyebrow, no kicker.

## Layout

A single centered column, max 1240px, with 24px side gutters on laptop and 12px on phone (24px top padding on laptop, 16px on phone). The 48px sticky top bar holds the mark, text tabs (active tab gets a 2px ink underline; Dashboard carries the needs-input count chip), the Ctrl K search trigger, the T3 connection chip, the theme toggle and the primary New task button. Below 768px the top bar compacts and a 56px five-cell bottom tab bar (Stage, Projects, History, Templates, More) takes over navigation, with a square yellow marker on Stage when anything needs input; page content reserves 96px of bottom padding for it.

Pages stack: PageHeader (H1, `//` meta line, actions right-aligned, 16px below), then panels separated by 14px. The Dashboard puts a one-row stat strip (16px below the header) above the state groups in fixed order: Needs input, Running, Completed, Failed, Backlog. Consecutive empty groups collapse into one ruled list with the label left and the empty sentence right. Detail pages split at 1024px into a fluid main column and a 340px side column (300px on History).

Rows are grids: 118px state column, fluid title, optional run order chip ("after #12" or "then #14"), right-aligned actions on laptop; on phone the title leads, the state chip floats right and actions wrap below. Spacing steps are 4, 8, 10, 12, 14, 16, 24px.

### Named Rules
**The Ruled List Rule.** One bordered panel per state or concern, rows separated by hairlines. Never a grid of cards.

**The Drawer Not Modal Rule.** Secondary flows (new task, edit, filters, More) open in a right drawer, 580px or 680px wide, that becomes a full-screen sheet below 768px, with Esc, a focus trap and focus restore.

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

Every corner is square (0px): buttons, inputs, chips, panels, drawers, keycaps, skeleton bars, the busy spinner, and state markers. Borders are always 1px; the only 2px strokes are the active-tab underline, the stat selection bar, the focus outline and the needs-input row edge. State markers are 6px squares (7px in alerts, 8px on the phone tab); the brand mark is a 10px ink square. Run order steps are bordered cells joined by 1px connectors; unstarted steps use a dashed border, the current step adds a 1px Ink Text outline offset 1px.

Icons are authored 16px SVGs on a 16-unit grid: 1.5 stroke, square caps, miter joins, currentColor, so they share the sharp geometry of the grid.

### Named Rules
**The Zero Radius Rule.** No element rounds a corner. A circle is never a state marker; the marker is a square.

## Components

### Buttons
Compact, flat, and ink-led.
- **Shape:** square (0px), 1px border, 32px tall with 12px side padding; small is 24px with 8px padding and 11.5px labels.
- **Primary:** Ink fill and border, `on-accent` text, 500 weight. One per region: New task, Start/Restart, Open in T3 when the task needs input, drawer submit.
- **Hover / Focus:** primary drops to 85% opacity; secondary and ghost fill Bench Gray; 150ms transition; 2px ink focus outline offset 2px. Disabled at 45% opacity. Busy shows a square spinning border.
- **Secondary:** Paper fill, Strong Hairline border, Ink Text. The default.
- **Ghost:** transparent border, Slate text; for close buttons and panel-header actions.
- **Danger:** Paper fill, Fault Red text and 45% red border, 8% red hover fill; Delete only, pushed to the far right.

### Chips
- **Style:** inline mono label (11.5px, 18px line, 1px 7px padding) with a tone-tinted 1px border, a faint tone fill and tone text. The `dot` variant prefixes a 6px square marker.
- **State:** task and thread states map onto the six tones; plain chips (Slate border, Ink Text) carry model, runtime, workspace, tags and the run order link.

### Cards / Containers (Panel)
- **Corner Style:** square.
- **Background:** Paper body, Bench Gray header bar (min 32px, 4px 12px padding) with an uppercase mono label in Ink Text, mono meta count beside it, actions right.
- **Shadow Strategy:** none (Flat Bench Rule).
- **Border:** 1px Hairline outside, Hairline under the header.
- **Internal Padding:** 12px; flush for row lists.

### Inputs / Fields
- **Style:** 32px, Paper fill, Strong Hairline border, square, 10px padding, 13px text; textareas switch to 12.5px mono. Selects carry an authored chevron.
- **Focus:** border and a 1px ring both go Ink.
- **Error / Disabled:** errors render as a red Alert below; disabled at 55% opacity. Labels sit above in the Label style.

### Segmented control
Joined mono buttons inside one Strong Hairline border, hairline dividers between options; the pressed option fills Ink. Used for runtime mode, time window, follow-up mode and theme.

### Navigation
- **Laptop:** text tabs at 13px, Slate at rest, Ink Text with a 2px Ink underline when active, full bar height.
- **Phone:** 56px bottom bar, five equal cells with a 16px icon over a 10.5px label; active cell gets a 2px Ink top border. Templates and Settings live in the More drawer.

### Stat strip (signature)
One bordered row of state counts that doubles as the Dashboard filter (`?state=`). Each cell shows a square tone marker (only when the count is non-zero), a bold tabular count and a mono label; zero counts go Slate. The selected cell fills its tone at 10% with a 2px inset bottom bar and colors the count. Laptop cells sit left with a "click a count to filter" hint right; phone cells split the width evenly with short labels. The last group, Settled (last 7 days), renders collapsed behind a chevron header (`aria-expanded`); filtering to it expands it.

### Task row (signature)
State chip, row title with a mono meta line (project, model, relative time, tags), optional run order chip, and right-aligned small actions. Needs-input rows take the One Yellow Edge and a primary Open in T3. Last errors print as a truncated red mono line under the title.

### Alert
Tinted callout: tone border at 50%, tone fill at 9% over surface, a 7px square marker, 12.5px text, actions right. `need` for waiting-on-you, `bad` for errors.

### Run order pipeline
Step cells joined by 1px Strong Hairline connectors, in the task detail Run order panel (top of the side column). Numbered, titled cells on laptop, numbers only on phone; the viewed task gets the Ink outline. Below it, "Runs after" and "Then runs" lines carry Unlink, and pickers link new steps. A waiting task's Start now arms a confirm ("unlink from #12 and start") before acting.

### Image dropzone
The prompt (new/backlog task) and the follow-up box are drop targets: dragging files over them lays a dashed Ink-accent border and a 90% surface scrim with "Drop images to attach" in mono. Below, 56px square thumbnails (1px Strong Hairline, `object-cover`) wrap in a row; each has an 18px square remove button on its top-right corner, uploading thumbs dim to 60% under the busy spinner, failed ones take a Red border and a red line naming the file. A ghost "Add image" button (file picker) and a mono `n/8` count (or the accepted types when empty) close the block. Read-only strips (task detail Prompt panel, non-backlog edit) are the same thumbnails linking to the full image. With images in a follow-up the send-mode group dims (images go as one new message).

### Command palette
Ctrl K. Centered 660px panel 72px from the top on laptop, full screen on phone; grouped results under Label headers, matched text bolded, active result gets a 7% ink fill and a 2px inset ink bar; a Bench Gray footer lists keycaps.

## Do's and Don'ts

### Do:
- **Do** keep every corner at 0px, including chips, markers, spinners and focus outlines.
- **Do** separate rows and regions with 1px Hairline and outline controls with Strong Hairline.
- **Do** color only what a state earns: chips, square markers, stat counts, alerts, the needs-input edge.
- **Do** draw tones with the two-token pattern: border at 45-55% tone, fill at 8-14%, text in the tone's text color.
- **Do** place the `//` meta line directly below the H1 in 12px mono Slate with a bold Ink Text `//`.
- **Do** head every panel with the 32px Bench Gray bar and an uppercase 10.5px mono label at 0.12em.
- **Do** set titles, actions and prose in Geist; set metadata, labels, chips, counts and keycaps in JetBrains Mono with tabular numerals.
- **Do** draw icons as authored 16px SVGs: 1.5 stroke, square caps, miter joins, currentColor.
- **Do** open secondary flows in a 580px (or 680px) right drawer that becomes a full-screen sheet below 768px.
- **Do** keep motion to 150ms color, opacity and border transitions on state change.
- **Do** give needs-input rows the 2px Waiting Yellow left edge and a primary Open in T3; it is the only colored row edge.

### Don't:
- **Don't** round any corner.
- **Don't** give chrome, navigation, links or primary buttons a hue; the accent is ink and inverts per theme.
- **Don't** set text in the bright need yellow; use `need-fg`.
- **Don't** put an eyebrow or kicker above a heading.
- **Don't** use a text glyph as an icon; draw an SVG. Keycap legends inside a Kbd are key names, not icons.
- **Don't** lay lists out as card grids or shadowed tiles; one ruled panel per state.
- **Don't** cast shadows from surfaces; only the floating command palette has one.
- **Don't** use circles or dots as state markers; markers are squares.
- **Don't** add colored left edges to any row other than needs input.
- **Don't** add entrance animations or choreography.
- **Don't** dress it as a neon terminal (glow, green-on-black, scanlines) or as a rounded-card SaaS dashboard.
