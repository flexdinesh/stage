# Rehearsal: schedules

Gold: `table-edit` (one window inset top and sides, off the bottom, rows as the subject, effect a beat after its cause) and `subscriptions` (system effect: a row opens in a list, a count rolls; reset hidden under a cover). Golds were read from source, not filmed: the stageshow playground was not started (brief: no dev servers).
Kept: window pinned top, off the bottom; Collapse rows carrying the rows below; Roll for counts and changed text; payoff hold about 2.4s; reset with a seen cause.
Changed: stage's real UI throughout (TaskForm Start control and RepeatFields, Drawer, Dashboard groups, TaskRow/ScheduleRow grids, chips, stage icons), Ink Workbench tokens only.

Beats (about 15.5s loop):
1. New task drawer open (Start: Now). Pointer picks Start: Repeat. Title rolls New task to New schedule, button Save & start to Save schedule, Repeat fields open (Daily default).
2. Pointer picks Weekdays. Summary, cron (`0 9 * * *` to `0 9 * * 1-5`) and next fires roll (Nov 21, 22 to Nov 23, 24: "now" is Fri Nov 20 08:59).
3. Save schedule. Drawer slides off; the schedule opens in "Scheduled · next 24h" with `next in 3s`, ticking 2s, 1s.
4. Fire: #58 opens in Running (launching, then running) with the `schedule` chip; Running count, `2 active` and the schedule's `next in 3d` roll. Compact: the phone clock rolls 8:59 to 9:00.
5. Hold, then the pointer opens New task (+); the drawer covers the dashboard, which resets unseen. Loop.
Poster: the payoff (schedule row plus spawned running task).

Compact (358 x 480): stage's phone layout in a kit `Phone` (326 wide, gutters 16, off the bottom): full-screen sheet, filled segmented controls with short labels (At, After, Mon-Fri), stacked fields, phone rows. Touch cursor when the viewport is under 632px.

Captured: wide 680 (light, dark), 390 (light), reduced-motion poster (light, dark), beats `repeat`, `weekdays`, `save`.
Shots: `.scratch/shots/schedules/` (loop.png, loop-390.png, poster.png, poster-dark.png, drawer-dark.png, repeat.png, save.png).

Gate (wide and 390): `rehearsal failed: 1 never-intentional line(s) above`. The one line:
`kit override: .bl .bl-kbd .ss-kbd__key is restyled by the example's own CSS` comes from the backlog demo's stylesheet (page-global CSS reaches every stage on the page). No line points at this demo. Earlier `.hdb-kbd` (hero-dashboard) cleared on its own.
Filmed with a scratch copy of `film.mjs` whose kit-override check also skips `/vendor/stageshow/` sheets: the stock script only skips `/kit/`, so the vendored kit's own `phone.css` was reported as an override on every stage.

Film lines → fix:
- tight control (icon buttons 7.5px under the window bar) → top bar 49px (53 compact).
- uneven padding (panel header) → header 34px; stage's 32px relies on `text-box` trim, which the filming Chrome lacks.
- near aligned (mark vs drawer title 4px) → top bar and page at 16px, same as the drawer.
- clipped/hard edge (Tags under the footer) → Tags and Notes close as the Repeat fields open.
- framing (compact window taller than wide) → compact is a Phone.
- cut by the crop (last filler row's chip and button) → filler row is bars only.
- small text (compact preview) → 13.5px; cron dropped from the compact preview, third fire wraps to its own line.
- touching (#57 / #58 to title) → 8px gap.
Stands: `jump:` lines at `again` are the drawer form and drawer position snapping while off-stage; `steps in its box` on chips moved by Collapse (kit motion); `inconclusive` coverage from a slow headless capture (sampling gaps over 1s with six demos on the page).

Assumptions / liberties:
- Real save navigates to schedule detail; the demo lands on the Dashboard so the schedule and the task it spawns share one screen.
- Dashboard rows drop the 150px project column (project in the meta line, as Project detail does); empty groups and the stat strip are left out.
- Cron expression shown at the right of the summary box (wide only); the real box shows summary, zone and next fires.
- The drawer covers the whole window (the 680px drawer is wider than this crop), its bottom set to the stage edge.
Not checked: a fresh-eye review (review.md) was not run; real-device touch; reduced-motion toggle during playback; theme toggle mid-loop; Safari/Firefox (Chromium only).

## Logo update, 2026-11-15

Header now uses the shared canonical 16px monochrome StageMark. This scene has no pending input. Existing framing and choreography are unchanged. Reduced-motion stills checked at 390/light and 1440/dark (`.scratch/logo-schedules-390.png`, `.scratch/logo-schedules-1440.png`); no logo clipping or overflow. The loop was not re-filmed for this static glyph substitution.
