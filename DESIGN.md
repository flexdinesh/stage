---
name: stage
description: Ink workbench for queueing and watching harness sessions; ruled lists where only state carries color.
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
  on-need: "#0b0d14"
  on-bad: "#ffffff"
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
  on-need-dark: "#0b0d14"
  on-bad-dark: "#0b0d14"
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
  badge:
    fontFamily: "JetBrains Mono Variable, ui-monospace, monospace"
    fontSize: "10px"
    fontWeight: 700
    lineHeight: "16px"
rounded:
  none: "0px"
spacing:
  xs: "4px"
  sm: "8px"
  row: "10px"
  control: "32px"
  control-sm: "24px"
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
  corner-badge:
    typography: "{typography.badge}"
    backgroundColor: "{colors.accent}"
    textColor: "{colors.on-accent}"
    rounded: "{rounded.none}"
    padding: "0 3px"
    height: "16px"
  corner-badge-need:
    backgroundColor: "{colors.need}"
    textColor: "{colors.on-need}"
  corner-badge-bad:
    backgroundColor: "{colors.bad}"
    textColor: "{colors.on-bad}"
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

Motion is quiet: 150ms color, opacity and border transitions on state change, a pulsing skeleton while loading (static with reduced motion), a square spinner on busy buttons, and no entrance choreography.

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
- **Ink** (`accent` / `accent-dark`): the primary action color. Black (#0b0d14) on white in light mode, near-white (#eef0f5) on deep slate in dark. Fills primary buttons (New task, Start, Open in {harness} on a needs-input row), the active nav underline, the selected segmented option, the brand glyph, text selection, focus outlines and the history bar chart. Paired with `on-accent` for text on it.

### Neutral
- **Paper** (`bg`, `surface`): the ground and panel bodies. Both pure white in light; in dark the ground is #1a1d23 and panel bodies lift one step to #1f2229.
- **Bench Gray** (`panel` / `panel-dark`): panel header bars, drawer footers, keycaps, hover fills on secondary and ghost buttons, the palette footer.
- **Ink Text** (`fg` / `fg-dark`): all primary text and row titles.
- **Slate** (`muted` / `muted-dark`): metadata, labels, placeholders, empty-state copy, inactive tabs.
- **Hairline** (`line` / `line-dark`): row separators, panel borders, bar borders.
- **Strong Hairline** (`line-strong` / `line-strong-dark`): control outlines (inputs, secondary buttons, segmented groups, search trigger), drawer edge, the rule between tree levels, dashed not-started tree chips and cells.
- **Scrim** (`scrim` / `scrim-dark`): behind drawers, sheets and the palette.

### State
- **Waiting Yellow** (`need`, text `need-fg`): needs input, waiting sessions, T3 pairing that waits on the user (unpaired, expired). The marker and border are the bright yellow; text uses the darker `need-fg` because the bright yellow fails as text on white.
- **Running Sky** (`run`): launching, running, queued and starting sessions.
- **Settle Teal** (`done`): a finished run not completed yet, whose task chip reads "to review" (history and notifications, which know only the run, read "run finished"); T3 threads labelled `completed` read "done".
- **Settled Green** (`ok`): completed tasks and trees, T3 connected. A completed task shows one "completed" chip; its execution chip joins only once execution moves on (running again, needs input, failed, interrupted, cancelled).
- **Fault Red** (`bad`): failed, interrupted, T3 unreachable or rejected, errors, danger buttons.
- **Idle Gray** (`idle`): backlog, cancelled, idle, rolled back.
- **On-state text** (`on-need`, `on-bad`): text on a solid need or bad fill (the corner badge). Ink on yellow in both themes; white on red in light, ink in dark.

### Named Rules
**The State Earns Color Rule.** Hue appears only on a state: a chip, a square marker, a stat count, an alert, the needs-input row edge, or the mark's top step while anything needs input. Chrome, navigation, links and primary buttons stay ink.

**The Two-Token Tone Rule.** Every tone sets a marker/border color and a text color. Borders mix the tone at 45-55% into transparent, fills at 8-14%; the need chip fills at 14% so yellow reads at a glance. Never invent a tone outside the six.

**The One Yellow Edge Rule.** User-approved: a task row in needs input carries a 2px Waiting Yellow left edge and makes its primary action "Open in {harness}". It is the only colored row edge in the system; every other row keeps a transparent 2px edge so titles align. The same edge marks a needs-input toast, an inbox row whose task or session is still waiting, and the Needs input panel while something still waits on the user.

## Typography

**Display Font:** Geist Variable (with ui-sans-serif, system-ui)
**Body Font:** Geist Variable
**Label/Mono Font:** JetBrains Mono Variable (with ui-monospace)

**Character:** A tight, slightly negative-tracked grotesk for anything a person reads as a sentence or presses, set against a mono that marks everything machine-shaped. The split is functional, not decorative: if it came from T3 or a clock, it is mono.

### Hierarchy
Sizes are role tokens (`--fs-*` on `:root`, exposed as `text-label`, `text-meta-sm`, `text-meta`, `text-detail`, `text-small`, `text-ui`, `text-body`, `text-title`, `text-headline`, `text-tab`, `text-input*` utilities). Values below are laptop; below 768px every role steps up about 1px (body 15.5px, meta 13px, label 11.5px, title 17px, headline 24px). Never write a raw `text-[Npx]`.

- **Headline** (700, 26px on laptop, 24px on phone, 1.25, -0.025em): the page H1 only. Balanced wrapping, breaks long task titles.
- **Title** (600, 16px): drawer and sheet titles.
- **Row Title** (500, 14.5px): task and session titles in rows; underline on hover.
- **Body** (400, 14.5px, 1.45, -0.005em): base text. Inputs, selects and textareas use `text-input*` (14px, mono 13px on laptop) and are 16px on phone so iOS Safari never zooms on focus.
- **Body Small** (400, 13px): alerts, details, empty states, button labels (500).
- **Stat** (700, 16px, tabular): stat strip counts.
- **Label** (JetBrains Mono 400, 11px, 0.12em, uppercase): panel headers (in Ink Text), field labels and section labels (in Slate).
- **Meta** (JetBrains Mono 400, 12px, 1.5 line): row metadata, chips, hints, timestamps. Small counts and panel meta sit at 11.5px (`text-meta-sm`), detail values at 12.5px mono (`text-detail`), read-only prompts at 13px mono with relaxed leading. **Badge** (JetBrains Mono 700, 10px, 16px line, tabular) is only the corner badge's count.

### Named Rules
**The Mono Is Metadata Rule.** JetBrains Mono for project names, models, times, ids, counts, chips, labels, keycaps; Geist for titles, actions and prose. Counts, times and step numbers use tabular numerals.

**The Meta Below Rule.** A page's context line sits directly below the H1 in 12px mono Slate, led by a bold Ink Text `//`. Nothing sits above a heading: no eyebrow, no kicker.

## Layout

From 1024px a 248px sticky full-height sidebar (Hairline right rule) leads a fluid content column (centered, max 1240px, 24px gutters and top padding). Sidebar, top to bottom: a 48px header row with the mark and wordmark, then the Back and Forward arrows where the host has none (see Back and history list); the Ctrl K search trigger beside a square primary New task button; the views (Dashboard, Tasks, Trees, Schedules: 16px icon (inbox, list-checks, network, calendar-clock) and Geist label, 32px rows); the project scope list (see Project scope); a footer with Snippets and Settings; then a 48px bottom row under a hairline: the status button (see Status button), the theme toggle (system shows the monitor icon) and the notifications bell. The content column has no bar of its own: the page starts at the top with its title, and its location rides in the meta line (see Page location). The current row (views and the selected project) takes the active row box: Bench Gray fill boxed by a 1px inset Strong Hairline, no side bar. Dashboard carries the count of the scope's Dashboard rows (a tree counts once, no backlog), in the same neutral style as the project counts, inset by the 28px pin slot so it shares the project counts' column. Below 1024px a fixed 52px top bar (a fixed 40px slot holding the glyph or Back, the scope chip, then the bell, search and a square New task, 40px controls) and a 56px five-cell bottom tab bar (Dashboard, Tasks, Trees, Schedules, More; More holds Projects, Snippets, Settings and the theme) take over navigation, with a square yellow marker on Dashboard when anything needs input and a square in the status button's tone on More while a connection needs attention; page content reserves 52px of top padding plus the top safe-area inset for the top bar, and 96px of bottom padding plus the bottom safe-area inset for the tabs.

### Project scope
One per-device scope (preference storage) filters Dashboard, Tasks, Trees and Schedules; picking a project anywhere else opens the Dashboard. Old `?project=` links set it once. A scope whose project is gone reads as All projects. The list: All projects (Geist), Pinned (when any), Projects (a `…` link to the Projects page) by most recent activity, then the projects with no open work folded under a mono "n quiet projects" disclosure (the selected scope stays listed). Project rows are mono 12.5px titles with their open work as one right-aligned neutral count: a 2ch right-aligned mono Slate number of every open task (needs input, review, ready, failed, running and backlog), so counts line up down the list; above 99 it reads `99+` with the `+` hanging past the slot. Sidebar counts carry no state color or square: needs input surfaces through the brand mark, the bell, the phone Dashboard tab and the Dashboard itself. Rows with no open work (the quiet projects) draw no count. The breakdown (need input, to review, start or retry, running, in backlog) is the count's tooltip and screen-reader text. A pin slot follows every row (12px pin icon, always filled, Slate in both states and ink on hover; revealed on hover or focus, always shown when pinned and on touch). Scoped pages lead their H1 with the project title in Slate (`zero / Tasks`), print the workspace path as the meta line, add a secondary Project settings (project detail) to the header actions, and drop the project column from rows. Phone opens the same list in a Projects sheet from the top-bar scope chip (44px rows).

Pages stack: PageHeader (H1, `//` meta line, actions right-aligned, 16px below), then panels separated by 14px. The Dashboard lists, in fixed order: Needs input, Awaiting review, Failed / interrupted, Running, Ready (empty groups hidden; with none but Running, one bordered line above: an ok check, "Nothing needs you." with the sentence "Questions, finished runs to review, failures and ready starts land here." and a secondary Browse tasks). A group lists its trees first (the Trees view row: state bar, next step, one action, task count expanding the members), then its tasks outside trees; a tree member never has its own row, and a tree lists once, under its most pressing step. The group header count is its rows, with "N trees · M tasks" when it has trees. Below them an "In flight" title (Geist 16px semibold, mono Slate "no action needed") heads a two-cell ruled strip (Scheduled · 24h, Backlog, counting tasks outside trees: tone square, bold count, caps label) whose cells open Tasks filtered to that group and to tasks not in a tree. Tasks (Open view) puts a one-row stat strip above its groups in fixed order: Needs input, Awaiting review, Ready, Failed / interrupted, Running, Backlog, Scheduled, Completed (Scheduled has no stat cell); empty groups are hidden. Every group that lists rows folds from its header: the whole Bench Gray bar toggles (a 12px Slate chevron before the tone marker, Ink on hover, rotated -90° when collapsed, `aria-expanded`); collapsed, the header keeps its title and count and drops its actions. Choices persist per device (preference storage); only Completed starts collapsed. Filtering to a group (`?state=`) always shows its rows. A collapsed Needs input reopens on its own when a task starts needing input (a new task, or a task asking again). Detail pages split at 1024px into a fluid main column and a 340px side column (300px on History).

Rows are grids: 118px state column, fluid title (task rows lead it with the task number), optional row chip ("needs #3 #5", a tree, a schedule, "by agent"), right-aligned actions on laptop; on phone the title leads, the state chip floats right and actions wrap below. Spacing steps are 4, 8, 10, 12, 14, 16, 24px.

### Named Rules
**The Ruled List Rule.** One bordered panel per state or concern, rows separated by hairlines. Never a grid of cards.

**The Drawer Not Modal Rule.** Secondary flows (new task, edit, filters) open in a right drawer, 580px or 680px wide, that becomes a full-screen sheet below 768px, with Esc, a focus trap and focus restore.

**The Go Or Change Rule.** Navigation, a manual state change and an action never look alike. Going somewhere is a link with a trailing arrow (a panel header's mono `Open →`), never a button. A manual state change goes through the State picker: a select-only Combobox showing the current status (tone square, mono label) whose other option is the one allowed move with its consequence right-aligned on the same line ("accept without a run", "clears acceptance"; disabled with "stop execution first" while it runs), confirmed in a drawer. Buttons act: Start, Retry, Open in T3, Edit.

**The One Door Rule.** Show one Open in {harness} or Copy resume command per task and tab. On task detail the Session header owns the destination, in the phone action bar below 768px. Transcript and reply links reuse that destination. Task’s Answer link goes to Session.

**The Safe Area Rule.** The page sets `viewport-fit=cover`, so anything pinned to a screen edge on phone pads itself with `env(safe-area-inset-*)`: the phone top bar (top), bottom tabs, task detail action bar and toasts (bottom), and every drawer sheet and the command palette (header top, footer or body bottom, sides in landscape). Primary actions never sit on the home indicator.

## Elevation & Depth

Flat. Depth comes from tone and line, not shadow: a Bench Gray header bar over a Paper body, hairline borders, a one-step-lighter surface in dark mode, and a scrim behind overlays. The single cast shadow belongs to the command palette, the one centered floating overlay on laptop. Selection and focus use inset bars and rings, never lift.

### Shadow Vocabulary
- **Palette overlay** (`box-shadow: 0 16px 48px rgba(0,0,0,0.28)`): command palette on laptop only.
- **Selected stat bar** (`box-shadow: inset 0 -2px var(--c)`): the active stat strip cell, in its tone.
- **Active row box** (`box-shadow: inset 0 0 0 1px var(--line-strong)`): over a Bench Gray fill, the current sidebar row and the highlighted palette, combobox and snippet picker result. Inset, so rows never shift.
- **Input focus ring** (`box-shadow: 0 0 0 1px var(--accent)`): with an ink border on focused inputs and selects.

### Named Rules
**The Flat Bench Rule.** Surfaces never cast shadows. Only the floating palette may; everything else separates by hairline and bar tint.

## Shapes

Every corner is square (0px): buttons, inputs, chips, panels, drawers, keycaps, skeleton bars, the busy spinner, and state markers. Borders are always 1px; the only 2px strokes are the active-tab underline, the stat selection bar, the focus outline and the needs-input row edge. State markers are 6px squares (7px in alerts, 8px on the phone tab); the brand mark is a 16px stepped-bars glyph (see Brand mark). Tree panel cells are bordered boxes; not-started (backlog) members use a dashed border (cells and tree view state chips), the viewed member adds a 1px Ink Text outline offset 1px, the selected tree view row a 1px Ink Text outline inset 2px.

Icons are Lucide (`lucide-react`), sharpened: square caps, miter joins, a constant 1.33px stroke at every size (`absoluteStrokeWidth`), currentColor, 16px by default (12px in chips), so they share the sharp geometry of the grid. All of them come through `packages/app/src/components/icons.tsx`; never import `lucide-react` elsewhere. Dashboard is the inbox, Schedules the calendar-clock; the timed-task chip keeps the plain clock.

### Named Rules
**The Zero Radius Rule.** No element rounds a corner. A circle is never a state marker; the marker is a square.

## Components

### Primitives
Feature code composes the primitives in `packages/app/src/components/ui/` (re-exported from `packages/app/src/components/ui.tsx`); it never hand-rolls a raw control or a visual recipe. `packages/app/tests/primitives.test.mjs` fails on a raw `<button>`, native input, checkbox or radio, popover attribute, `confirm()`, portal, hand-rolled panel header, row, needs-input edge, disclosure chevron, inline error, muted paragraph, elevation shadow or rounded corner outside `packages/app/src/components/ui/`, naming the primitive to use; its short allowlist names each exception and why. Every primitive renders on `/dev/ui` (a hidden, lazy route; not linked).

**Size tokens.** Control and row sizes are CSS variables in `index.css`, swapped at the 768px phone breakpoint, so no feature writes a `h-10 md:h-8` pair:
- `--control-h` 32px (phone 40px), Tailwind `h-control`/`size-control`: buttons, inputs, pickers, segmented, icon buttons.
- `--control-h-sm` 24px (phone 32px), `h-control-sm`: small buttons and selects.
- `--row-py` 8px (phone 10px), `py-row`: ruled row padding.

**By area** (one file each under `packages/app/src/components/ui/`):
- **button:** `Button` (primary, secondary, ghost, danger; `small`, `busy`), `IconButton` (square, `label` is its accessible name; ghost, secondary, primary), `TextButton` (a title that acts), `PickerButton` (secondary control with a trailing chevron: server and scope pickers, More menus).
- **form:** `Input`, `Textarea`, `Checkbox`, `Radio`, `Field` (label above a control), `FieldGroup` (a fieldset with a Label legend, optional mono meta and hint), `ChoiceRow` (checkbox or radio row with a detail line), `Segmented`.
- **combobox:** `Combobox` (see Combobox).
- **layout:** `Panel` and `PanelHeader` (the Bench Gray bar), `PageHeader`, `SectionHeading` (an H2 outside panels with mono meta), `SubHeading` (a Label group heading inside panels and drawers).
- **list:** `RowList` + `Row` (ruled `px-3 py-row` rows; `need` draws the yellow edge, `bare` for rows with their own grid), `rowClass` where the element can't be an `li`, `RowButton`/`rowButtonClass` (whole-row selectable buttons; selected takes the active row box), `needEdgeClass`.
- **text:** `Marker` (6px square, 7px `md`), `Meta`, `MetaLine` (present items joined with ` · `), `IdTag` (`#51`), `Hint` (Geist help text; prose is never mono), `RelTime` (relative, exact on hover), `TextLink` (hover, rest, muted), `ExternalLink`.
- **feedback:** `Alert`, `ErrorText`, `InlineError` (one red mono line), `Muted`, `EmptyState` (muted line with an optional next-step action; `inset` inside flush panels), `ConfirmInline`.
- **overlay:** `Drawer` and `ModalLayer` (focus trap, scroll lock, Esc, focus returns), `useMenu` + `Menu` + `MenuItem` (native top-layer popover anchored to its trigger: light dismiss, Esc, wrapping arrows, Home and End, first item focused on open, focus back to the trigger; items carry an optional mono `reason` under a disabled label and a red `danger`; `MenuSeparator`; `MenuGroup` heads a setting's choices with its Label, each a `MenuItem checked` (`menuitemradio`, a leading 14px Ink check on the current one, the others keep its space); flips inline near the viewport edge), `usePopover` + `PopoverPanel` (a non-menu panel: status details, server picker), `FloatingPortal`, `KeyHints` (keycap footer), `Highlight` (bold matched text), `rovingIndex`.
- **disclosure:** `DisclosureChevron`, `DisclosureButton`.
- **copy:** `CopyButton` (label reads "Copied" for 1.5s; polite announcement), `CommandBox`, `useCopy`.
- **loading:** `Skeleton`, `SkeletonBar`, `SkeletonRows`, `QueryState`.
- **tone:** `Chip`, `Kbd`, `toneClass`.

**Confirms.** An irreversible or wide action (delete, disconnect, forget, remove, unbind, dissolve, leave, connect as a new service) confirms in place with `ConfirmInline`: a Waiting Yellow Alert that names what happens, then the confirm button (danger for destructive, primary otherwise) and a ghost Cancel that takes focus so a stray Enter never confirms; its error prints below. Never `window.confirm`, never a modal. The Awaiting review "Complete all" header confirm and a review row's "Starts #12" confirm stay compact in-row variants: confirm, then ghost Cancel.

### Buttons
Compact, flat, and ink-led.
- **Shape:** square (0px), 1px border, 32px tall with 12px side padding; small is 24px with 8px padding and 12px labels. On phone (below 768px) default is 40px with 14px padding and small is 32px, for thumb-sized targets.
- **Primary:** Ink fill and border, `on-accent` text, 500 weight. One per region: New task, Start/Restart, Complete on a review row, Open in {harness} when the task needs input, drawer submit.
- **Hover / Focus:** primary drops to 85% opacity; secondary and ghost fill Bench Gray; 150ms transition; 2px ink focus outline offset 2px. Disabled at 45% opacity. Busy shows a square spinning border.
- **Secondary:** Paper fill, Strong Hairline border, Ink Text. The default.
- **Ghost:** transparent border, Slate text; for close buttons and panel-header actions.
- **Danger:** Paper fill, Fault Red text and 45% red border, 8% red hover fill; Delete only, pushed to the far right (an Archive or Unarchive secondary sits just before it).

### Chips
- **Style:** inline mono label (`text-meta`, 1.5 line, 1px 7px padding) with a tone-tinted 1px border, a faint tone fill and tone text. The `dot` variant prefixes a 6px square marker.
- **State:** task and session states map onto the six tones; plain chips (Slate border, Ink Text) carry `tracked session`, `manual`, needs and the tree link. Detail page headers never repeat a panel: task detail's header chips are flags only (`archived`, `tracked session`, `replaced`), drawn only when one applies; its state lives in the State panel and its model, permission, mode and workspace in Config. Schedule detail's header keeps only its active/paused chip; its config lives in its Config panel. Both Config panels' Harness row follows the name with its connection's live state chip, in the Settings Connections row's tones and read on its machine (`connected`, `expired`, `unreachable`, `checking`; an offline or outdated worker an idle `offline` / `needs update`; an unset connection an idle `disconnected`), its title the Settings detail line and error; a replaced connection keeps its muted "(replaced)" and no chip. A yellow or red state, or `disconnected`, adds a Geist "Fix in Settings" link under it to Settings Connections.
- **Corner badge** (`CornerBadge`, `.corner-badge`): a count on a square icon button's top-right corner, so the button never widens. 16px tall (min 16px wide, 0 3px padding), straddling the corner by 4px, mono 10px bold tabular, capped at `99+`, a solid fill knocked out from the ground by a 2px `bg` ring (a separation, not a cast shadow). Plain fills Ink (`accent` / `on-accent`); need fills Waiting Yellow with `on-need` text; bad fills Fault Red with `on-bad` text. Decorative: the button's accessible name carries the count. Used by the notifications bell and the status button.
- **Not a chip:** a role or identity tag beside a row title (`main` or `worker` on Settings Connections machines) is Slate mono `Meta` with no box; boxed beside row buttons it reads as a control.

### Cards / Containers (Panel)
- **Corner Style:** square.
- **Background:** Paper body, Bench Gray header bar (min 32px, 4px 12px padding) with an uppercase mono label in Ink Text, mono meta count beside it, actions right.
- **Shadow Strategy:** none (Flat Bench Rule).
- **Border:** 1px Hairline outside, Hairline under the header.
- **Internal Padding:** 12px; flush for row lists.

### Loading (skeleton)
A loading view mirrors the layout it stands in for, so nothing shifts when data lands: rows reuse their real grid (task, schedule, project and tree rows; the task detail header, action row and two columns, State first on phone), and text known before the data (panel titles such as Prompt, State, Tree, Tasks; stat strip and In flight labels; section headings) renders as itself. Only data becomes bars: square Bench Gray blocks (Hairline-dark on a Bench Gray header bar) sized to the text, chip, button or count they replace. Bars pulse; with reduced motion they hold still. The placeholder keeps its space but stays hidden for its first 200ms, an instant step and not a fade, so a fast load shows only the finished view. Screen readers hear one "Loading …" status. Stat strip and In flight cells keep their labels and swap the count for a bar (stat cells disabled until counts land); the Dashboard `//` meta line holds its height with a bar. Shared pieces: `SkeletonBar`, `Skeleton`, `SkeletonRows` (the ruled-rows default for `QueryState`) in `packages/app/src/components/ui/loading.tsx`; no raw pulse elsewhere (a test enforces it). Switching a list's filter or project scope keeps the previous rows on screen at 60% opacity (150ms) and busy until the new rows land; only a list's first load shows a skeleton.

### Inputs / Fields
- **Style:** 32px (40px on phone; small selects 24px, 32px on phone), Paper fill, Strong Hairline border, square, 10px padding, 14px text (16px on phone); textareas switch to 13px mono (16px on phone). Selects and comboboxes carry an authored Slate chevron (`.select-chevron`, per-theme `--chevron`). Enabled buttons, selects and labels show a pointer cursor; disabled ones `not-allowed`.
- **Focus:** border and a 1px ring both go Ink.
- **Error / Disabled:** errors render as a red Alert below; disabled at 55% opacity. Labels sit above in the Label style.

### Combobox
Every dropdown is the one `Combobox`; there is no native select (a boundaries test bans `<select>`). At rest it looks like an input: same height (small: 24px, 32px on phone), border, chevron and focus ring. Two variants share the list, rows and keys:
- **Searchable** (lists over 8 options, and every project picker): an input. Focus or click opens the list with the current label selected, so typing replaces it and filters. Matches are case-insensitive substrings, ranked label prefix, label substring, tag, detail; ties keep the list's order. Projects rank title prefix, title substring, path only, with ties and the empty query by recent activity. A pinned first option ("All projects", "Global", "Create new project") leads while the query is empty.
- **Select-only** (8 or fewer, so a phone keyboard never opens for a short list): a button. Click, Enter, Space or ↓ opens; typing jumps to the first label starting with the typed prefix (repeating a letter cycles); Enter or Space selects.

The list drops under the control (left-aligned, at least 280px, right-aligned when it would overflow), at most 344px tall, and flips above near the bottom of the visible area, including over a phone keyboard. Flat (Strong Hairline, Paper, no shadow); rows show a leading Ink check on the current value, the label at 13px, an optional 1-line mono Slate detail (a project's path), an optional plain chip (archived) and an optional mono Slate tag at the end (a model's provider); matched text bold; 44px+ rows on phone. A saved value the harness no longer offers lists as a muted row that arrows skip and clicks ignore. The active row gets the active row box; the laptop footer shows keycaps (↑↓ move, ↵ select, Esc close). Enter selects and never submits the form; Esc closes the list (not the drawer) and restores the label, as do Tab and blur. No match shows a muted line ("No matches", "No matching projects").

### Segmented control
Joined mono buttons inside one Strong Hairline border, hairline dividers between options; the pressed option fills Ink. Default is 32px tall overall (40px on phone), matching buttons and inputs it sits beside; small (compact controls) is 26px (34px on phone). Used for runtime mode, time window, theme, project sort/archive filter.

### Brand mark
Three stepped square bars, widest at the bottom, on the 16-unit grid; no rounded corners, no circle.
- **Top bar glyph** (both bars): 16px, bars 3 units tall with 1-unit gaps (x 1/3/5, widths 14/10/6) in `currentColor`: bottom solid, middle at 72%, top step at 44%. While anything needs input the top step fills Waiting Yellow (`need`), 150ms fill transition, none with reduced motion. Followed by the bold `stage` wordmark, 8px gap. Decorative (`aria-hidden`): the sidebar Dashboard count and the phone Dashboard tab marker announce the state. The phone top bar keeps the wordmark for screen readers only.
- **Tab favicon:** `favicon.svg` (idle: full-bleed #0b0d14 tile, white bars, top step at 44%) swaps to `favicon-need.svg` (top step #e3c341) while anything needs input. Bars are 2 units tall with 2-unit gaps so they hold at 16px. Fixed ink tile in both themes.
- **App icons:** `logo.svg` is the brand source: square #0b0d14 tile, white bottom bar, middle at 72%, static #e3c341 top step (outside app chrome, so the yellow is brand, not state). Bars sit inside the maskable 80% safe zone; the OS applies its own mask.
- **Raster provenance:** every PNG in `apps/web/public/icons` is generated from `logo.svg` (run in `apps/web/public`): `magick -background none -density 96 logo.svg -resize 192x192 PNG32:icons/icon-192.png` (same at 512 for `icon-512.png`); `magick -background '#0b0d14' -density 96 logo.svg -resize 512x512 -flatten -alpha off PNG24:icons/icon-maskable-512.png` (same at 180 for `apple-touch-icon.png`). The desktop app icon (`desktop/app/icon/icon.png`, 1024px, and `icon.icns`) is rendered from the same `logo.svg`; the commands are in [docs/desktop.md](docs/desktop.md#app-icon).

### Navigation

- Laptop (from 1024px): the sidebar's views, Dashboard, Tasks, Trees, Schedules, then the project scope list, with Snippets and Settings in its footer (see Layout).
- Phone: five equal tabs, Dashboard, Tasks, Trees, Schedules, More. More is a page in the standard shell, with a ruled list of Projects, Snippets and Settings, the status sections (see Status button, 44px rows) and the Theme control. The bottom tabs stay visible, and More carries the active indicator on its page and those destinations.

### Pull to reload
On phones and tablets, in browser and installed PWA, a single-finger downward pull from the document top reloads the current URL when released at 80 CSS pixels. Below that distance it cancels. Controls, focused editors, nested scrollers, drawers and the palette keep their gestures; horizontal intent, multitouch and navigation cancel the pull.

A downward pull moves page content with increasing resistance, bounded below 112px; the 80px trigger reveals 56px of space beneath the header. Header, bottom tabs and fixed task actions stay anchored. A single pointer-transparent, square-cornered Paper/Strong Hairline indicator sits below the active header. Geist copy changes from “Pull to reload” to “Release to reload”, then “Reloading…” with the square busy spinner. On release or cancellation, content settles back over 180ms; the reloading indicator remains visible until navigation completes. State changes announce politely; reduced motion skips the settling animation and keeps the spinner still. Full reload clears unsaved drafts and requires connectivity.

### Stat strip (signature)
One bordered row of state counts that doubles as the Tasks and Trees filter (`?state=`). Each cell shows a square tone marker (only when the count is non-zero), a bold tabular count and a mono label; zero counts go Slate. The selected cell fills its tone at 10% with a 2px inset bottom bar and colors the count. Cells: Needs input, Running, Review, Failed, Ready (idle tone: backlog tree members whose needs are completed, waiting for Start), Backlog (untimed backlog not ready), Completed. Counts follow the Tasks tree filter. Laptop cells sit left with a "click a count to filter" hint right from 1280px (hidden below so seven cells fit); phone cells split the width evenly with short labels, and the phone strip omits Completed (six cells fit 360px; the collapsible group remains available). The last group, Completed (last 7 days), starts collapsed (see Layout for group folding). It lists tasks, newest completion first. The Trees view strip uses the same cells by each tree's next step: Needs input, Review, Ready, Failed, Running, Waiting, Completed (phone omits Completed).

### Tasks view
The place to browse work. Header: Track session, New task (laptop; phone uses the top-bar button). Controls row: `Open | Closed | All` segmented left (Closed: archived tasks and members of done or archived trees), `All tasks | Not in a tree` right (`?tree=none`; phone `All | No tree`). Open view: the stat strip, then the state groups listing every task, tree members included (their meta line links the tree). Closed and All views: one Tasks panel. Trees have their own view (`/trees`).

The Awaiting review header carries a ghost "Complete all `n`" from two listed review rows (the listed rows only; the project scope applies). It previews every row, then confirms inline in the header: mono "Complete 6 tasks?", "Starts #12, #13" when auto dependents start, primary Complete, ghost Cancel (wraps on phone). Rows complete in turn, each lingering like a single Complete; blocked or failed ones list above the rows as red mono `#12: reason` lines with a ghost Dismiss.

### Task row (signature)
State chip (backlog tree members read `ready` or `blocked`, idle tone), row title with a mono meta line (project, model, relative time), one optional plain row chip, and right-aligned small actions. The row chip is, in order: `archived`, `tree done` or `tree archived` (plain; the tree icon for the tree ones, linking the tree) on a closed row (Project detail Closed and All views); `Steerer · 4m` (plain, the 12px ship-wheel icon, linking the steerer, titled "Steerer #480 is handling this. You will be notified in 4m.") while a needs-input row's notification is held for its tree's steerer, counting whole minutes up (`<1m` in the last minute) every 15s; the start time while timed; "needs #3 #5" while blocked; the schedule; "by agent". On the Dashboard and in Tasks, a tree member's meta line ends with its tree: tree icon and title in Ink Text, Strong Hairline underline at rest (Ink on hover), truncated at 26ch, opening the tree with the task selected (`/trees/:id?task=:taskId`); a closed tree's row chip links it instead. Blocked rows get no Start (Start early lives on task detail and the tree view). The task number leads the title as an unlinked mono 12px Slate tabular `#51` (6px gap, never wrapped apart), so needs chips ("needs #55") read against the rows they name; it sits outside the title link so the hover underline stays on the title. A completed row's meta line shows the acceptance time and elapsed run time, for example `completed 3h ago · ran 35m` (exact times on hover; `ran` is omitted until the run span is known). Needs-input rows take the One Yellow Edge; Open in {harness} is the primary action, unless Stage can answer the session's request on the row (see Requests). When the host has nowhere to open the session but it has a resume command (Codex), the row offers a small Copy resume command (`CopyButton`) in that slot, primary while it needs input, secondary otherwise, hidden while confirming or just completed. Last errors print as a truncated red mono line under the title.

Review rows (finished run, not completed) act Complete (primary, one click, empty note), Review (secondary, opens the completion drawer), then Open in {harness} (secondary). A blocked completion prints its reason as a red Alert under the title. When unclaimed auto dependents would start (tree open), the actions turn into an inline confirm: mono Slate "Starts #12, #13", primary Complete, ghost Cancel. On the Dashboard a completed row lingers in Awaiting review for 6s with an ok `completed` chip and a ghost Undo (reopens) in place of its actions, then moves to Completed; the state change in place is the feedback, no toast; "Completed #12" / "#12 back in review" go to a polite live region. The State panel shows the acceptance timestamp as “Completed”. Evidence shows `Session: <id>` or “No execution reference.” Accepted results use fixed copy for user completion and historical migration. Inferred completion uses the task harness’s descriptor copy, with “Completed automatically when its harness closed the session. This result stays unchanged when execution continues.” as the fallback. Inferred completion metadata uses the descriptor’s `via` label or “via harness”.

On the Dashboard and Tasks (every state group) the project gets its own column while the scope is All projects: laptop rows put a fixed 150px project cell right after the 118px state chip (mono 12px, Ink Text, medium weight, linked, truncated with the full name in `title`) so project names align down the whole page, and the project leaves the meta line. Phone rows print the project as a mono 12px Ink Text line above the title. A scoped view drops the column; Project detail rows keep the project in the meta line.

Closing work: the Dashboard and the Tasks Open view never list closed work (archived tasks, done or archived trees and their members); Tasks Closed and All do. Project detail's Tasks panel header carries a small `Open | Closed | All` segmented control (Closed: done or archived) beside the state select. Task detail's action row ends with Archive (secondary; standalone tasks only, disabled with "Stop the task first" while active) or Unarchive when archived, then Delete (danger, far right) only while the task has never started; an archived task adds a plain `archived` header chip. The palette lists closed tasks and trees too, each with a plain chip (`archived`, `tree done`, `done`).

### Task detail

The shared title, flag chips and needs-input alert sit above **Task** and **{Harness} session**. Task holds Stage’s actions, prompt, images and handoffs, accepted results, State, Tree, journal, timeline and Config. Session starts with a ruled header containing the harness, attempt, state, reason, session ID and session actions. Requests lead the session content when input is pending, followed by Message, Last word, Recent activity and optional harness sections. Usage and session details sit to the side on laptop. Before a session is recorded, show the empty state and launch details. Launch recovery lives on Session.

Phone and tablet use one column, with State ahead of the Task prompt. At 1024px both tabs use a fluid main column and a 340px side column. Below 768px the fixed bar above bottom navigation carries Start/Restart on Task, and Open/Copy resume, Stop and declared actions on Session, as available. Both views keep Answer reachable while the current session needs input.

### Last word
A read-only view of the selected session’s latest assistant reply.
- **Row preview:** needs-input and done rows only (task rows and external session rows). One line of Geist 13px Slate prose with an ellipsis, full preview in `title`, no marker or label. Laptop: under the meta line in the title column. Phone: its own full-width line between the title block and the actions, so it is not squeezed beside the state chip.
- **Panel:** "Last word" in the Session tab’s main column, with the reply’s relative time (mono, exact time on hover) and Copy in the header. The body renders sanitized markdown (`.prose-reply`): Geist 14.5px at 1.55 leading, 75ch measure, headings at body or title size (never competing with the page H1), square Slate list markers, inline code and code blocks in mono on the Bench Gray panel with hairline borders (code blocks scroll sideways, prose wraps anywhere), hairline tables and rules, and a 1px Strong Hairline quote rule. Raw HTML shows as literal text. A truncated excerpt ends with a hairline-ruled mono footer, "Excerpt. Full reply in {harness}", linking to the session. Hidden until a reply has been read.

### Prompt

First panel in the Task tab’s main column. Preserve mono text, Copy, six-line clamp and Show all / Show less. Ordinary tasks retain launch notes, images and handoff. Adopted tasks read only their first own user message, skipping inherited history, with bounded full-text expansion, when the connection offers operation `opening`; otherwise the panel's empty state is the harness's declared reason (else "{harness} does not share a session's first prompt."). Keep loading, empty ("It shows once {harness} records the first message."), offline ("{harness} not connected, so stage can't read the prompt.") and retry states.

### Transcript

Full conversations stay in the harness. Where the harness declares `activity`, Session shows a bounded Recent activity panel. It has a plan checklist, changed files with addition and deletion counts, and ruled feed rows with kind icons, mono previews, status and relative time. Truncation adds “Older activity in {harness}”. “Full transcript in {harness}” reuses the header destination. The current session polls every five seconds while visible and running or waiting for input. A past attempt reads activity once per visit. [Session surface](docs/adr/0014-session-surface.md)

The Claude Code session slot puts the reported model, permission mode and effort in the header as wrapping mono metadata. Missing values are omitted.

### Alert
Tinted callout: tone border at 50%, tone fill at 9% over surface, a 7px square marker, 13px text, actions right. `need` for waiting-on-you, `bad` for errors.

### Start control and schedules
The task drawer's Start segmented control (Now / Backlog / At time / Repeat; Backlog / At time when editing a backlog task; Backlog only for a new task joining a tree with needs, Now / Backlog / At time for a new tree root (schedules take no tree); hidden when editing a task with needs; there is no After task: order lives in trees) decides what the single primary footer button does, and its label follows: "Save & start", "Save to backlog", "Save · starts Nov 12 02:00", "Save schedule". At time shows date and time inputs, a zone select (browser default) and a mono "starts in" line. Repeat shows a preset segmented control (Hourly, Daily, Weekdays (Mon-Fri on phone), Weekly, Cron) with its minute, time, weekday or raw cron field, a zone select and a Bench Gray mono box with the summary and the next three fires from the server. On phone the segmented controls fill the width in equal cells with short labels.

A backlog tree member (or a new task joining a tree from the tree view's Add task, whose drawer opens with a plain Alert "Joins tree X, needing #3. It waits in backlog until its needs are completed.") shows a `TREE START` select: "Inherit (tree: auto)" (names the tree's mode when known), "Auto: starts when its needs are completed", "Manual: reads ready, you start it". Inherit saves as no override.

Schedule rows share the task row grid: a plain "repeat" chip (idle "paused" when paused) with the repeat icon, title, mono summary, zone and "next in", then Run now and Pause/Resume. The last run is plain meta ("last #131 finished") unless it failed, was interrupted or blocks the next fire: then a "last #208" state chip fills the column before the actions (phone: the state word, tinted, in the meta line), and a blocking run adds a tinted "skips while #208 runs" or "skips until #214 is answered" after "next in" (not while paused). A healthy schedule row carries no state color. A timed backlog task shows a plain chip with the clock icon ("at 02:00") in the row chip slot and its Start reads "Start now"; a spawned task shows a plain repeat chip linking its schedule. Stage adds a "Scheduled · next 24h" group between Backlog and Completed (timed tasks leave Backlog for it); it has no stat strip cell. Schedule detail: Run now (primary), Pause, Edit, Delete (danger, inline confirm); Runs in the main column, When / Config / Prompt panels in the side column.

### Tree view
`/trees/:id`: H1 tree title, then an Ink Text mono `// Project: stage` line (`Projects: alpha · beta` when spanning projects; sorted distinct current-member project names, wrapping without truncation; `No member projects` when empty), then `7 tasks · 3 levels · 2 projects · auto · created Nov 14 · active 6m ago` (created is a date anchor, `today 10:20` / `yesterday 16:20` / `Nov 14` / `Oct 3, 2025`; active is relative in Ink Text; exact times on hover; a `completed` chip once every member is accepted; a plain `done · 2 not run · 1 cancelled` or `archived` chip once closed), actions right: Auto | Manual segmented (tree start mode), More (popover: Rename (inline form above the H1), Add existing task, Add session, a Strong Hairline divider, Mark done, Archive tree, Dissolve tree in red, Delete tree in red only when the server permits deletion; Dissolve and Delete open a `ConfirmInline` above the tabs, "Delete this tree and its 4 tasks? None has started. This can't be undone."; Mark done and Archive apply at once and, while a member is launching, running or needs input, sit disabled with a mono Slate reason under the label, "#4 is running"), Add task (secondary; "New task after #4" while a node is selected, opening the New task drawer prefilled with the tree and that need), Start ready `n` (primary, disabled at 0). Below lg the actions wrap under the meta line. A done or archived tree drops Auto | Manual, Add task, Start ready and the structural menu items (and the status it already has); a secondary Reopen (or Unarchive) takes Start ready's slot, a mono Slate line under the header reads "Marked done Nov 16. Remaining tasks won't start." (or "Archived Nov 16. ..."), graph rows lose Start and Retry (Open in T3 stays), and the side panel's node controls read only ("Reopen the tree to change needs.", no Remove from tree). The state change in place is the feedback: no toast.

- **Graph list:** the `TREE` panel is one ruled row per member in level order (roots first; a level orders by the mean position of its needs, then id), a Strong Hairline rule between levels. Laptop columns share one subgrid: level (Slate mono `L2`, first row of a level only), state chip (`NodeChip`; ready / blocked idle, dashed while backlog), mono `#id` and the title (Geist 500 14px, truncated; Ink Text mono project on its own line beneath the title for every task, including single-project trees and phones; project names wrap anywhere), needs (mono `#3 #5`, a failed need in red with "blocked by failed #5" on hover; `root` for a root), time in state right-aligned (Ink Text mono; Waiting Yellow text while it needs input), then one action: Open in T3 (needs input) > Start (ready; a blocked node's Start arms "Start before #3, #5 complete", which keeps its needs) > Retry (failed) > Open in app. A Paper column header row (Slate Label) tops the list. Rows are at least 52px and grow for wrapped project names; phone rows print `#id` title, then project, then chip · `needs #3` · time in state · action right, with no level label.
- **Time in state:** "running 18m", "waiting 6m" (needs input), "done 22h ago", "failed 3h ago", "ready 3h", "queued 2d" (blocked or plain backlog). Counted from the task's `stateSince` (latest event into its state); exact time on hover, plus `ran 35m` for done.
- **Row states:** needs input takes the One Yellow Edge (inset, so columns stay aligned); the selected row a 1px Ink Text outline inset 2px.
- **Layout:** a small List | Graph segmented control sits in the `TREE` panel header (List by default; Graph at `?layout=graph`, kept next to `?view=`). There is no hover effect on rows. The panel meta reads "click a task to edit" (list, laptop), "steps run top to bottom · click a task to edit" (graph, laptop) or "tap a task to open it" (phone). On lg+ the title selects the row or box; below lg it opens the task.
- **Graph:** a top-down layered graph in the same level order as the list. Each level is a band with an 88px step gutter: Ink Label `STEP 2`, then a mono line `now` (Sky; any member launching, running, needing input or ready, and the band takes a 5% Sky tint), `done` (every member is completed) or `waits` (Slate), plus `3 parallel` when the level has several members; Hairline between bands. Boxes are 224x80, flat Surface with a Strong Hairline (Sky while running or launching, dashed while backlog, One Yellow Edge while it needs input, the selected box the inset Ink outline): `NodeChip` and time in state (mono, Waiting Yellow while it needs input) on top, mono `#id` and the title (Geist 500, truncated, full title on hover) below, then the Ink Text mono project on a third line for every task (truncated with the full name on hover and included in the accessible task label; the tree header retains the full names); no actions (they stay in the list, side panel and task detail). Arrows run from each need's bottom centre to its dependent's top centre: solid Ink 1.5px once the need is completed, dashed Slate while it has not, dashed Red when it failed. Rows centre on the widest level; wider graphs scroll horizontally (phone included). Boxes sit in the DOM in level order, so Tab follows the execution order.
- **Side panel:** lg+ only, 340px, shown only while a node is selected; with nothing selected the `TREE` panel spans the content width. The selected node lives in `?task=` (written with history replace, cleared on close), so task detail's Back to tree returns to it; below lg the row keeps its selected outline. The selected node (`TASK #4` header with a mono `Open →` link to task detail and a ghost close that hides the panel): its title and project, a `STATE` section (the State picker, a Slate line saying what the state means, then the node's one action: Start (primary), Retry or Open in T3), then the Tree node controls (see Tree panel) and Remove from tree.
- **Activity:** the Activity view's `ACTIVITY` panel reads the durable event journal at full content width, newest first, twenty per page with Load older entries. Ruled rows use mono timestamps, immutable entry links, event labels and task/source-session links; moving events link both trees. Task links open task detail even after a member leaves. Truncated entries fetch the full record. A muted history-start notice explains that tree events begin at journal rollout and earlier task history remains on each task.
- **Views:** Tasks | Notes | Activity below the header, Tasks by default, Notes at `?view=notes`, Activity at `?view=activity`. Tasks retains the graph and side panel. Notes and Activity fill the content width. The note draft survives switching tabs.
- **States:** five pulsing skeleton rows while loading; only a deleted or unknown tree is a 404 page ("Tree not found", Back to trees). Empty and dissolved trees retain their archived identity, notes and history. Dissolve detaches tasks, archives the tree, and stays on its page. A completed tree shows the completed chip; a done or archived tree its plain chip. Delete is shown only when server eligibility permits it, including protection for recorded work by departed members.

### Journals
Tree Notes and the task detail main column share one ruled `NOTES` panel. The composer leads with a snippet-aware textarea, an optional category select (Uncategorized, Finding, Decision, Blocker, Outcome) and one primary Append note button. Tree composition defaults to This tree, with current tasks available in a Save note to select; task detail saves to that task. A short hint explains ownership: task notes move with the task, tree notes stay. Closed work accepts notes without reopening or scheduling. Notes are append-only, with a 16 KiB UTF-8 byte limit and an inline byte count. Submission failures preserve the draft and retry key; only success clears it. A task that leaves during composition requires choosing the destination again.

Below the composer, Notes | Events | All defaults to authored notes. A category select (All categories, Finding, Decision, Blocker, Outcome), literal text search and (tree only) task-ID filters apply with Filter; Clear resets them. Entries are newest first, twenty per page, with Load older entries. Each row shows its permanent entry link, timestamp (exact on hover), category or event label, owner, attempt when present, and available source task/session and moved-tree links. Markdown uses the existing sanitized renderer. Previews are bounded to 600 runes; Read full entry fetches the immutable record and Show preview collapses it. Deep links use `?entry=<id>#journal-<id>` (plus `view=notes` on trees) and load that entry even outside the current page or filters.

Loading uses pulsing bars; errors keep the composer mounted and offer Retry. A stale tree cursor explains the membership change and offers Reload entries from the first page. An empty filtered list invites a note or a filter change. The event view identifies the start of tree history. New task and adoption drawers offer Initial note; task editing has no mutable notes field. Schedules keep Notes template, copied into each spawned task as its initial journal note. Duplicates do not copy journal entries.


`/trees` (sidebar Trees under Tasks; a phone tab between Tasks and Schedules; follows the project scope): header with Track session, an `Open | Closed | All` segmented control (Closed: done, completed or archived trees). Open view: the Trees stat strip, an Active panel (open trees with work left, Needs input, Review, Ready, Failed, Running, Waiting order, then most recent activity), and a Completed (last 7 days) group, collapsed by default. A stat cell lists only its group. Closed and All: one Trees panel, most recent activity first. Each Tree row's task count (`16 tasks`, first on its mono meta line) is its fold toggle: a 12px Slate chevron before it, text and chevron Ink on hover, no fill, the hit area padded past the text so the row keeps its height, `aria-expanded`. It expands the members in place as the tree view's graph list (level order; a member title opens the task). The title aligns with the panel header label. An empty line per view.

### Tree row
A tree in a ruled list (the Trees view). Laptop grid: title (Geist 500 body, linked) with an Ink Text mono `Project: stage` line under it (`Projects: alpha · beta` for several, sorted distinct current-member names, wrapping anywhere; `No member projects` when empty), then the Slate `treeMeta` mono line (and a `completed 3h ago` chip with `Completed <date time>` on hover; a plain `done · ...` or `archived` chip, closed time on hover, once closed, whose only action is a secondary Open), a time column (lg+, right-aligned mono: `active 6m ago` in Ink Text over `created Nov 14` in Slate; the list's sort key and the tree's age, exact times on hover), a 240px state column, a 128px action column. Below lg the times print as one mono line under the meta (`active 6m ago · created Nov 14`). The state column is the segmented state bar over a mono line: the next step left ("1 needs input" in need text, "2 ready", "1 failed" in red, "2 to complete", "3 running", "waiting on needs", "all completed") and `2/5 completed` right. A steered tree adds a plain chip after the title chips: the 12px ship-wheel icon and `steered` (`steerer stopped` once the steerer failed, was interrupted or cancelled), titled with the steerer's `#id` and title. Action: View tasks (primary, opens the tree) when a member needs input, "Start ready n" (primary small, starts every ready member) when any is ready, else a secondary Open. Phone: title and action on top, the bar full width below.

Segmented state bar: an 8px tall row of square cells with 1px gaps, one cell per member (one stretch per state above 30 members), finished work first: completed (ok), done, running, needs input, failed, ready (idle fill), not-started backlog (hollow, Strong Hairline outline), cancelled (idle at 45%). One `role="img"` label carries the counts ("7 tasks: 1 completed, 1 running, ...").

### Tree panel
A tree member's task detail says where it sits and how to get back, before any action. The meta line reads `// xpop / Ship auth / task 12`, the tree title linked and underlined at rest (Strong Hairline underline, Ink on hover). Between the header (and its flag chips, when any) and the action row sits the tree bar (`nav`, label "Tree"): a 1px Strong Hairline box, no fill, holding the Slate tree icon, the Label `TREE` (laptop only), the tree title (Geist 500, linked), the plain closed chip when done or archived, a mono Slate `task 3 of 5` (`3 of 5` on phone; level order), the mini level view, and a secondary "Back to tree" button (back chevron) far right. The mini level view is one inline run in execution order: each level's cells, a 12px Slate step chevron between levels, wrapping as needed; each cell a 24px (32px on phone) Strong Hairline box with a 6px tone square and mono `#id`, dashed while backlog, linking the task; the viewed task gets the Ink outline. On phone the title takes the first row, cells and the button share the second. The tree title and Back to tree open the tree view with this task selected (`/trees/:id?task=:taskId`).

The side column opens with a `STATE` panel (the State picker and its Slate meaning line; below lg it sits right under the action row instead), then the `TREE` panel, which holds only the shared Tree node controls, each a hairline section with a Label header:
- `NEEDS · n`: one line per need (`#id` title, its chip, a ghost remove while backlog) and a cycle-safe "Add a need…" select; a root says "None. It is a root: it starts when you start it." Outside backlog: "Needs change only while it is in backlog."
- `DEPENDENTS · n`: one line per dependent with its chip ("Nothing runs after it yet." when none), then, while the tree is open, two stacked full-width small secondary buttons, each with a mono Slate caption under it: "New task" (plus icon; "Create a new task that waits for #12.", opens the New task drawer with that need) and "Existing task" (link icon; "Pick one from any project. It moves into this tree and waits for #12.", opens the "Existing task after #12" drawer). Same layout whether or not dependents exist.
- Backlog only: `START MODE` (Inherit / Auto / Manual segmented, mono line naming the tree's mode and what it means), the Hand off context checkbox row (only with needs).
- Footer: Remove from tree (danger small, inline confirm naming what happens to its dependents).
Lines open the task here (they select the node in the tree view). A task outside any tree gets a `TREE` panel reading "Not in a tree. Add it to one so it can need other tasks, or be needed." with a small "Add to tree" in the header (an archived task reads "Not in a tree. Unarchive it to add it to one." without the button): a drawer with a radio list (New tree first, then active trees with their meta); New tree adds a title (the task's title) and an Auto | Manual start mode; the task joins as a root. An adopted task shows a plain `tracked session` chip in the header chips, has no Start or Duplicate, and its Prompt panel reads the session's first message from T3.

### Steering strip
Tree steering ([ADR 0013](docs/adr/0013-tree-steering.md)) on the tree view, between the header and the tabs: one line in a 1px Strong Hairline box, no fill, like the task detail tree bar: the Slate 12px ship-wheel icon and the Label `STEERER` (laptop only), then the state.
- None (open tree): a Slate sentence, "No steerer. A steerer task watches this tree, answers its members and accepts their work.", and a small secondary "Add steerer" right. A done or archived tree without a steerer shows no strip.
- Active: the steerer's state chip, its mono `#480` and linked title, a mono Slate delivery line ("Last update 2m ago · 3 pending · hold 5 min"; "No update sent yet" before the first digest; "Starting" before it launches), then small Replace (secondary) and Stop steering (ghost) right. A failed delivery adds a full-width truncated mono line in `need-fg`, "Last delivery failed: <reason>" (it retries on its own, so not red).
- Stopped (the steerer failed, was interrupted or cancelled while still bound): a Waiting Yellow Alert in its place, "Steerer stopped. Needs input goes to you." with the steerer's link and state chip, primary small Replace and ghost Stop steering.
Stop steering opens a `ConfirmInline` under the strip, "Stops the steerer's session. Needs input goes to you.", red "Stop steering". Warnings from a set or stop (an old session that would not stop, a launch failure) show as a Waiting Yellow Alert with a ghost Dismiss. Add steerer and Replace open the task drawer in steering mode: a plain Alert naming the tree ("Steers Tree X: a standalone task that starts now ..."; Replace: "Replaces steerer #480: its session stops ..."), Connection and Workspace (both required; a connection whose harness can't send lists disabled with a `can't send` chip and the harness's reason, and a chosen one blocks the submit with that reason), an optional Title (placeholder `Steer: <tree title>`), Intent (snippet textarea, no images, "What the steerer drives the tree toward. Stage adds the tree, its notes and the playbook."), and the Harness group; no project, start, tree start or initial note. The primary reads "Start steerer" (Replace: "Replace steerer"); Replace prefills intent, destination and config from the current steerer. The tree's More menu ends with a `STEERER HOLD` group: Off, 2 min, 5 min, 10 min, 15 min (plus the current value when set elsewhere), the current one checked; picking one applies it at once.

### Steering panel
A steerer task's side column (after State) holds a `STEERING` panel: a definition list with the tree (linked), Binding `active` or `ended · <reason> · 2h ago` (replaced, stopped, tree finished, steerer task removed), and while active Delivered `through #41`, Last update (relative, or `none yet`) and Pending; a failed delivery adds the mono `need-fg` line; then a small secondary "Steering events". While the binding is active the "Not in a tree" panel is hidden. Steering events opens a wide drawer with the tree's feed, oldest first, 50 a page with Load more: one ruled row per event, a mono Slate line (`#42`, relative time, the event in Ink words such as "needs input" or "decision note", the member's `task #10` link, "by the steerer" for its own, a plain `pending` chip for what the next digest carries), then the member's title and a Slate detail (reply excerpt, error, note text, "Steerer did not answer"), clamped to three lines.

A held member (its needs input waits for the steerer) keeps the needs-input alert on task detail, worded for the hold: "Steerer #480 is handling this. You'll be notified in 4m if it doesn't answer." (plus the harness's own note when it has requests Stage can't list); the Requests panel, Reply and Open in {harness} stay, since the first answer wins. In tree Activity and Notes, steering events read in words ("steering started", "steerer replaced", "steering ended" with "Steering by #12 ended: tree finished", "steerer hold" with "Steering hold: 5m to off"). A held notification that reached the user late carries its note ("Steerer did not answer", "Steerer: <reason>") as a mono Slate line under the title in the inbox row and the toast.

### Handoff
A task's handoff lives in its Prompt panel, because it is part of the message sent; one section per need. Backlog with handoff on: one mono Slate line under the prompt, "+ on launch: handoff from #4, #5 (final reply, branch, session link)". Launched: one hairline-ruled section per need under the prompt (and images), each with a Label `HANDOFF FROM #4`, mono Slate `sent HH:MM` (exact time on hover), a small "Copy handoff" button right, a mono char-count line, and a collapsed native `<details>` ("Handoff text") opening the block in the same mono pre as the prompt (max 384px, scrolls). A skipped need prints one mono Slate line, "Handoff skipped: <reason>". No color: it is context, not state. The panel's Copy still copies the prompt only. A steerer's launch brief follows the handoffs in the same form: one hairline-ruled section, Label `STEERING BRIEF`, mono Slate `attempt N`, a small "Copy brief" button, a mono line "6,234 chars appended to the prompt, frozen for attempt N" and a collapsed `<details>` ("Brief text"); no color. The Hand off context checkbox row sits in the Tree panel (and the tree view side panel) while backlog, and in the task drawer when editing a task with needs.

### Launch options
Where a session starts is the harness's own launch options (a new worktree, its base ref, its branch), rendered in the task and schedule drawers' Harness group with the rest of the connection's options, by kind and never by name: `choice` a picker, `boolean` a Harness default / On / Off segmented control, `text` an input with the placeholder "Harness default". Each takes the harness's `label`. Choice values use declared labels when available. Harness default clears the override. Off saves an explicit false. An option with `dependsOn` shows only while its dependency holds (T3's Base ref and Branch only with New worktree on), and a hidden one is not saved. Task detail prints the chosen options once in the session panel. Schedule detail prints them in its Config panel. There is no worktree base picker: a dependent never starts from its predecessor's branch; the handoff names that branch. No color: it is config, not state.

### Harness group
The task and schedule drawers (`packages/app/src/components/HarnessFields.tsx`, from `TaskForm`) hold one ruled group under the prompt: a Hairline top rule with 12px under it, then a `FieldGroup` legend "Harness · {connection}" (`label-caps`; the connection name carries its machine from two machines up). Its fields come only from the connection's capabilities, in the harness's order: Model, each launch option by kind (see Launch options; a `dependsOn` option only while its dependency holds), Permission, then the mode under the harness's `modeLabel` ("Interaction mode" when absent), each labelled by the harness, two columns from 768px and one on phone. Every picker starts at "Harness default"; a saved choice the harness no longer offers reads "id (unavailable)", disabled, with the harness's reason as the hint. Loading uses the field skeleton. Retry keeps the draft when the read fails. Reset to harness defaults clears overrides without changing other fields. Settings › Connections › Manage › New tasks reuses the same fields, model and its options only, under its own Model sub-section. No new color.

### Capability list
Settings › Connections › Manage (`packages/app/src/components/ConnectionCapabilities.tsx`), once the connection is set up and after the harness's setup panels: a flush "Capabilities" Panel (meta `declared by {harness}`) listing what the connection declares in its capabilities' `operations`, one ruled line per declared operation in Stage's fixed order: Start tasks, Stop from Stage, Track existing sessions, Prompt images, Answer approvals and questions in Stage, Send follow-ups (one generic map, `OPERATION_LABELS` in `packages/app/src/lib/capabilities.ts`; never per harness). Each line: the label (Geist, Ink when available, Slate when not) and a right mono Slate `available` / `unavailable`; an unavailable one with a reason adds the harness's reason as a small mono Slate line under it. The panel only shows declared operations that gate user controls. It takes no space when none are declared. No chips, markers or new colors: it is a declaration, not a state.

### Launch meta line
The Session details panel shows the chosen launch model and non-default options for the current attempt, in the harness’s labels. Each attempt shows its reported branch and directory when available. Recorded facts sit below. The current Session can link to browser address settings where declared.

### Runs panel

Task run span remains in metadata. Detailed harness runs are opened in the harness.

### Sessions

Explicit Track session drawer from Stage and Project; Add session from a tree. Paste a T3 link or ID, Preview session, inspect title/project/state, then confirm. Optional title and initial note for a new owner. Show actionable eligibility and lookup errors in place; keep input during retries. The Session header’s attempt switcher lists recorded attempts newest first and marks the current one. Past attempts have their own route at `/tasks/:id/session/:attempt` and show a read-only banner, their last observed state, reply, usage, facts, links and activity where available. A pending launch can leave every recorded attempt read-only. Past session references open the recorded attempt. The OpenCode header adds a wrapping mono line for the reported agent and model, with the variant beside the model. The shared Activity and Usage panels carry the rest.

T3 adds provider instance, model and runtime mode as wrapping mono metadata
below the Session header's state and thread ID. The Recent activity file
list shows paths alone when the harness has no change counts.

The Codex header uses a wrapping mono metadata row for the thread's model, reasoning effort and reported permission preset. Custom settings show the approval policy and sandbox individually.

### Image dropzone

Backlog task prompts accept image drops and Add image. Preserve thumbnails, upload/error states, the 8-image limit and read-only image links on launched tasks.

### Notifications bell
A secondary 32px button (40px on phone) in the sidebar bottom row (after the theme toggle) on laptop, and in the phone top bar (before search) with the bell icon. Always square: the unread count rides a corner badge (see Chips), plain, or need-toned when an unread needs-input item is still waiting. Zero unread shows the bell alone.

### Needs input panel

Keep the compact needs-input alert above both tabs, and mark the Session tab with a need-colored square. Task and past attempts offer an **Answer** arrow link to `/tasks/:id/session#requests`. Requests lead the current Session content. Needs-input rows and notifications open `/tasks/:id/session`.

### Requests (answer in Stage)

`packages/app/src/components/Requests.tsx`. The current Session shows requests where its connection offers `respond`. The harness can also answer them.
- **Session tab:** the “Requests” panel (`#requests`, flush, mono `N pending`) leads. Each request is a ruled block: title (body, medium), detail (13px Slate), the preview (command, path, input) in a sharp Bench Gray mono box that wraps, long ones folded to 8 lines with a mono "Show all N lines" disclosure. Approvals: one button per option in the harness's order, the first primary (Allow), the rest secondary (each Always allow names the rule and where it is written; long labels wrap and the button grows); the text option (Deny) opens an inline "Reason for <harness> (optional)" field with Deny (primary) and Cancel. Phone stacks the buttons full width. Questions: one fieldset per question, the header as an inline mono Slate label before the prompt (never above it), square radios or checkboxes (`ChoiceRow`, description under the label, "pick any" meta on multi-select), Other with its input indented to the labels, one primary Send disabled until every question is answered. Once answered the block keeps its preview and shows a mono Slate status line, "Answered · waiting for <harness>" or, when the harness was answered first (404), "Already answered in <harness> or expired"; errors use the bad Alert. Answers are guarded by the task's state revision (first answer wins): one that lost (409 `already_answered`) keeps its controls and draft and prints the same mono Slate status line under them, "Already answered, by the steerer or in the harness.", while the task refetches.
- **Rows:** a needs-input task row or inbox row with exactly one pending approval prints a one-line mono Ink summary under its title ("Run a command · touch a && echo b", truncated, full text in `title`) and takes small Allow (primary) and Deny (ghost, no reason) in its actions; a question or several requests show a small primary "Answer" ("Answer 2") link to `/tasks/:id/session#requests`. The session action drops to secondary. Toasts and silent waiting sessions are unchanged.

Approval detail preserves separate lines for the working directory and reason. The mono preview carries the command, changed paths with counts when available, or the requested permission profile. Permission actions state whether they apply to the turn or session.

### Session composer

The current Session has a single multiline Message field. A running turn offers **Steer** and **Queue** when declared. An ended turn offers **Send**, including a needs-input turn once Stage knows there are no pending requests it needs to handle. When delivery is unavailable, show the reason and link to the session actions for Open or Copy resume when available.

Ctrl/Cmd+Enter uses the first available delivery in Steer, Queue, Send order. The 32 KiB UTF-8 limit and blank validation disable delivery without clearing text. Drafts survive tab, attempt and state changes. Successful delivery shows “Steered · time”, “Queued · time” or “Sent · time”. A session_busy reply displays its reason inline and keeps the draft. A needs-input Send is guarded by the task’s revision, like a request answer.

When the current session declares `queueManage`, a non-empty **Queued messages** list sits directly under the composer. Hairline rules divide text previews in native queue order. Each row shows time and source when reported, with **Steer now** and **Cancel** actions that wrap on phones. Cancel asks for inline confirmation. A promoted message that is still pending shows **Steering** and can be cancelled. Refresh on successful Queue sends and actions, and poll every five seconds during visible running or needs-input sessions.

### Checkbox
16px square native checkbox: Strong Hairline border on Paper; checked fills Ink with the authored check in On-Accent; focus-visible adds a 1px Paper gap and 1px Ink ring; disabled at 55%. Used in a ruled list (Settings Notifications events) and as standalone rows ("Hand off context" in the Tree panel, tree view side panel and task drawer; "Complete tasks when settled in T3" / "Complete tasks when archived in OpenCode" under a hairline in the T3 and OpenCode connection cards, mono hint below "Only successful runs. Change the task's state to undo it.", saved at once, optimistic): whole row is the label, 44px tall on phone (32px laptop), Bench Gray hover, checkbox, 6px square state marker, Geist label, mono Slate hint right.

### Settings
Three tabs, each its own route: General (`/settings`, the default), Connections (`/settings/connections`) and Notifications (`/settings/notifications`); old `/settings/machines/:id` links land on Connections. Every tab leads with the H1 "Settings" and a meta line naming the tab (`// Connections · 2 machines · 3 connections`), then the tab bar (`TabNav`): boxed mono uppercase cells on a Strong Hairline, the current one in Ink Text with a 2px Ink underline (the active-tab stroke), the rest Slate with a Bench Gray hover. Phone: three equal cells across the width, 40px. Connections carries a 6px square in the status button's tone while a connection needs attention. The More page keeps one Settings link.
- **General** (one column, max 720px): User, Theme, Info. **Info** describes the running Stage from `/api/health` in every host: Details rows Version (`v0.9.4 · 65bac1c · api 7`), Running as (`main`, plus `· per-user service` or `· started from a terminal` in the desktop app), Started, Identity; the desktop app adds Socket (a remote's URL), Desktop app (`Stage 0.9.4`), a small "Copy diagnostics" in the header, and under a hairline the "Open Stage at login" Checkbox row (mono hint "starts hidden, with Stage") and, only when this machine joined another server as a worker, that worker row with Restart and Leave.
- **Connections**: header actions Sync now (secondary; its result is a plain Alert under the tabs) and Add machine (primary, the pairing drawer). One flush `MACHINES` Panel (meta `synced every 5 min`), one group per machine, the main machine first. A machine header on Bench Gray: 6px marker, name in Geist 500, `main` or `worker` as Slate mono `Meta`, the mono status line; right: Rename (ghost), Add connection (secondary) and Remove (danger, workers only). A worker on another API version adds a mono hint under its header. Its connections follow, indented to the name, one line each and never expanding: harness name in Geist 500, a plain `default` chip on the default connection, a truncated mono Slate where-line (T3: `controlbox · v0.1.9 · token expires in 17d`; OpenCode: `127.0.0.1:4096 · local service`; Codex: `codex-cli 0.171.0 · dee@example.com`), the state chip, small Manage and a ghost Disconnect (never disabled). Disconnect opens the harness's `ConfirmInline` under the row; once confirmed the row leaves. Manage opens a right drawer, "{harness} on controlbox", holding the harness's setup panels (its entry in the `packages/app/src/harnesses/` slot registry, else the generic schema form); once the connection is set up, a `NEW TASKS` Panel follows its status panel, and the Capabilities panel (see Capability list) closes the drawer: Set as default (secondary; when default, the `default` chip, a hint and a small Clear default), then a ruled Model sub-section (meta `pinned` or `harness default`) with the harness's model and option comboboxes and Pin, or Save pinned and Unpin. Harness-specific new-task settings join this panel. Add connection opens a `Menu` of the machine's connectable harnesses, connected ones disabled with "already connected"; picking one opens the same drawer. Add connection is disabled (reason as title) while the machine is offline or needs an update, or when every harness is connected. No connection reads as a mono hint "No connections. Connect a harness to run tasks here." followed by a small secondary button per connectable harness (Connect T3, Connect OpenCode) that opens its drawer. An offline worker lists its connections on record with an idle `offline` chip, no actions, and "editable when studio is back". Phone: the whole connection row is a button that opens Manage (Disconnect lives in the drawer). A footer hint: "Workers run `stage worker` and connect out to the main machine. Harness credentials stay on each machine."
- **Notifications**: the Notifications panel (see Settings notifications), max 720px.

### Settings notifications
One panel, two sections split by a hairline. `EVENTS · all devices`: the global event filter (Needs input, Failed, Ready, Done; Ready in the idle tone, hint "needs are completed, start it"), each toggle saving at once (optimistic; a failed save reverts and shows a red Alert); a muted line explains that unchecked events are dropped, or reads "All events off. Nothing is recorded or pushed." `THIS DEVICE`: per-device enable, test and disable; the header chip shows this device's state. In the browser it is Web Push ("Web Push to this browser for the checked events.", success "Test notification sent."). On desktop the same section, chip and buttons drive native notifications: intro "System notifications from the Stage app for the checked events, when Stage is not in front."; unsupported reads "This system has no notification service Stage can use."; blocked is a plain Alert, "Notifications from Stage are turned off in system settings. Allow them there, then try again.", with small Open System Settings (when the host can open them) and Try again; on adds a ghost "System settings" after Send test and Disable; when the host cannot open its settings, a muted "Allow notifications for Stage in your desktop's settings." follows; success reads "Test notification shown." No new components, tokens or colors.

### Status button
The first button of the sidebar bottom row: a secondary 32px square, like the bell, with one 16px icon over everything Stage depends on: every set-up harness connection on every machine (the main machine and each worker), each read on its own machine. All healthy: a check in Settled Green. A problem: a triangle alert in its tone (`need-fg` or red); from two problems up a corner badge in the worst tone (red over yellow), as the bell's does, so the button stays square. While anything is checking, a Slate refresh; with nothing configured, a Slate plug. Its accessible name and title carry the sentence: "All connected", the one problem ("OpenCode rejected", "T3 pairing expired"), "n need attention", "Checking", "Connect a harness". A connection counts when set up (its Settings row: T3 with a token, a set URL or a live connection; OpenCode connected to a service); tones follow the Settings chips (yellow: T3 unpaired or expired, OpenCode refused the sign-in or its pairing expired; red: unreachable, rejected the pairing, unavailable, or its status call fails). A worker that is offline or needs an update makes each of its connections a red problem ("controlbox offline", "controlbox needs an update"; detail: when it was last seen, or the build it runs). With two or more machines the sentence names the machine ("T3 pairing expired on controlbox"). Click toggles the details panel (Bench Gray fill while open; no hover preview); Esc, an outside click or tabbing away closes it. The panel opens 4px above the bottom row, its left edge level with the button, 300px, flat Paper, Strong Hairline, no shadow: one section per group (Harnesses on a single machine; one per machine, by its label, from two machines up) with a Bench Gray `label-caps` header and its count, problems first, then one ruled line per item linking to Settings: 6px square, Geist name (in its tone when a problem), mono state right, then mono Slate detail lines (T3: `controlbox · v0.1.9 · URL`, `synced 2m ago · token expires in 17d`; OpenCode: `host:port · sign-in`, `checked 5m ago`) and a red error, 2 lines max. An "Open settings" footer row (settings icon) closes it. With nothing configured the panel reads "No harness connected yet". Phone shows the same sections inline on More, and the More tab carries a square in the status tone only while a problem stands.

### Page location
No content bar: the page title is the first thing in the content column on every breakpoint. A page's trail (`PageHeader` `trail`: project / tree / task n; project / schedule n; project label / tree n; Projects / project n) starts its `//` meta line, earlier steps ink links (underline on hover), joined to the rest of the meta by ` · `.

### Back and history list
Back and forward go through this tab's in-app history only (never to a page before Stage). A browser tab (and a `minimal-ui` window) keeps its own arrows, so the sidebar drops them and the mark stands alone in the header; the desktop app and the installed standalone web app show them. On laptop two ghost 32px arrow buttons (chevron left and right, 16px icons, ink when available, 45% when there is nothing that way) end the sidebar header; right-click or a 500ms press on an arrow opens the history list. On phone a secondary 40px Back takes the glyph's slot in the top bar when there is a page behind and the page is not a tab root (Dashboard, Tasks, Trees, Schedules, More); otherwise the slot shows the glyph, and the slot keeps its width so nothing moves. Phones get no forward. The history list is a 256px `Menu` under the arrow: flat Paper, Strong Hairline, no shadow, up to 10 pages nearest first, each a 32px row (44px on phone) with the page kind in mono Slate (`task`, `tree`, `project`, `schedule`) then its name (the page title once visited, else `task 12`), truncated. The same name is the document title, `<name> · stage` (`Tasks · stage-app · stage` under a project scope, `Connections · Settings · stage`), so the browser's own history list reads the same. Arrow keys move, Enter goes, Esc or an outside click closes and focus returns to the arrow. After a reload the pages before it are unknown: Back still works, forward and the list wait until pages are visited again. The desktop app adds Go > Back and Forward (Cmd+[ and Cmd+] on macOS, Alt+Left and Alt+Right elsewhere) and the mouse side buttons.

### Dashboard connection alerts
Connection recovery appears above the Dashboard without replacing planning, for every harness alike: with no harness set up anywhere, one yellow Alert "Connect a harness to start tasks." (the first words link Settings › Connections); otherwise one compact Alert per status-button problem (see Status button), in its tone (yellow `need`, red `bad`), reading the problem's sentence and "Fix in Settings" (a rest `TextLink`). It reads the status button's data, so it never polls a harness of its own. Pairing and retry live in Settings, the connection destination.

### Connections
Group connections by machine. Each laptop row aligns the state, reason and Manage controls. Below 768px the reason wraps beneath the state and Manage opens the full-screen sheet. The named setup panels share a state chip, a ruled definition list for facts, a fix with a copyable command, Check again and inline Disconnect confirmation. A machine that is offline shows its last saved facts with an offline label. Declared actions keep their labels, fields and unavailable reasons in the drawer and the row overflow. Previous connections starts collapsed. Each row identifies the connection and machine. Remove connection opens a full-width inline confirmation below the row details, with the actions below the prompt on phones.

### OpenCode setup
The Settings OpenCode connection card; chip `not connected` (idle), `connected` (ok), `unavailable` (bad), `rejected` or `expired` (need, sign-in lost). Not connected with a service on this machine: "OpenCode is running on this machine", mono service and version rows, primary Connect with mono meta "signs in with the service's own password", then an "or another machine" rule. Without one: muted "No OpenCode service on this machine.", "On the machine running OpenCode, run this and paste one link. Links work once and expire in 5 minutes." and an `opencode pair` command box. The one field, "Pairing link or address" (mono), reads back under itself as you type: `pairing link · service host:port · single use, 5 min` with Pair, `address · host:port` with Connect, anything else disables it. An address that needs a password raises a need Alert ("This OpenCode needs a password. Paste a pairing link from `opencode pair`, or enter the service password.") and a Password field with Connect. Connected: ruled mono rows Service (`url · v2.3.0` when the local version is known), Sign-in (`local service`, `paired · renews automatically, expires Nov 26`, `password`, `none`), Checked; muted small notes, one per operation the connection offers (respond: "Permissions and questions of OpenCode sessions can be answered on the task, or in OpenCode. Forms with conditional or external fields are answered in OpenCode."; send: "A follow-up sent while OpenCode works waits for its run to end."), as on the Claude Code card; small Check again and Advanced, Disconnect (danger, right) behind an inline confirm "Disconnect OpenCode? Tasks and history stay. You can connect again any time." Sign-in lost puts a need Alert first: paired "OpenCode at host:port no longer accepts Stage's session. Run `opencode pair` there and paste the new link. Tasks and history are kept." with the link field and Pair again; local "OpenCode changed its password." with Connect local service; password points to Advanced. Advanced is the "OpenCode advanced" drawer (Service URL, Address for other devices, Web client URL, Basic credentials; Cancel / Test and save) and saves only the fields that changed. Connect, Reconnect, a pairing link, an address or the Advanced save that reaches a different (or earlier) service shows the same `ConfirmInline` as the Advanced drawer ("Connect as new service" / "Connect again", Cancel); a pairing link keeps its redeemed token for that confirm.

### Codex setup
The Settings Codex connection card ("Codex connection", the `codex` entry of the `packages/app/src/harnesses/` slot registry, the same Manage drawer); chip `connected` (ok), `not connected` (idle), `signed out` (need), `unreachable` or `not installed` (bad). Ruled facts: the Codex CLI and app server versions, the account (`dee@example.com · plus` for ChatGPT, else the provider: API key, Amazon Bedrock, Google Cloud; "Not signed in" when signed out), `CODEX_HOME /home/u/.codex`, `daemon running · checked 2m ago`. Not connected: the reason as an inline error, then the fix and its command (`codex login`, `codex app-server daemon start`) in a `CommandBox`, then primary Connect (disabled with a muted "Unavailable: reason" where the machine cannot run Codex). Connected: small Check again; Disconnect (danger, right) behind an inline confirm "Disconnect Codex? Tasks and history stay. You can connect again any time. It stays listed as disconnected until you remove it." Under a hairline the "Codex CLI" field (mono, placeholder `codex`, hint "Path to the codex binary. Empty uses codex on PATH.") with Save, sent only on a change, then the "Complete tasks when archived in Codex" toggle. No new components, tokens or colors.

### Project workspaces
Project list rows, headers and picker results share a compact placement line with
the primary workspace, machine, harness, state and extra workspace count. The
ruled Workspaces panel groups each checkout under its machine. Rows label the
harness, copyable path or external workspace, and text state chip. The fields
stack and paths wrap below 768px. Offline locations stay visible. Pickers,
workspace lists and counts omit bindings on retired connections.

Bind workspace opens a Drawer, full screen on phone, with suggestions first,
a repository filter, and links to Stage owners and candidate projects.
Discovery review pairs its attention count with reason-labelled inventory.
Resolve and merge open with a preview step. Focus moves to the plan after
it loads, and a changed confirmation returns to an updated plan. A merge
names the project to keep and the records moving to it. Split identifies
what stays. Project actions contains the archive confirmation.

Task and schedule forms identify the workspace on the chosen connection.
Primary follows the connection's default. A different selection pins the
workspace and keeps its full path in the field's details. A missing pin shows
its repair in the field. Task detail identifies the assignment recorded at launch.
Picking a connection or workspace never moves the form: square secondary
icon buttons sit right of the Workspace combobox (Refresh workspaces, always
shown, disabled without an active connection, the busy spinner while it
runs; Clear workspace in backlog only, disabled with none picked), and one
line under it is always kept, reading the status, else "Checking launch
support…" while the chosen workspace's launch check runs, else the path
(mono), else "Choose a workspace before starting this backlog task." in
backlog, else "No connection selected" / "No workspace selected" (Geist, so it
never reads as a path). A final launch refusal still takes its own hint row.
Only a long status may wrap. "Connect a harness in Settings." sits under
Connection when there is none.

### Replaced connections
A connection is one harness instance. When another T3 or OpenCode replaces it,
its tasks keep their last state and read as history:
- **`replaced` chip:** a plain idle-tone chip after the harness name (task rows,
  task detail, tree nodes beside the project), `title` "Ran on T3 'box a', which
  was replaced. Read-only."
- **Task detail:** a plain Alert, "Ran on T3 'box a', which was replaced. Its
  history and result stay here; Stage no longer follows it." Start, Retry and Stop
  stay visible but disabled, with "Its T3 connection was replaced. Duplicate it to
  run again." on hover; Complete, Cancel, Archive and Duplicate still work. No
  Open in T3 / OpenCode link anywhere for such a task.
- **Task form:** pickers list active connections only; a task on a replaced one
  shows a disabled "T3 (replaced)" option so its value still reads.
- **Pairing another T3** (Settings or Dashboard pairing controls): a plain Alert
  where the user paired, "Paired. T3 'box a' was replaced; its 4 tasks stay
  read-only. Bind projects to continue."
- **OpenCode address to another service:** Test and save answers with a `ConfirmInline`
  under the form, "http://new:4096 is a different OpenCode service.
  Connect it as a new service? http://oc:4096 becomes read-only: its 3 tasks keep
  their last state and its project bindings need rebinding.", with primary
  "Connect as new service" and ghost Cancel (focused). A replaced service coming
  back reads "… is the OpenCode service Stage used before. Connect it again? Its
  tasks and project bindings come back, and … becomes read-only." with "Connect
  again". Editing an address drops the confirm.
No new components, tokens or colors.

### Notifications drawer
Right drawer (full-screen sheet on phone), one row per task or external session across the last 30 days. Current needs-input groups lead in their own ruled panel; other groups sit under Recent · Today / Recent · Yesterday / Earlier (Label on Bench Gray), newest notification first. Compact for scanning: panel headers are 28px and panels sit 8px apart. Each row is two lines (8px vertical padding): the title clamped to one line (full title on hover) with the current-state chip right; below, a mono meta line (project · relative time, exact on hover; then `· was <event>` in the event's tone when the state changed or is unavailable; then an `n updates` disclosure only when the group has more than one event) with the small actions right. On phone `was <event>` starts its own meta line. Current needs input alone earns the yellow edge and primary Open in T3 action. Chips reuse the shared task-state colours: yellow needs input, sky running/launching, teal done, red failed/interrupted, gray inactive. Unknown current state reads as an `unavailable` chip. A `ready` notification (a manual tree task whose needs are completed; idle tone) whose task is still in backlog reads as a `ready` chip (not "backlog", and no `was` label) and gets a primary small Start (starts the task; errors show as a red mono line).

Expanding updates reveals a newest-first vertical notification history: hairline connector, square semantic markers, coloured mono event labels, and relative times with exact timestamps on hover. Older updates page within the row; loading uses skeletons and errors offer Retry history. History includes the recorded events (needs input, ready, done, failed). An external session with no project title reads “external session”. The empty inbox reads “No notifications in the last 30 days. Needs input, failed, ready and done events land here.” Expansion survives live state changes. Unread rows show a 6px Ink square before a 600-weight title; opening the drawer reads everything through the global newest event ID, but the markers stay until it closes. Arrivals while open become read on close. Load more pages task and session groups after server aggregation. The footer notes when push is off on this device (desktop: "Notifications are off on this device").

### Toast
In-app notification while the app is visible: bottom-right stack, 360px wide on laptop; full width above the bottom tabs on phone. Flat (Strong Hairline border, Paper surface, no shadow), fades in over 150ms (no movement; none with reduced motion). Two lines: the title (one line, full on hover) with a ghost close, then the event chip, mono project and time, and View and Open in app right (wrapping below on phone) (a ready toast whose task is still in backlog leads with Start). Needs-input toasts take the yellow edge and stay until acted on; the rest (ready, done, failed) dismiss after 8s, paused on hover or focus. At most three; older ones collapse into a mono "+n more · open notifications" strip that opens the drawer. Events that arrived while the app was hidden never toast; the bell count covers them. While the drawer is open, toasts are off.

### Snippet row
On the Snippets page, one panel per scope (Global first, then each project with snippets), with an "Expand all / Collapse all" ghost action in the header. Each row: a 20px `DisclosureChevron` button (`aria-expanded`, rotated -90° when collapsed), the name as Row Title, and a 2-line mono Slate preview. Expanded, the full body prints at 13px mono with whitespace kept, followed by a small Copy button. Edit and Delete sit right on laptop and wrap below on phone.

### Snippet picker
Typing `/` at a word start (or `template:`) in the task prompt, notes opens a list of matching snippets. Matches are global plus the task's project. On laptop it sits under the caret, 360px wide, and flips above near the viewport bottom. On phone it drops 4px under the line holding the `/`, as wide as the field, and never flips: its top stays fixed while the query is typed and moves only with the `/` when the form scrolls, so it reads as attached to the trigger. It aims for 296px (header plus about 4.5 rows, the cut row hinting the list scrolls), capped by the room above the keyboard (tracking `visualViewport`); fewer matches shrink it from the bottom. When the `/` sits too close to the keyboard, the form scrolls once on open, only by the missing room and never past the top of the scroll container; it never re-scrolls while filtering. Rows are 44px+ and the 40px header carries a ghost Close button (no Esc on a phone). Flat (Strong Hairline border, Paper, no shadow), a 28px Bench Gray header ("SNIPPETS" Label plus count), rows with the name at 13px, a plain scope chip right and a 1-line mono preview. The active row gets the active row box, and the footer shows keycaps (↑↓ move, ↵ insert, Esc close) on laptop. Enter or Tab inserts the snippet in place of the token and keeps native undo; Esc closes it until the token ends.

### Command palette

Ctrl K lists Stage pages, tasks and projects. Enter opens a task in Stage, Ctrl Enter opens its session in the harness, Alt C copies the task reference. Retain dense keyboard-first rows and existing focus treatment.

### Copy link

Task headers carry Copy link (self-describing markdown) and URL. Laptop task rows expose Copy link on hover/focus; the palette uses Alt C. Preserve copy feedback and the polite live region. Generated references point to tasks.

### Ref chip

Task references render as compact state/title chips. Historical session references are resolved explicitly: open the owning task when available, otherwise native T3 (remote desktop uses its web URL). Unknown references preserve their original text. Prompts, final replies, journals and handoffs keep this behavior.

### Desktop host
The desktop app renders the same UI; its differences are restrained and live in `packages/app/src/components/Host.tsx` (only when the platform has `host`).
- **Connection screen** (before the server first answers, or when it speaks another API): Paper ground, a single 560px column from about 14% of the window height. The static brand glyph and wordmark (top step at 44%, no needs-input state yet), then the PageHeader (H1 names the state, `//` meta line the detail), a flush `STAGE SERVER` Panel (identity as meta; a remote's name with its URL as meta) with three ruled mono step rows (`packages/app/src/components/Steps.tsx`, shared with T3 setup), `service`, `server`, `api`: a 6px square marker in its tone (idle gray waiting, ok green, bad red) or the 8px square run-tone spinner on the active step, the step name in Ink Text and a mono note right (`waiting`, `starting`, `checking`, a version, `not running` in red). Below: the fix as a bad Alert, problems as a ruled list (Geist message over a mono Slate fix), a remedy command in a Bench Gray mono box with a small Copy, one primary action (Start Stage server, Restart Stage server or Retry), and a ghost "Show details" that reveals the `SERVER LOG` Panel (mono pre, scrolls at 288px) and a small "Copy diagnostics". The starting screen waits 400ms before it appears so a fast start shows only the Paper ground. No entrance motion; the spinner stops with reduced motion.
- **Host banner** (after the app first ran on the active server): Alerts stacked at the top of the page column above the PageHeader, 14px above it. Reconnecting is a plain Alert with "Retry now"; stopped, failed or (remote) unreachable is a bad Alert with its primary action and the remedy command as a mono line; a terminal-started server and a server version that differs from the app are plain notes with a ghost Dismiss (skew adds "Restart Stage server"). The app stays mounted underneath so drafts survive.
- **Settings Info lines**: the desktop app's rows and controls in General > Info (see Settings). The Notifications panel's `THIS DEVICE` controls native notifications (see Settings notifications).
- **Settings T3 connection Disconnect**: while T3 is paired (a stored token or a saved admin token), a hairline footer under the Details rows holds a small Disconnect (danger, right). Disconnect confirms with `ConfirmInline`: "Disconnect from tmmacbox? Stage forgets its token, URL, browser URL and admin token and stops syncing. Tasks and bindings stay; pair the same T3 again to resume. The token stays valid in T3 until Dec 17." with Disconnect (danger) and Cancel (ghost, focused) under the copy so it wraps on a phone. Disconnect is never locked. The state change in place is the feedback: the chip turns `unpaired`, the footer leaves, Pair offers pairing, and the Dashboard and top bar treat T3 as not configured.
- **OS notifications and badge**: the resident app shows the checked events as system notifications while Stage is not in front (toasts when it is) and keeps the needs-input count on the Dock or launcher icon; the page draws neither. With more than one server saved, a notification's title starts with the server's name and a click switches to that server first; the badge sums every server.
- **Servers** (`packages/app/src/components/Servers.tsx`, logic in `packages/app/src/lib/host.ts`): server switching is kept in code but not offered in the UI. The app assumes one server, this machine's: no Settings Servers panel, no top-bar server menu, no "Switch server".
- **Remote server**: the connection screen shows steps `server` and `api`, primary Retry (also for `incompatible`: the app cannot restart a remote); `unreachable` reads `unreachable` in red on the server step, and a refused hostname adds the `STAGE_ALLOWED_ORIGINS=<url>` remedy box. Local server controls stay hidden for remote servers. Open in T3 uses remote T3 web URLs; this machine and browsers prefer native T3 links.
- Native menus, window chrome, Cmd or Ctrl accelerators and window persistence belong to the OS; the page draws none of them.

## Do's and Don'ts

### Do:
- **Do** keep every corner at 0px, including chips, markers, spinners and focus outlines.
- **Do** separate rows and regions with 1px Hairline and outline controls with Strong Hairline.
- **Do** color only what a state earns: chips, square markers, stat counts, alerts, the needs-input edge.
- **Do** draw tones with the two-token pattern: border at 45-55% tone, fill at 8-14%, text in the tone's text color.
- **Do** place the `//` meta line directly below the H1 in 12px mono Slate with a bold Ink Text `//`.
- **Do** head every panel with the 32px Bench Gray bar and an uppercase 11px mono label at 0.12em.
- **Do** set titles, actions and prose in Geist; set metadata, labels, chips, counts and keycaps in JetBrains Mono with tabular numerals.
- **Do** take icons from Lucide through `icons.tsx`: square caps, miter joins, constant 1.33px stroke, currentColor.
- **Do** open secondary flows in a 580px (or 680px) right drawer that becomes a full-screen sheet below 768px.
- **Do** keep motion to 150ms color, opacity and border transitions on state change (plus the skeleton pulse, still with reduced motion).
- **Do** give needs-input rows (and still-waiting needs-input toasts, inbox rows and the Needs input panel) the 2px Waiting Yellow left edge and a primary Open in {harness}; it is the only colored row edge.
- **Do** build every harness-dependent control, panel and line from what the harness declares (descriptor, operations, launch options, the links and data it reports), in its own words and order; a harness that declares nothing shows nothing ([ADR 0007](docs/adr/0007-harness-boundary.md)).

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
- **Don't** branch a view on a harness name, or offer a session control (worktree, branch, base) Stage would have to compute; those belong to the harness.

The inbox includes a Waiting in {harness} group ("Waiting outside Stage" across several harnesses) for current external sessions with no recorded events. Rows use the same two-line layout (title and needs-input badge, then project and a small primary Open in {harness}), without unread markers, event times or history controls. They disappear when resolved or replaced by a recorded notification group.
