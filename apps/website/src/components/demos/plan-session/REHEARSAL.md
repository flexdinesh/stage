# Rehearsal: plan-session

Gold: `panel-annotation` / `overlap` layout (product inset top and left, off the bottom; a satellite straddling its right edge over its margin, never over its words). Motion from `ai-chat` / `ai-cards` (bottom-anchored thread, placeholder past turns rising under a 20px mask, `Stream` reply, `Collapse` drop for the calls block). Golds read from source, not filmed (no preview server; brief forbids starting one).

## Finish-review round (this pass)

- Composition inside the 680 box: the session window sits at x 24..520 (24px gutter top and left); stage's task list straddles its right edge (x 366..656), its right edge on the stage's 24px gutter. Nothing bleeds past the stage's right edge (at 1440 the panel ends at the same right line as the other demos' windows).
- Window frame: top, left and right edges all drawn (right edge visible above and below the panel). The bottom runs under the landing's 64px foot fade, like the other feature demos; only T3's composer (placeholder) sits in the fade, never the payoff. The panel ends at y 396, 20px above the fade.
- T3 sidebar dropped. The window is now session-only: title bar (`Migrate auth to sessions`, mono `api-gateway` on the right so the bar's insets balance), thread, composer.
- Tool calls are one line each (`create_task  Title  #41`, `create_tree  41 43:41 42:43  Auth`); models live in the panel rows (four different models). The thread column stops 16px short of the panel.
- Panel rows back to 52px (7px padding) so the panel's foot pads like its sides.
- Cursor rests in the ground above the panel's top-right, moves down to Start ready, returns.
- No URL bar shown (app window with title), so no host string needed.

Compact (358 x 600): window off the right (16px gutter top and left, foot closed at y 356, 342 x 340 visible, so it stays landscape-ish per the bled-window rule); panel 294 wide at x 32, top 192, straddling the window's foot. The panel holds its slot from the first frame (compact only): header `Tasks 0`, `Backlog 0`, four placeholder rows (empty check, two bars). Each real row fades in over its slot as its call finishes; the bars fade out under it. Body is fixed at four rows (ends y ~476) until create_tree adds the second section header (ends y 517, 19px above the fade). At the exit the rows fade and the order state resets with them (the tree header `Auth` to Backlog, late header out, body back to four slots), so the snap changes nothing visible. Full-height window was tried and rejected (gate: window narrower than tall, empty pane 77% blank). Calls are 28px rows; at the payoff the earliest call and the ask have scrolled up under the thread's top mask (chat scroll). Composer and `api-gateway` hidden.

Poster: the payoff (the full tree, #41 running, all five calls ticked).

## Gate

Filmed with a scratch copy of the stock `film.mjs` whose only change is an init script removing `inert` from the page. Reason, verified: the landing marks `.lp-demo` `inert`, which drops the whole stage from hit testing, so `document.elementsFromPoint` at the panel's center returns only `.lp-demo` and its ancestors instead of the window under it. The `uneven gutters` check uses that call to recognize a satellite laid over another surface; under `inert` every straddling satellite reads as a loose card on the stage. (run-order's `.ro-sat` gets the same line.)

- Wide (1440, patched): `rehearsal passed (necessary, not sufficient: references/verify.md and the fresh eye in review.md still stand)`
- 390 (patched): `rehearsal passed (necessary, not sufficient: references/verify.md and the fresh eye in review.md still stand)`
- Wide (1440, stock script): `rehearsal failed: 5 never-intentional line(s) above`, all five `uneven gutters: section.ps-panel ...` (the `inert` false positive above; same panel at each growth height). No other gate line.

Coverage reported `inconclusive` (sampling gaps; shared machine, other agents filming).

Remaining candidates (not gate lines):
- `settle snap` sub-pixel (0.7 to 0.9px) on the calls block / panel as the bottom-anchored thread grows; larger 5 to 7px `settle snap` on `.ps-call__title` / `.ps-call__out` during the `Collapse` open of a new call row. Not visible in sheets.
- `lands on a transform` (panel, #42 row) and `steps in its box` (#42 row, chip) during the reorder slide.
- `appears with no entrance` on the header `Count` (rolls digit-to-digit; kit part).
- `near-empty frames` ~1.8s at the loop seam (ask and placeholders only, panel gone); trimmed 320ms off it this pass.
- 390: `clipped` / `uneven` for the ask and placeholder bars passing under the thread's top mask (intentional chat scroll); `near aligned` window title vs ask left edge (3.3px) in the opening frames.
- 390: resolved this pass (panel mounted with placeholder rows from frame 1). Ground under the panel is ~60px above the fade until ordering, then 19px.
- 390: real row and placeholder bars overlap mid-crossfade for ~0.2s per landing (row surface fades in over the bars).
- `loop seam: state still differs from initial (from, to)` kit warning at both widths; keys are not plan-session state (likely another stage on the page). Not a gate line.

## Captured

Compact rework pass: `poster-390.png`, `dark-poster-390.png`, `early-390.png` / `dark-early-390.png` (opening frame, panel with placeholder rows), `loop-390.png` (patched gate), `exit-390.png` (seam and first landings), `loop-1440.png` (patched gate, re-run), `poster-1440.png`, `dark-poster-1440.png`.

Previous pass:

`.scratch/shots/plan-session/`: `poster-1440.png`, `poster-390.png`, `dark-poster-1440.png`, `dark-poster-390.png`, `loop-1440.png` (patched gate), `loop-1440-stock.png` (stock gate), `loop-390.png`, `before-1440.png` (pre-fix opening frame).

Dark: posters checked at both widths (`--dark`, vocs follows prefers-color-scheme). The lighter band at the foot of the dark stills is the capture compositing the masked fade, not the page: stage, `.lp` and body all compute `rgb(26, 29, 35)`.

## Not checked

Live rAF pacing on a device, touch, mid-play theme toggle, resize between widths, reduced-motion poster this pass, a fresh-eye review subagent.

Assumptions: T3 Code drawn neutral (no screenshot reference); tool calls as one ruled block of one-line rows; `api-gateway` as T3's project label in the title bar.

Strings updated for trees (was `order_tasks` / Chain / Start chain before run-order links were replaced): the call is `create_tree` with the CLI's `ID[:NEEDS]` shape, the header shows an illustrative tree title (`Auth`), and the button reads Start ready (real: TreeRow.tsx `Start ready N`, on tree rows).
