---
name: stage
description: Marketing and docs site for stage in its Ink Workbench world; ruled, inked, square, and only state carries color.
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
  display:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "clamp(40px, 6.2vw, 76px)"
    fontWeight: 700
    lineHeight: 1.02
    letterSpacing: "-0.035em"
  display-section:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "clamp(28px, 3.2vw, 38px)"
    fontWeight: 650
    lineHeight: 1.1
    letterSpacing: "-0.028em"
  lede:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "clamp(17px, 1.5vw, 19px)"
    fontWeight: 400
    lineHeight: 1.5
    letterSpacing: "-0.005em"
  body-lg:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "16.5px"
    fontWeight: 400
    lineHeight: 1.55
  button-lg:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "14px"
    fontWeight: 500
  wordmark:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "18px"
    letterSpacing: "-0.02em"
  headline-demo:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "22px"
    fontWeight: 700
    lineHeight: 1.25
    letterSpacing: "-0.025em"
  stat-phone:
    fontFamily: "Geist Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "17px"
    fontWeight: 700
    fontFeature: "tnum"
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
  code-panel:
    fontFamily: "JetBrains Mono Variable, ui-monospace, monospace"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.9
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
  button-landing-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.on-accent}"
    typography: "{typography.button-lg}"
    rounded: "{rounded.none}"
    padding: "0 16px"
    height: "40px"
  button-landing-secondary:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.fg}"
    typography: "{typography.button-lg}"
    rounded: "{rounded.none}"
    padding: "0 16px"
    height: "40px"
  button-landing-secondary-hover:
    backgroundColor: "{colors.panel}"
  landing-link:
    textColor: "{colors.fg}"
    typography: "{typography.button-lg}"
  landing-facts:
    textColor: "{colors.muted}"
    typography: "{typography.meta-line}"
  landing-panel:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.none}"
  landing-panel-header:
    backgroundColor: "{colors.panel}"
    textColor: "{colors.fg}"
    typography: "{typography.label}"
    padding: "0 12px"
    height: "32px"
  landing-panel-body:
    textColor: "{colors.fg}"
    typography: "{typography.code-panel}"
    padding: "16px"
  theme-toggle-option:
    textColor: "{colors.muted}"
    rounded: "{rounded.none}"
    width: "32px"
    height: "28px"
  theme-toggle-option-hover:
    backgroundColor: "{colors.panel}"
    textColor: "{colors.fg}"
  theme-toggle-option-active:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.on-accent}"
  site-footer:
    textColor: "{colors.muted}"
    typography: "{typography.body-sm}"
    padding: "20px 0 0"
    width: "1240px"
---

# Design System: stage

## Overview

**Creative North Star: "The Ink Workbench"**

stage is a workbench, not a dashboard of cards. Every view is a stack of ruled, square-cornered panels on a white (light) or deep slate (dark) ground, each headed by a tinted bar with an uppercase mono label, each holding hairline-separated rows. The chrome is ink and hairline only: black-on-white primary actions in light mode, near-white-on-near-black in dark. Color is withheld until a task's state earns it, so the one yellow on screen is always the thing waiting on the user.

Density is high and deliberate: 14.5px body, 32px controls (40px on phone), 8-10px row padding, 14px between panels. Geist carries titles, actions and prose; JetBrains Mono carries everything that is metadata (project names, models, times, counts in rows, labels, chips, keycaps). The direction refuses the rounded-card SaaS dashboard and the neon-terminal costume: it reads like an instrument panel drawn with a ruler, lit only where attention is owed.

Motion is quiet: 150ms color, opacity and border transitions on state change, a pulsing skeleton while loading, a square spinner on busy buttons, and no entrance choreography.

This repo is stage's website, the same world on two surfaces. The landing page (`/`, Persuade) is the workbench run in public: a display-scale Geist headline, a `//` meta line, ink and outline actions, then full-width hairline-ruled rows where copy sits left and a stageshow demo of stage's real UI plays right. The docs (`/docs/*`, Read) keep vocs' reading layout and re-ink it through the same tokens, so they read as the app's manual. The demos are faithful pictures of the product: inside them the app's own sizes, rows, panels and state tones apply unchanged (Components, Product UI). Token source of truth is `src/pages/_root.css` (`--st-*`); vocs variables and the stageshow kit (`--ss-*`, `src/components/stage-look.css`) are mapped onto it, never given their own values.

**Key Characteristics:**
- Zero radius on every element, including chips, markers, spinners and focus outlines.
- 1px hairlines (11% ink) separate rows and regions; 20% ink outlines controls.
- Ink accent that inverts per theme; no brand hue in chrome.
- Six state tones (need, run, done, ok, bad, idle) drawn as tinted borders, square markers and colored text.
- Uppercase JetBrains Mono panel headers on a light-gray bar.
- Drawers and full-screen sheets instead of modals.
- Site: display-scale Geist headings on the landing only; app role sizes everywhere else.
- Site: demos are inert pictures (role img, foot fade crop), never screenshots, following the site's light/dark theme.

## Colors

An ink-and-paper neutral system with a strictly semantic state palette; nothing else is colored.

In this repo every color is a `--st-*` custom property on `:root` in `src/pages/_root.css`; the dark values live under `:root[data-vocs-theme="dark"]`, the attribute vocs and the site theme toggle both write. The `-dark` frontmatter keys are those dark values. vocs is told the same thing (`accentColor: light-dark(#0b0d14, #eef0f5)`), and its palette maps onto the tones: blue to `run`, green to `ok`, red to `bad`, yellow to `need`, iris to `done`, so callouts stay inside the six. There is no `--st-scrim`: the one demo scrim (backlog drawer) mixes `fg` at 22% in light and `on-accent` at 55% in dark, close to the `scrim` tokens. Code blocks use the `github-light` / `github-dark-dimmed` highlighter themes on the Bench Gray panel.

### Primary
- **Ink** (`accent` / `accent-dark`): the primary action color. Black (#0b0d14) on white in light mode, near-white (#eef0f5) on deep slate in dark. Fills primary buttons (New task, Start, Open in T3 on a needs-input row), the active nav underline, the selected segmented option, the idle brand glyph, text selection, focus outlines and the history bar chart. Paired with `on-accent` for text on it.

### Neutral
- **Paper** (`bg`, `surface`): the ground and panel bodies. Both pure white in light; in dark the ground is #1a1d23 and panel bodies lift one step to #1f2229.
- **Bench Gray** (`panel` / `panel-dark`): panel header bars, drawer footers, keycaps, hover fills on secondary and ghost buttons, the palette footer.
- **Ink Text** (`fg` / `fg-dark`): all primary text and row titles.
- **Slate** (`muted` / `muted-dark`): metadata, labels, placeholders, empty-state copy, inactive tabs.
- **Hairline** (`line` / `line-dark`): row separators, panel borders, bar borders.
- **Strong Hairline** (`line-strong` / `line-strong-dark`): control outlines (inputs, secondary buttons, segmented groups, search trigger), drawer edge, run order connectors, dashed backlog steps.
- **Scrim** (`scrim` / `scrim-dark`): behind drawers, sheets and the palette.

### State
- **Waiting Yellow** (`need`, text `need-fg`): needs input, waiting sessions. The marker and border are the bright yellow; text uses the darker `need-fg` because the bright yellow fails as text on white.
- **Running Sky** (`run`): launching, running, queued and starting sessions.
- **Settle Teal** (`done`): done (run finished) and awaiting review.
- **Settled Green** (`ok`): completed tasks and trees, T3 connected.
- **Fault Red** (`bad`): failed, interrupted, T3 offline, errors, danger buttons.
- **Idle Gray** (`idle`): backlog, cancelled, idle, rolled back.

### Named Rules
**The State Earns Color Rule.** Hue appears only on a state: a chip, a square marker, a stat count, an alert, the needs-input row edge, or the demo brand glyph's top step while input waits. Website chrome, navigation, links and primary buttons stay ink. Static favicon and notification artwork keep the canonical yellow top step as brand, outside app chrome.

**The Two-Token Tone Rule.** Every tone sets a marker/border color and a text color. Borders mix the tone at 45-55% into transparent, fills at 8-14%; the need chip fills at 14% so yellow reads at a glance. Never invent a tone outside the six.

**The One Yellow Edge Rule.** User-approved: a task row in needs input carries a 2px Waiting Yellow left edge and makes its primary action "Open in T3". It is the only colored row edge in the system; every other row keeps a transparent 2px edge so titles align. The same edge marks a needs-input toast, an inbox row whose task or session is still waiting, and the Needs input panel while something still waits on the user.

## Typography

**Display Font:** Geist Variable (with ui-sans-serif, system-ui)
**Body Font:** Geist Variable
**Label/Mono Font:** JetBrains Mono Variable (with ui-monospace)

**Character:** A tight, slightly negative-tracked grotesk for anything a person reads as a sentence or presses, set against a mono that marks everything machine-shaped. The split is functional, not decorative: if it came from T3 or a clock, it is mono.

### Hierarchy
The site has no Tailwind and no size custom properties: component CSS writes literal px against `var(--st-font)` and `var(--st-mono)` (Geist Variable and JetBrains Mono Variable, loaded through `@fontsource-variable`). Every literal must be a step named here. The product roles below are the app's laptop sizes; below 768px each steps up about 1px (body 15.5px, meta 13px, label 11.5px, title 17px, stat 17px, headline 24px). Demos draw the laptop roles in their full layout and the phone step in their compact layout.

**Site display scale (landing only).** These sit above the product ramp on purpose; they exist so a marketing page can hold a headline at a distance, and they never appear inside a demo or in the docs.
- **Display** (700, `clamp(40px, 6.2vw, 76px)`, 1.02, -0.035em, max 15ch, balanced): the landing H1 only.
- **Display Section** (650, `clamp(28px, 3.2vw, 38px)`, 1.1, -0.028em, max 18ch, balanced): landing section H2s.
- **Lede** (400, `clamp(17px, 1.5vw, 19px)`, 1.5, -0.005em, Slate, max 58ch, pretty): the one sentence under the H1.
- **Body Large** (400, 16.5px, 16px on phone, 1.55, Slate, max 46ch): feature and closing copy.
- **Button Large** (500, 14px): landing buttons and the docs link under each feature.
- **Meta Line** (JetBrains Mono 400, 12.5px, 1.5): the landing `//` line and the facts list.
- **Code Panel** (JetBrains Mono 400, 13px, 1.9): the closing quickstart panel.
- **Wordmark** (18px, -0.02em): "stage" in the vocs top nav after the 16px stepped glyph; the footer wordmark is 13px 700 at the same tracking, also with a 16px glyph.
- **Headline Demo** (700, 22px) and **Stat Phone** (700, 17px, tabular): demo-scale steps, the backlog demo's compact page H1 and the hero demo's compact stat counts.

Product roles (app and demos):

- **Headline** (700, 26px on laptop, 24px on phone, 1.25, -0.025em): the page H1 only. Balanced wrapping, breaks long task titles.
- **Title** (600, 16px): drawer and sheet titles.
- **Row Title** (500, 14.5px): task and session titles in rows; underline on hover.
- **Body** (400, 14.5px, 1.45, -0.005em): base text. Inputs, selects and textareas use `text-input*` (14px, mono 13px on laptop) and are 16px on phone so iOS Safari never zooms on focus.
- **Body Small** (400, 13px): alerts, details, empty states, button labels (500).
- **Stat** (700, 16px, tabular): stat strip counts.
- **Label** (JetBrains Mono 400, 11px, 0.12em, uppercase): panel headers (in Ink Text), field labels and section labels (in Slate).
- **Meta** (JetBrains Mono 400, 12px, 1.5 line): row metadata, chips, hints, timestamps. Small counts and panel meta sit at 11.5px (`text-meta-sm`), detail values at 12.5px mono (`text-detail`), read-only prompts at 13px mono with relaxed leading.

### Named Rules
**The Mono Is Metadata Rule.** JetBrains Mono for project names, models, times, ids, counts, chips, labels, keycaps; Geist for titles, actions and prose. Counts, times and step numbers use tabular numerals.

**The Meta Below Rule.** A page's context line sits directly below the H1 in 12px mono Slate, led by a bold Ink Text `//`. Nothing sits above a heading: no eyebrow, no kicker. The landing keeps the rule at its scale: the `//` line follows the lede in 12.5px mono.

**The Two Scales Rule.** The display scale (Display, Display Section, Lede, Body Large) is for landing copy; the product ramp is for everything a visitor would see in stage itself, including every demo and the docs chrome. Decision recorded 2026-11-15: the six detector `design-system-font-size` advisories (landing H1, H2 and lede clamps, the 18px wordmark, the 22px demo headline, the 17px demo stat) are this intentional scale, now named above. A new off-ramp size is a new step to name here, not a literal to slip in.

## Layout

### Site
The vocs top nav carries the whole site: the mark (16px monochrome stepped glyph, 8px gap, the 18px wordmark), search, Docs / API / GitHub, vocs' theme control and the GitHub social. There is no app top bar or bottom tab bar on the site.

The landing escapes vocs' content padding so its hairlines run edge to edge, then holds content to a 1240px grid with `padding-inline: max(gutter, (100% - 1240px) / 2)`; the gutter is 24px, 16px below 768px. Order: hero (top padding `clamp(48px, 8vw, 104px)`, bottom `clamp(48px, 6vw, 80px)`; H1, lede, `//` line, actions 28px below with an 8px gap; then the full-width hero demo `clamp(40px, 5vw, 64px)` below), five feature rows, one closing row. Feature and closing rows are a two-column grid, copy 5fr and visual 7fr, gap `clamp(32px, 5vw, 72px)`, vertically centered, padding `clamp(64px, 8vw, 112px)` (closing `clamp(72px, 9vw, 128px)`); every section but the last ends on a 1px Hairline rule. Copy rhythm inside a row: H2, body 16px below, facts 24px below on a hairline with 16px above, link 24px below. Below 768px rows stack to one column with a 32px gap and 56px padding, and the hero actions stretch to fill the width.

Docs keep vocs' full layout (sidebar, content at its own measure, outline right), restyled only through variables. The footer (vocs Footer slot) is one row inside 1240px: a hairline above, 20px top padding, 12px row / 24px column gap, 32px below; mark, links (16px apart), mono meta pushed right, theme toggle. On phone the meta drops to its own last line.

### Product (as the demos draw it)
A single centered column, max 1240px, with 24px side gutters on laptop and 12px on phone (24px top padding on laptop, 16px on phone). The 48px sticky top bar holds the mark, text tabs (active tab gets a 2px ink underline; Dashboard carries the needs-input count chip), the Ctrl K search trigger, the T3 status control, the theme toggle and the primary New task button. Below 768px the top bar compacts to 52px with 40px controls and a 56px five-cell bottom tab bar (Stage, Projects, Sessions, Snippets, More; More holds History and Settings) takes over navigation, with a square yellow marker on Stage when anything needs input; page content reserves 96px of bottom padding plus the safe-area inset for it.

Pages stack: PageHeader (H1, `//` meta line, actions right-aligned, 16px below), then panels separated by 14px. The Dashboard puts a one-row stat strip (16px below the header) above the state groups in fixed order: Needs input, Running, Completed, Failed, Backlog. Consecutive empty groups collapse into one ruled list with the label left and the empty sentence right. Detail pages split at 1024px into a fluid main column and a 340px side column (300px on History).

Rows are grids: 118px state column, fluid title (task rows lead it with the task number), optional run order chip ("after #12" or "then #14"), right-aligned actions on laptop; on phone the title leads, the state chip floats right and actions wrap below. Spacing steps are 4, 8, 10, 12, 14, 16, 24px.

### Named Rules
**The Copy Left, Proof Right Rule.** A landing feature row is copy in the 5fr column and its demo in the 7fr column, hairline-separated from the next row. No icon-card grids, no bento, no section numbers.

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

### Site and demo composition
The site pages are flat: sections are separated by full-width Hairline rules, the closing panel is a Hairline box with a Bench Gray bar, and nothing on the page chrome casts a shadow. Demo windows are a 1px frame, never a drop shadow: the stageshow window, card, hero and ground shadows all map to a single hairline ring (`0 0 0 1px` Hairline for cards and grounds, Strong Hairline for windows and heroes), drawn inside the frame where a mask would cut it. Only an object composed to float over another (a satellite panel over a window, a carried row lifting off its list, a popover) casts a shadow, always over a Strong Hairline ring:
- **Float** (`0 0 0 1px var(--st-line-strong), 0 10px 28px -10px rgba(11, 13, 20, 0.24)`): popovers inside a demo.
- **Satellite** (`0 0 0 1px var(--st-line-strong), 0 16px 36px -14px rgba(11, 13, 20, 0.3)`): a panel set over a demo window (run order, plan session).
- **Lift** (`0 0 0 1px var(--st-line-strong), 0 12px 28px -8px rgba(11, 13, 20, 0.28)`): a row carried across the hero demo.
In dark the same three keep the ring and darken the drop to `rgba(0, 0, 0, 0.7)`, `0.75` and `0.7`.

### Named Rules
**The Flat Bench Rule.** Surfaces never cast shadows. Only the floating palette may; everything else separates by hairline and bar tint. Inside a demo this holds for the product UI it pictures.

**The One Elevation Per Object Rule.** On the site, a window or ground is a hairline ring and nothing more; a shadow belongs only to an object floating over another object in the composition (satellite, popover, carried row), and that object keeps its Strong Hairline ring under the drop.

## Shapes

Every corner is square (0px): buttons, inputs, chips, panels, drawers, keycaps, skeleton bars, the busy spinner, and state markers. Borders are always 1px; the only 2px strokes are the active-tab underline, the stat selection bar, the focus outline and the needs-input row edge. State markers are 6px squares (7px in alerts, 8px on the phone tab); the brand mark is a 16px stepped-bars glyph (see Brand mark). On the site, landing facts carry a 6px Strong Hairline square marker (no tone: a fact is not a state), and every vocs element is forced square, its literal pills, surfaces and step markers included. Run order steps are bordered cells joined by 1px connectors; unstarted steps use a dashed border, the current step adds a 1px Ink Text outline offset 1px.

Icons are authored 16px SVGs on a 16-unit grid: 1.5 stroke, square caps, miter joins, currentColor, so they share the sharp geometry of the grid.

Demo window chrome takes the same geometry: the stageshow window frame is square, and its traffic lights are three square marks in 20% ink with no hue (yellow belongs to needs input), with the address set in mono.

### Named Rules
**The Zero Radius Rule.** No element rounds a corner. A circle is never a state marker; the marker is a square. On the site this covers vocs and the stageshow kit: every `--vocs-radius-*` and `--ss-radius-*` is 0px.

**The Ink Traffic Lights Rule.** Window controls in a demo are square, colorless ink marks. Red, yellow and green belong to states, never to chrome.

## Components

Site components come first. From Buttons to Command palette the entries are stage's product components, kept as the reference the demos draw; a demo reproduces them at the app's sizes and never restyles them for marketing.

### Landing buttons
The product button at marketing size, still flat and square.
- **Shape:** square, 1px border, 40px tall, 16px side padding, 14px 500 label, 8px gap to a trailing authored 16px arrow (or external-link arrow when the target is off-site, which opens in a new tab).
- **Primary:** Ink fill, `on-accent` text; one per action group (Get started).
- **Secondary:** Paper fill, Strong Hairline border, Ink Text (GitHub, Read the docs).
- **Hover / Focus:** primary mixes Ink 86% into the ground, secondary fills Bench Gray, the arrow nudges 2px right; 150ms ease-out on background, color and border; global 2px ink focus outline offset 2px. On phone both buttons stretch to share the row.

### Landing link
The docs link closing each feature: 14px 500 Ink Text with a Strong Hairline underline offset 4px that turns Ink on hover (150ms), and a trailing arrow. Elsewhere links underline at a 3px offset; docs links are Ink and go Slate on hover.

### Facts list
Two or three mono facts under a feature's body: 12.5px JetBrains Mono Slate at 1.5, 8px apart, on a Hairline top rule with 16px above. Each fact leads with a 6px Strong Hairline square, 10px gap, nudged to the cap line. No tone, no icons.

### Stage panel (landing)
A product panel used as page furniture (the closing Docker compose quickstart): Hairline border, Paper body, 32px Bench Gray bar with a Hairline under it and the uppercase 11px mono label at 0.12em in Ink Text. The body is a 13px mono pre at 1.9 leading, 16px padding, scrolling sideways; commands are prefixed `$ ` and the closing note `# `, both prefixes and the note in Slate.

### Demo (signature)
Every product visual is a stageshow demo of stage's real UI with illustrative data, never a screenshot. The wrapper is `role="img"` with an `aria-label` describing what plays, and the scene inside is `inert`, so a demo is one picture to assistive tech and takes no focus. One crop grammar for all of them: a mask fades the last 64px to transparent, so windows may run off the foot instead of ending on a hard edge. Demos read the live `data-vocs-theme` and switch with the site, play only while in view, and hold their payoff frame under reduced motion. Inside, the `look-stage` mapping holds: `--ss-*` reads `--st-*`, chrome stays ink, state tones keep the two-token pattern, controls are 32px and rows 26px.

### Theme toggle
A three-option radiogroup (System, Light, Dark) in the footer: one Strong Hairline box, Hairline dividers, 32x28px square cells holding authored 16px icons. Slate at rest; hover goes Ink Text on Bench Gray; the checked cell fills Ink with `on-accent` icon. 150ms color transitions. It shares vocs' storage key and DOM contract (`data-vocs-theme` and `color-scheme` on `<html>`), so it and vocs' own control stay in sync, and System follows the OS live.

### Site footer
One hairline-topped row: the mark (16px monochrome stepped glyph, "stage" 700 in Ink Text), Docs / API / GitHub links in 13px Slate going Ink on hover, the 12px mono meta "MIT licensed · self-hosted" pushed right, then the theme toggle.

### Brand mark
User-approved treatment A, 2026-11-15. Three square stepped bars, widest at the bottom. Source: stage commit `0909f394272566b0aab0a0870641bb6665443351`, `web/src/components/Layout.tsx` and `web/public`.
- **Website header/footer:** 16px monochrome glyph, 8px gap to lowercase `stage`. Bottom bar solid, middle 72%, top 44%; follows the theme's ink. Vocs retains its text/link semantics and draws `public/mark.svg` through a CSS mask. Footer and demos use the decorative `StageMark` component with identical geometry: x 1/3/5, y 11/7/3, widths 14/10/6, heights 3 on a 16-unit grid.
- **Demo chrome:** hero (laptop and phone), backlog, schedules and push use the same component. Only a scene with pending input colors its top step `--st-need`: the hero follows its needs-input count; push follows its unanswered panel. A 150ms fill/opacity transition is disabled under reduced motion. Website branding never responds to a demo or calls the Stage API.
- **App artwork:** `public/logo.svg` is copied byte-for-byte from Stage's 512px source: square ink tile, white bottom bar, middle 72%, static yellow top step. Both notification placements use this asset through `StageLogo`; no duplicate SVG artwork.
- **Favicon:** `public/favicon.svg` is copied from Stage's `favicon-need.svg`. Static yellow top step, fixed dark tile in both themes, bars optimized for 16px. Vocs `iconUrl` points to it.
- **Social image provenance:** `public/og.png` (1200x630 PNG) is generated from the rendered site header glyph, hero copy and actual stageshow needs-input frame. Start `mise run dev`, then run `node apps/website/scripts/generate-og.mjs` from the repo root (requires `agent-browser`). The script can take another local serving URL. It opens and closes its own browser session and keeps the prior social image composition.

### Docs (vocs)
vocs' own components, re-inked only through `src/pages/_root.css`: ground, surfaces, code and blockquote backgrounds map to Paper and Bench Gray (vocs' reading `surface` stays on the ground), text to Ink Text and Slate, borders to Hairline, the active tab border to Ink, radius to 0. Ask AI is off per page and hidden as a fallback. Text selection is Ink with `on-accent` text; the scrollbar thumb, caret and native controls are Ink-tinted. Code, tables and keycaps use tabular numerals. The sidebar's four sections (Concepts, Usage, Guides, Reference) are collapsible and open by default; vocs opens the section holding the current page. Code blocks scroll inside the panel while the copy button stays pinned to its right edge on a Bench Gray backdrop, with room past the last character; it reveals on hover or focus, and stays visible on touch screens.

### Buttons
Compact, flat, and ink-led.
- **Shape:** square (0px), 1px border, 32px tall with 12px side padding; small is 24px with 8px padding and 12px labels. On phone (below 768px) default is 40px with 14px padding and small is 32px, for thumb-sized targets.
- **Primary:** Ink fill and border, `on-accent` text, 500 weight. One per region: New task, Start/Restart, Open in T3 when the task needs input, drawer submit.
- **Hover / Focus:** primary drops to 85% opacity; secondary and ghost fill Bench Gray; 150ms transition; 2px ink focus outline offset 2px. Disabled at 45% opacity. Busy shows a square spinning border.
- **Secondary:** Paper fill, Strong Hairline border, Ink Text. The default.
- **Ghost:** transparent border, Slate text; for close buttons and panel-header actions.
- **Danger:** Paper fill, Fault Red text and 45% red border, 8% red hover fill; Delete only, pushed to the far right.

### Chips
- **Style:** inline mono label (`text-meta`, 1.5 line, 1px 7px padding) with a tone-tinted 1px border, a faint tone fill and tone text. The `dot` variant prefixes a 6px square marker.
- **State:** task and session states map onto the six tones; plain chips (Slate border, Ink Text) carry model, runtime, workspace, tags and the run order link.

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
Every project picker (task form, snippet scope, Dashboard/Sessions/History/continuation filters) is a type-to-filter combobox that looks like a select at rest: same height, border, chevron and focus ring. Focus or click opens the list with the current label selected, so typing replaces it. Matches are case-insensitive substrings of the title or workspace path, ranked title prefix, title substring, path only; ties and the empty query sort by recent activity. A pinned first option ("All projects", "Global") leads while the query is empty. The list drops under the input (left-aligned, at least 280px, right-aligned when it would overflow), at most 344px tall, and flips above near the bottom of the visible area, including over a phone keyboard. Flat (Strong Hairline, Paper, no shadow); rows show the title at 13px over a 1-line mono Slate path, matched text bold, archived projects with a plain chip; 44px+ rows on phone. The active row gets the palette's 7% ink fill and 2px inset ink bar; the laptop footer shows keycaps (↑↓ move, ↵ select, Esc close). Enter selects and never submits the form; Esc closes the list (not the drawer) and restores the label, as do Tab and blur. No match shows a muted "No matching projects" line.

### Segmented control
Joined mono buttons inside one Strong Hairline border, hairline dividers between options; the pressed option fills Ink. Default is 32px tall overall (40px on phone), matching buttons and inputs it sits beside; small (follow-up send mode) is 26px (34px on phone). Used for runtime mode, time window, follow-up mode, theme, project sort/archive filter.

### Navigation

- Laptop: Stage, Trees, Projects, Schedules, Snippets, Settings. Smaller widths fold the final three into More.
- Phone: four equal tabs, Stage, Trees, Projects, More. More contains Schedules, Snippets, Settings and theme. Keep existing sizes, borders and active indicators.

### Pull to reload
On phones and tablets, in browser and installed PWA, a single-finger downward pull from the document top reloads the current URL when released at 80 CSS pixels. Below that distance it cancels. Controls, focused editors, nested scrollers, drawers and the palette keep their gestures; horizontal intent, multitouch and navigation cancel the pull.

A downward pull moves page content with increasing resistance, bounded below 112px; the 80px trigger reveals 56px of space beneath the header. Header, bottom tabs and fixed task actions stay anchored. A single pointer-transparent, square-cornered Paper/Strong Hairline indicator sits below the active header. Geist copy changes from “Pull to reload” to “Release to reload”, then “Reloading…” with the square busy spinner. On release or cancellation, content settles back over 180ms; the reloading indicator remains visible until navigation completes. State changes announce politely; reduced motion skips the settling animation and keeps the spinner still. Full reload clears unsaved drafts and requires connectivity.

### Stat strip (signature)
One bordered row of state counts that doubles as the Dashboard filter (`?state=`). Each cell shows a square tone marker (only when the count is non-zero), a bold tabular count and a mono label; zero counts go Slate. The selected cell fills its tone at 10% with a 2px inset bottom bar and colors the count. Laptop cells sit left with a "click a count to filter" hint right; phone cells split the width evenly with short labels. The last group, Completed (last 7 days), renders collapsed behind a chevron header (`aria-expanded`). Completed tasks appear newest completion first.

### Task row (signature)
State chip (backlog tree members read `ready` or `blocked`, idle tone), row title with a mono meta line (project, model, relative time), one optional plain row chip, and right-aligned small actions. The row chip is, in order: `archived`, `tree done` or `tree archived` (plain; the tree icon for the tree ones, linking the tree) on a closed row (Project detail Closed and All views); the start time while timed; on the Dashboard, the tree (authored tree icon and the tree title, truncated at 22ch, linking `/trees/:id`) for needs-input and ready breakouts; "needs #3 #5" while blocked; the schedule; "by agent". Blocked rows get no Start (Start early lives on task detail and the tree view). The task number leads the title as an unlinked mono 12px Slate tabular `#51` (6px gap, never wrapped apart), so needs chips ("needs #55") read against the rows they name; it sits outside the title link so the hover underline stays on the title. A completed row's meta line shows the acceptance time and elapsed run time, for example `completed 3h ago · ran 35m` (exact times on hover; `ran` is omitted until the run span is known). Needs-input rows take the One Yellow Edge; Open in T3 is the primary action. Last errors print as a truncated red mono line under the title.

On the Dashboard (every state group, external needs-input sessions included) the project gets its own column: laptop rows put a fixed 150px project cell right after the 118px state chip (mono 12px, Ink Text, medium weight, linked, truncated with the full name in `title`) so project names align down the whole page, and the project leaves the meta line. Phone rows print the project as a mono 12px Ink Text line above the title. The column stays when the Dashboard is filtered to one project; Project detail rows keep the project in the meta line.

Closing work: the Dashboard never lists closed work (archived tasks, done or archived trees and their members). Project detail's Tasks panel header carries a small `Open | Closed | All` segmented control (Closed: done or archived) beside the state select. Task detail's action row ends with Archive (secondary; standalone tasks only, disabled with "Stop the task first" while active) or Unarchive when archived, then Delete (danger, far right) only while the task has never started; an archived task adds a plain `archived` header chip. The palette lists closed tasks and trees too, each with a plain chip (`archived`, `tree done`, `done`).

### Last word
A session's latest assistant reply, a read-only glance (T3 stays the place to talk).
- **Row preview:** needs-input and done rows only (task rows and external session rows). One line of Geist 13px Slate prose with an ellipsis, full preview in `title`, no marker or label. Laptop: under the meta line in the title column. Phone: its own full-width line between the title block and the actions, so it is not squeezed beside the state chip.
- **Panel:** "Last word" Panel right under Prompt in the main column on task detail (above Created tasks). Header meta is the reply's relative time (mono, exact time on hover); Copy in the header. The body renders sanitized markdown (`.prose-reply`): Geist 14.5px at 1.55 leading, 75ch measure, headings at body or title size (never competing with the page H1), square Slate list markers, inline code and code blocks in mono on the Bench Gray panel with hairline borders (code blocks scroll sideways, prose wraps anywhere), hairline tables and rules, and a 1px Strong Hairline quote rule. Raw HTML shows as literal text. A truncated excerpt ends with a hairline-ruled mono footer, "Excerpt. Full reply in T3", linking to the session. Hidden until a reply has been read.

### Transcript

Conversations open in T3. Task detail contains prompt, final reply and journal, without a conversation tab or composer.

### Alert
Tinted callout: tone border at 50%, tone fill at 9% over surface, a 7px square marker, 13px text, actions right. `need` for waiting-on-you, `bad` for errors.

### Start control and schedules
The task drawer's Start segmented control (Now / Backlog / At time / After task / Repeat; Backlog / At time / After task when editing a backlog task) decides what the single primary footer button does, and its label follows: "Save & start", "Save to backlog", "Save · starts Nov 12 02:00", "Save · runs after #12", "Save schedule". At time shows date and time inputs, a zone select (browser default) and a mono "starts in" line. Repeat shows a preset segmented control (Hourly, Daily, Weekdays (Mon-Fri on phone), Weekly, Cron) with its minute, time, weekday or raw cron field, a zone select and a Bench Gray mono box with the summary and the next three fires from the server. On phone the segmented controls fill the width in equal cells with short labels.

Schedule rows share the task row grid: a plain "repeat" chip (idle "paused" when paused) with the authored repeat icon, title, mono summary, zone and "next in", the last run's state chip, then Run now and Pause/Resume. A timed backlog task shows a plain chip with the authored clock icon ("at 02:00") in the run order chip slot and its Start reads "Start now"; a spawned task shows a plain repeat chip linking its schedule. Stage adds a "Scheduled · next 24h" group between Backlog and Completed (timed tasks leave Backlog for it); it has no stat strip cell. Schedule detail: Run now (primary), Pause, Edit, Delete (danger, inline confirm); Runs in the main column, When / Config / Prompt panels in the side column.

### Task trees

Tree members express dependencies. Render current task/tree state and native T3 navigation; keep the approved dependency and start policies.

### Handoff
A task's handoff lives in its Prompt panel, because it is part of the message sent; one section per need. Backlog with handoff on: one mono Slate line under the prompt, "+ on launch: handoff from #4, #5 (final reply, branch, session link)". Launched: one hairline-ruled section per need under the prompt (and images), each with a Label `HANDOFF FROM #4`, mono Slate `sent HH:MM` (exact time on hover), a small "Copy handoff" button right, a mono char-count line, and a collapsed native `<details>` ("Handoff text") opening the block in the same mono pre as the prompt (max 384px, scrolls). A skipped need prints one mono Slate line, "Handoff skipped: <reason>". No color: it is context, not state. The panel's Copy still copies the prompt only. The Hand off context checkbox row sits in the Tree panel (and the tree view side panel) while backlog, and in the task drawer when editing a task with needs.

### Runs panel

Task run span remains in metadata. Detailed harness runs are opened in T3.

### Session adoption

Track session accepts a pasted T3 URL or ID, previews metadata and requires confirmation. No global session browser. See the root DESIGN.md for current product controls.

### Session batch (Created tasks)

Task detail has a compact Batch panel and View batch drawer (`?batch=1`). The drawer groups tasks by tree, then independent tasks. Retain Start and Edit, selection order, and New tree from selection with Parallel or Chain. By-agent row links open that task's batch. Closed or moved tasks retain their identity and current grouping.

### Image dropzone

Backlog task prompts accept image drops and Add image. Preserve thumbnails, upload/error states, the 8-image limit and read-only image links on launched tasks. No conversation composer.

### Notifications bell
A secondary 32px button (40px on phone) in the top bar (between search and the T3 status control on laptop; before search on phone) with an authored bell icon. Unread count sits inside as a mono chip: plain, or need-toned when an unread needs-input item is still waiting. Zero unread shows the bell alone, square.

### Needs input panel

Keep a compact yellow-edged needs-input alert with Open in T3 and Stop. Native T3 handles questions and permission approvals. Notifications retain live waiting state and offer Open in T3, without a question form.

### Checkbox
16px square native checkbox: Strong Hairline border on Paper; checked fills Ink with the authored check in On-Accent; focus-visible adds a 1px Paper gap and 1px Ink ring; disabled at 55%. Used in a ruled list (Settings Notifications events) and as the standalone "Hand off context" row (Run order panel, task drawer): whole row is the label, 44px tall on phone (32px laptop), Bench Gray hover, checkbox, 6px square state marker, Geist label, mono Slate hint right.

### Settings notifications
One panel, two sections split by a hairline. `EVENTS · all devices`: the global event filter (Needs input, Failed, Done), each toggle saving at once (optimistic; a failed save reverts and shows a red Alert); a muted line explains that unchecked events are dropped, or reads "All events off. Nothing is recorded or pushed." `THIS DEVICE`: per-browser push enable, test and disable; the header chip shows this device's push state.

### T3 status
A square secondary control (32px, 40px on phone) in both top bars, linking to Settings. Only the icon carries hue: an authored green check when connected, a red X when offline or the status call fails, a 6px Idle Gray square while checking. Its `aria-label` names the state. On pointer devices, hover or keyboard focus reveals a 280px details card right-aligned 4px under it: flat Paper, Strong Hairline, no shadow, a Bench Gray "T3 CONNECTION" header with the state chip, mono rows for URL, last full sync and token expiry, the last error in red (2 lines max) when present, and a muted "Click to open settings" footer. 150ms opacity fade, none with reduced motion. Touch devices never show the card; a tap goes to Settings.

### Notifications drawer
Right drawer (full-screen sheet on phone), one row per task or external session across the last 30 days. Current needs-input groups lead in their own ruled panel; other groups sit under Recent · Today / Recent · Yesterday / Earlier (Label on Bench Gray), newest notification first. Titles lead with the current-state chip right, a mono project/latest-notification time line below, then an updates disclosure and Open in app. Current needs input alone earns the yellow edge and primary Open in T3 action. Chips reuse the shared task-state colours: yellow needs input, sky running/launching, teal done, red failed/interrupted, gray inactive. Unknown current state reads unavailable; a changed or unavailable state labels its historical event separately as Last notification. A `ready` notification (a manual tree task whose needs are completed; idle tone) whose task is still in backlog reads as a `ready` chip (not "backlog", and no "Last notification" line) and gets a primary small Start (starts the task; errors show as a red mono line).

Expanding updates reveals a newest-first vertical notification history: hairline connector, square semantic markers, coloured mono event labels, and relative times with exact timestamps on hover. Older updates page within the row; loading uses skeletons and errors offer Retry history. History includes the recorded events (needs input, ready, done, failed). Expansion survives live state changes. Unread rows show a 6px Ink square before a 600-weight title; opening the drawer reads everything through the global newest event ID, but the markers stay until it closes. Arrivals while open become read on close. Load more pages task and session groups after server aggregation. The footer notes when push is off on this device (desktop: "Notifications are off on this device").

### Toast
In-app notification while the app is visible: bottom-right stack, 360px wide on laptop; full width above the bottom tabs on phone. Flat (Strong Hairline border, Paper surface, no shadow), fades in over 150ms (no movement; none with reduced motion). Event chip and a ghost close on top, title (2 lines max), mono project and time, then View and Open in app (a ready toast whose task is still in backlog leads with Start). Needs-input toasts take the yellow edge and stay until acted on; the rest (ready, done, failed) dismiss after 8s, paused on hover or focus. At most three; older ones collapse into a mono "+n more · open notifications" strip that opens the drawer. Events that arrived while the app was hidden never toast; the bell count covers them. While the drawer is open, toasts are off.

### Snippet row
On the Snippets page, one panel per scope (Global first, then each project with snippets), with an "Expand all / Collapse all" ghost action in the header. Each row: a 20px chevron button (`aria-expanded`, rotated -90° when collapsed), the name as Row Title, and a 2-line mono Slate preview. Expanded, the full body prints at 13px mono with whitespace kept, followed by a small Copy button. Edit and Delete sit right on laptop and wrap below on phone.

### Snippet picker
Typing `/` at a word start (or `template:`) in the task prompt, follow-up or notes opens a list of matching snippets. Matches are global plus the task's project. On laptop it sits under the caret, 360px wide, and flips above near the viewport bottom. On phone it drops 4px under the line holding the `/`, as wide as the field, and never flips: its top stays fixed while the query is typed and moves only with the `/` when the form scrolls, so it reads as attached to the trigger. It aims for 296px (header plus about 4.5 rows, the cut row hinting the list scrolls), capped by the room above the keyboard (tracking `visualViewport`); fewer matches shrink it from the bottom. When the `/` sits too close to the keyboard, the form scrolls once on open, only by the missing room and never past the top of the scroll container; it never re-scrolls while filtering. Rows are 44px+ and the 40px header carries a ghost Close button (no Esc on a phone). Flat (Strong Hairline border, Paper, no shadow), a 28px Bench Gray header ("SNIPPETS" Label plus count), rows with the name at 13px, a plain scope chip right and a 1-line mono preview. The active row gets the palette's 7% ink fill and 2px inset ink bar, and the footer shows keycaps (↑↓ move, ↵ insert, Esc close) on laptop. Enter or Tab inserts the snippet in place of the token and keeps native undo; Esc closes it until the token ends.

### Command palette

Ctrl K lists Stage pages, tasks and projects. Enter opens a task in Stage, Ctrl Enter opens T3, Alt C copies the task reference. Retain dense keyboard-first rows and existing focus treatment.

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
- **Do** keep motion to 150ms color, opacity and border transitions on state change; on the site the one movement is the 2px arrow nudge in landing buttons.
- **Do** give needs-input rows (and still-waiting needs-input toasts, inbox rows and the Needs input panel) the 2px Waiting Yellow left edge and a primary Open in T3; it is the only colored row edge.
- **Do** read every site color from a `--st-*` token in `src/pages/_root.css`; map vocs and stageshow variables onto those tokens rather than giving them values.
- **Do** keep landing type on the named display scale (Display, Display Section, Lede, Body Large) and everything inside a demo on the product ramp.
- **Do** lay out landing features as copy 5fr / demo 7fr inside the 1240px grid (24px gutters, 16px on phone), each row ending on a full-width hairline.
- **Do** ship every product visual as an inert `role="img"` stageshow demo with an `aria-label`, cropped by the 64px foot fade and following the site theme.
- **Do** frame demo windows with a hairline ring and square ink traffic lights; cast a shadow only from an object floating over another.

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
- **Don't** use a screenshot for a product visual, or let a demo take focus.
- **Don't** put a gradient hero, an icon-card bento or section numbers on the landing.
- **Don't** add an off-ramp font size without naming it as a step in Typography.
- **Don't** color window traffic lights.
- **Don't** add section entrance animations; demo choreography stays inside the demos.
