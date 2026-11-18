# Rehearsal: readme-cover

Gold: `overlap` (a second surface in front of the hero window's right edge, both off the foot) with `ai-chat`'s bottom-anchored thread and `Stream` reply; the stage window, row, chip and Start come from the landing's `backlog` demo, the call block and busy mark from `plan-session`. Changed: the satellite is a second real window (stage in a browser), overlapping T3's right edge by 28px, so the two apps read side by side the way the earlier README cover's reference frame did; connectors labeled `MCP` and `start` (2px ink, 11.5px labels) cross a 65px channel between the two apps' content.

Beats: ask typed in T3's composer and sent (0 to 2.7s) … Claude's line streams, `stage · create_task` runs and returns (to 5.4s) … the `MCP` line draws from the lit call to stage, the row lands at the top of Tasks, count 5 to 6 (to 7.5s) … the cursor crosses to stage and presses Start: chip rolls `backlog` to `running`, the button becomes `Open in T3 Code` (8.6s) … the `start` line draws back, T3 switches to the task's own thread (new sidebar row, origin `started from stage #45`, Claude streaming, Working) … payoff holds 2.4s (`poster`) … T3 returns to the planning thread and the row and thread fold away. Loop 14.83s.

Design: 920 x 420, shown at 880 (no compact; GitHub scales the image). Transparent ground (alpha checked by the recorder) so GitHub's own light or dark page is the ground. T3 window 480 wide, 60px past the foot; stage window 428 wide, 36px lower, its last whole row ending 4px above the foot.

Captured: wide (960 viewport) light and dark, `--states` sheets with the gate, poster stills at the `mcp` and `poster` marks, every frame of the recorded loop in both themes (`mise run readme:cover -- --keep`).

Recording: 890 frames at 60 fps per theme on the virtual clock, 2x (1840 x 840) with alpha, lossless WebP (734 distinct frames after merging stills; durations 16/17ms). Light 2.62 MB, dark 2.69 MB. The last recorded frame equals the first (PSNR inf), so the seam is continuous. Dark frames checked composited on GitHub's #0d1117.

Gate (light and dark, `--states --states-for 18000 --gate`): `rehearsal passed (necessary, not sufficient: references/verify.md and the fresh eye in review.md still stand)`

Film:
- `framing` (windows nearly square / floating whole / off center / 20px bleed) → first layout (two whole windows with a gap) → moved to a wide banner (880 then 920 x 420), both windows off the foot, stage overlapping T3's right edge (the satellite waiver).
- `loose stack`, `near aligned`, `uneven padding`, `unsized text`, `half-pixel glyph`, `touching`, `cut by the crop`, `near-tangent`, `steps in its box`, sidebar row overflow → fixed (row anatomy with a left state column, 15px offsets, uniform composer inset, sized separator, 12px send icon, call output shortened to `#45`, rows end on the foot, sidebar Collapse track `minmax(0, 1fr)`, rows on a layer).
- `appears with no entrance` on `Count` (kit part rolling digits) → stands (same as plan-session).
- `vanishes with no exit` / `appears with no entrance` on the composer placeholder → it is now mounted throughout and fades.
- `vanishes with no exit` on the `start` label at the exit → the reset snap waits 300ms for the line and label to fade.

Review (fresh-eye subagent, on the recorded frames):
1. Dark cover an opaque plate → real: `_root.css` paints `body` and the dynamic CSS imports load in any order. `cover.css` now wins by specificity; the recorder refuses frames without alpha.
2. Opening mostly void and bars → the planning thread opens on a real earlier exchange; #41 and #38 and two sidebar threads have real titles.
3. Empty top-right corner → the stage window moved up from 70 to 56 (36px stagger kept for depth).
4. Connectors too small → a 65px channel (T3's words stop 80px short), 2px strokes, 11.5px labels; design widened to 920.
5. Cursor parked on the stage window's border → rests in T3's thread, clear of arrivals.
6. Onion skin on the thread swap → the outgoing view is gone (160ms) before the incoming fades in.
7. Start and chip blank mid-change → chip uses the vertical `Roll`; the Button's label crossfade is the kit's own (one frame or two), left as is.
8. Send button held in hover → the cursor parks at T3's sidebar foot after sending.
9. I-beam over the typed text → the composer is clicked low, under the text line.

Assumptions: T3 Code drawn neutral (no screenshot reference): sidebar with wordmark, project label, threads; model in the thread bar; an `MCP · stage` chip in the composer. The return line is labeled `start`, not MCP: the real Start goes through stage's harness adapter.

Unresolved: none.

Not checked: GitHub's own rendering of `<picture>` in its mobile app, a 390 width (no compact by design), reduced motion (the README image has none), Safari decoding of a 60 fps lossless WebP.
