# Rehearsal: hero-dashboard

Gold: `linear` (wide hero: one 1:1 window, realistic chrome, one real row among filler, a caused reset) for the frame; `dark-hero` for the status change on one chip and the list moving by its height; `hero-demo` for the named-target cursor beats. Not filmed: the brief forbids starting a dev server, so the golds were read from source, not filmed beside this one.

Kept from the golds: one window at 1:1, real chrome, real words only on the story row, filler rows keep the product's signature mark (stage's state chips, like Linear's status circles), cursor aimed at named targets, the loop ends where it began with a seen cause.

Changed: stage's Ink Workbench throughout (`--st-*` tokens only, 0 radius, hairlines, mono metadata, ink buttons). Wide: 1240x682 stage = the full `.lp-hero__visual` column; the browser window is inset 0, so its left edge is the H1's left edge and its right edge the grid edge (measured: 1440 viewport H1 x=100, visual 100..1340, window 100..1340, scale 1; 1280 viewport H1 x=24, visual 24..1256, window 24..1256, scale 0.994). Runs off the foot; no own fade (the landing's 64px `.lp-demo` mask softens it). Frame hairline drawn inside so the stage edge never cuts it. Compact: a redesign, not a squeeze: stage installed on a phone (kit `Phone`, pro frame, 326 wide, pinned 16px top/left, off the bottom), laid out in stage's own phone layout (52px top bar, stat cells as counts plus square markers, project line over title, actions under it, 44pt options).

Crop: stage heights chosen so the poster/form-open frame ends on a full Running row with the next Running row as the ~64px filler under the page fade (wide 682: last full row ends y=623; compact 660: last full row ends y=597). The rest frame then ends on full Done rows; Done has 3 rows (stat 3) so no panel header sits in the fade in any state.

Beats (loop about 15s):
1. A Running task (#51) asks: it lifts out of Running (Fly, slight lift and shadow) into Needs input. The empty "Needs input 0" ruled line becomes the panel header in place (Paper to Bench Gray, gray marker to yellow, empty sentence fades); Running closes the gap. The chip rolls running to needs input and the 2px yellow edge and Answer button arrive during the flight. After landing: stat counts roll (1 / 2), stat marker opens, Dashboard tab count chip opens.
2. Cursor presses Answer; stage's Needs input form (RequestForm: question, square single-select radios, 0/1 answered, Send answer disabled at 45%) opens under the row, the yellow edge running down it.
3. Cursor picks "5xx and timeouts only" (ink square, 5% ink fill, 1/1 answered, Send answer enables), presses Send answer (square busy spinner). Sent: the button confirms on itself (label rolls to "Sent", check icon), the row's chip rolls back to running, Answer closes, the yellow edge leaves row and form; held 1.1s. Then the form folds, the row flies back to Running, Needs input folds back to its empty line, counts and tab chip roll back on landing. That is the opening frame: the seam's cause is the answer being sent.

Poster (reduced motion): the sent moment: question, picked option, 1/1 answered, "Sent" with check, row reading running again. Tells ask, answer, back to work on its own.

Captured (`.scratch/shots/hero-dashboard/`): loop-1440.png, loop-390.png (gate sheets), poster-1440.png, poster-390.png (reduced motion, light), poster-1440-dark.png, poster-390-dark.png, rest-1440.png / rest-390.png (early-loop stills), sent-390.png / sent-zoom-390.png (send to landing at 390), before-1440.png / before-390.png (pre-fix).

Gate (stock film.mjs, after the last edits):
- 1440: `rehearsal passed (necessary, not sufficient: references/verify.md and the fresh eye in review.md still stand)`
- 390: `rehearsal passed (necessary, not sufficient: references/verify.md and the fresh eye in review.md still stand)`

Film lines and fixes:
- buttons with no padding (host reset beats the kit's layer) -> padding set in the demo's unlayered button rule.
- `tight control` 7.5px under the bar -> top bar 49px so 32px controls clear the rule by 8.
- `framing: window 358x600 narrower than tall` (compact desktop window) -> compact is a phone.
- `framing: phone 9px short of the right edge` -> 16px gutter.
- `kit override .hdb-kbd .ss-kbd__key` -> only colors/border kept on the keycap.
- `loose stack` label over the empty sentence (compact) -> sentence moved into the label's line box, pulled up to 4-6px of ink.
- `hard edge` tab chip cut while its room closes -> words fade before the room closes, and after it opens.
- `handoff shift` (chip mid-roll at the swap, 0.5px landing offset) -> flyer held 320ms past landing; landing rect measured from the slot.
- Labels clipped in compact ("Answe", "runnin") after the layout switch -> Scene remounts on layout so Roll re-measures.
- Loop seam warnings (from/to, cursor) -> rest point move, rects cleared at the end.

Standing (not gate lines, judged):
- `jump:` on the tab chip / stat marker / Count: the page films at 50-400ms intervals (six stages on one page); an rAF probe shows the widths easing smoothly. Curves softened to the travel ease anyway.
- `settle snap` 0.7px and one 14.6px on the chip's Roll after the flight: not traced to a visible hop at speed; worth a slowed look.
- Compact type: the phone draws its screen at about 0.77 of the stage, so stat labels are gone (counts plus square markers, gray marker on a zero) and panel labels, meta, chips and "answered" are set at 13-13.5px so they render >= 10px; the 390 gate no longer prints `small text`.
- `jump:` +50px on one frame at [answer] (390): the form Collapse opening inside a 1.1s sampling gap; contact sheet shows it opening smoothly.
- `handoff shift` ~5px on four unnamed spans (390), `settle snap` 0.7-3.3px on the row (1440, varies run to run): not traced to a visible hop in the zoomed send-to-landing sheet; worth a slowed look.
- `touching` #51 to title 5px (390) and `near aligned` Answer vs project (390): stage's own row spacing; kept.
- `near-tangent` rows 1px apart: that is the hairline between rows.
- `appears with no entrance` on a Count: on first mount/hydration.

Review: no separate fresh-eye reviewer was run.

Assumptions: inline answer form under the row is a demo liberty (stage navigates to task detail's Needs input panel); the form omits the panel header and the "Or type your own answer" textarea for height; notifications bell left out of the top bar per the brief's list; search trigger shows the xl form (text + Ctrl K) although 1240 is under stage's xl breakpoint.

Dark: posters checked at 1440 and 390 (film `--dark`).

Not checked: the dark loop in motion (posters only; tokens are shared), the full loop under a theme toggle mid-play, tablet widths between 640 and 1240 (wide design scales down to about 0.52 at 640), Safari/Firefox, a live frame-rate log beyond the one rAF probe, pause/offscreen resume.

## Logo update, 2026-11-15

Replaced the square with the shared canonical 16px StageMark, including the phone header. The top step follows the same needs-input count as the stat strip; choreography and framing are unchanged.

Verification: direct browser sampling across full loops at 1440/light and 390/dark observed idle and needs-input states with zero mismatches against the displayed count (1167 and 1129 frames). Reduced-motion transitions are 0s. Home/docs chrome also checked at both widths and themes.

Rehearsal: `.scratch/logo-hero-1440.png` (9 frames, 2s intervals). The tool printed `rehearsal passed (necessary, not sufficient: references/verify.md and the fresh eye in review.md still stand)` but exited 2: motion coverage was inconclusive from sampling gaps and the scanner's pre-hydration `data-ss-verification-stage` attribute caused a hydration diagnostic. A reduced-motion 390 still had the same scanner diagnostic. Neither run is claimed as complete motion verification.

Fresh-eye review: no new logo defects in the desktop/light, mobile/dark, OG and contact-sheet frames. Fine transition smoothness and Safari/Firefox remain unverified; the direct state checks prove state agreement, not sub-frame motion quality.
