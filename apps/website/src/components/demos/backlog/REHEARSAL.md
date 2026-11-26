# backlog demo: rehearsal

## Story
stage's New task drawer over a project page (`api-gateway`, Tasks panel filtered to backlog).
1. Prompt typed, then title.
2. Model select opened, `claude-opus-5-5` picked; Effort rolls `medium` to `high`. (No worktree beat: a worktree is the harness's own launch option, not a Stage field.)
3. Start `Now` to `Backlog`: the one primary footer button rolls `Save & start` to `Save to backlog` (real TaskForm behavior). Pressed; drawer slides out, scrim fades.
4. Payoff: `#58 Add rate limit headers` opens at the top of the list (Collapse carries rows down), idle `backlog` chip, meta `claude-opus-5-5 · high  just now`, count rolls 5 to 6, soft landing tint.
5. Reset with a cause: the new row's Start rolls its chip to `launching` (run tone) and the row leaves the backlog list; New task opens a fresh drawer (fields snapped while hidden).

Poster: drawer closed, new row landed on top and still tinted; the row's meta carries the drawer's model and effort.

## Gold copied
`form-error` (typed field, button label changing on itself), `subscriptions` (a row arriving at the top, carrying rows below, count rolling), `panel-annotation` (tool surface over product). Layout: whole window filling the stage (16px gutter, 640+ wide, passes the "fills" rule) because a right drawer must stay whole: any right or bottom bleed would cut the drawer or its Save button.

## What changed from the golds
- Look: stage Ink Workbench tokens only (`--st-*`), 0 radius, mono metadata, ink primary buttons with `--st-on-accent` text (kit default `#fff` is invisible in dark).
- Product parts rebuilt from stage source (TaskList.tsx, TaskForm.tsx, ModelFields.tsx, ui.tsx): state chip, task row grid (118px state column), panel header bar, selects with stage's chevron, segmented control (content-width cells, ink fill, 150ms crossfade as in the product; the kit Segmented forces equal widths).
- Kit parts used: Stage, Window, Field (title), Button (New task, Start, Save, close), Kbd, Popover (model list), Roll, Count, Collapse, Text.
- Deviation from brief: brief said "Save to backlog (secondary to Start)". Real stage has one primary button whose label follows the Start segmented control, so the demo picks Backlog there and the button rolls to "Save to backlog".
- Wide drawer is 464px in a 648px window (real: 680px on a wide screen); provider/model/effort grid is 96 / 1fr / 96 instead of equal thirds so the model id fits.
- Compact (358x600): chromeless stage phone layout at 1:1 in a hairline viewport; drawer is the full-screen sheet; phone sizes (40px controls, 32px small buttons, +1px type); Provider dropped (Model | Effort), Start fills the width. A 334px browser window failed the window-proportion rule, and a scaled `Phone` put labels under 10px and cropped the sheet footer.

## Checked
- `film.mjs --gate` wide (680) and `--width 390`: the only remaining lines are 6 `kit override: .ss-phone…` (see below). All of the demo's own lines are cleared (framing, tight control, loose list, uneven gutters, loose stack, popover gutter, covers).
- Reduced-motion poster (`--reduced --still`), dark mode (vocs `data-vocs-theme="dark"`): scrim switches to a dark mix, ink buttons invert.
- Seam: last beat (New task opens the drawer) returns to the opening frame; cursor returns to its rest point.
- `oxlint --deny-warnings` and `tsc --noEmit`: clean.

## Not cleared / not checked
- Gate fails only on `kit override: .ss-phone…` x6: film exempts kit sheets by a `/kit/` path, but the kit is vendored at `src/vendor/stageshow/`, so the kit's own `phone.css` is flagged on every stage on this page. Needs a fix outside this folder.
- vocs's `@layer vocs_base` reset (`* { padding: 0; margin: 0 }`) outranks `@layer stageshow` on this page, zeroing kit spacing site-wide. This demo restores the window bar, url field and button padding locally (same values as the kit); a global layer-order fix makes those rules redundant.
- Non-gating candidates left: `jump:` on kit Field chars while typing and on kit Roll items (y 2-3px at roll start, likely the same reset hitting Roll's padding/margin trick), `near-tangent` of the compact viewport hairline to the stage edge, `near aligned` brand vs drawer title.
- Film ran slow (sampling gaps, loop stretched to ~28s); real-time loop is about 16s. Live frame rate, touch, and an offscreen/resume cycle were not checked by hand.

## Logo update, 2026-11-15

Header now uses the shared canonical 16px monochrome StageMark. This scene has no pending input. Existing framing and choreography are unchanged. Reduced-motion stills checked at 390/light and 1440/dark (`.scratch/logo-backlog-390.png`, `.scratch/logo-backlog-1440.png`); no logo clipping or overflow. The loop was not re-filmed for this static glyph substitution.
