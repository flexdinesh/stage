# Rehearsal: run-order

Gold: `story` (related objects: a hero plus a raised satellite tied by a relationship), with `payout-flow` timing for the no-cursor beats and `overlap` placement (satellite in front of the window, covering only filler rows). Gold not filmed: the stageshow playground needs its own dev server, which the brief forbids; built from the gold sources instead.

Changed from the gold: stage's Ink Workbench throughout (0 radius, hairlines, mono metadata, state-only color via `--st-*`). Hero = browser window on a project page, `TASKS` ruled list (#12, #13 + skeleton rows). Satellite = #13's `TREE` panel (TreePanel.tsx / TreeNodePanel.tsx; was the `RUN ORDER` panel before trees), on `--ss-shadow-satellite`, top on #13's row line (measured with `rect('row-13')`), bottom held at the 24px gutter in every resting state. Compact (358x461) is a redesign: bare ruled panel (no window, which would read taller than wide), phone rows (title leads, state chip right, needs chip in the meta line), numbers-only levels, record reply wraps.

Beats (loop ~16s):
1. Cursor opens the "Needs" picker ("Add a need…"), picks `#12 Retry failed webhooks`, presses Add. Picker folds away; levels join (cell 1, connector draws, cell 2 dashed with ink outline); rows get `then #13` / `needs #12`; "Hand off context" row appears.
2.  #12 `running` -> `done` (teal); cursor selects Complete #12 -> `completed` (green), meta rolls to `completed just now`; level cell and "Needs" meta follow.
3. #13 `backlog` -> `launching` -> `running` (sky), its `needs #12` chip leaves (no longer waiting), cell 2 tints; the toggle slot crossfades to `HANDOFF FROM #12 · sent 14:02`, reply excerpt, `billing-api#41` (open, sky), branch `feat/webhook-retries`.
Payoff holds 3.2s; reset: #12/#13 rows and the satellite fade (300ms), snap to start while hidden, fade back; panel header, skeleton rows and window chrome stay.
Poster: #12 completed, #13 running, handoff shown.

Captured: wide 680x480 at 1280 viewport and compact at 390, light loop + `--states`; dark poster (reduced motion) wide; light reduced-motion posters at both widths. Heights checked with a Playwright probe: satellite bottom 24px from the stage edge pre-launch and final, both widths.

Gate (1280 and 390, last run): `rehearsal failed: 1 never-intentional line(s) above`. The one line is `kit override: .bl .bl-kbd .ss-kbd__key` from the backlog demo's CSS (film scans page stylesheets); no line points at run-order. Earlier runs also showed pwa-push `.ss-phone__*` overrides, since fixed by that agent.

Film lines handled:
- satellite gutter (17/27/30/34/38px) -> toggle and handoff share one States slot sized to the record, so the panel resizes once (at Link) and rests at exactly 24px.
- uneven padding (header 8 vs 12) -> header labels trimmed / 11px line, header 33px (32 + hairline, as stage).
- popover near bottom edge -> list cut to one real option + one skeleton.
- tight control (Link / Copy handoff vs rows behind the satellite) -> compact skeleton rows drop the chip skeleton, 8px padding.
- uneven inset on handoff head -> real "Copy handoff" button on the right.
- cut words in compact (reply, branch) -> reply wraps, `branch` key hidden at 358.
- step text shifting 12px when cell 2 tints -> marker slot opens by width instead of display toggle.
- hard edge on handoff refs -> record arrives by crossfade inside an already-open slot.

Left standing (candidates, not gate lines): `jump`/`settle snap` on state chips at the reset snap (rows hidden at opacity 0 at that moment) and on Roll width changes in sparse samples; `steps in its box` on the compact right-pinned state chip as its label width eases. Sampling coverage stayed `inconclusive` (machine shared with other agents filming; gaps up to ~1.7s), so frame-level motion was not fully judged.

Assumptions / liberties:
- Real stage shows the sent handoff in the Prompt panel; here it takes the "Hand off context" slot of the Tree panel so one surface tells the story.
- Dependents section, start mode and base omitted from #13's panel. `then #13` on #12's row is illustrative (real rows show only the `needs` chip). Running-task "Open in app" actions omitted from rows.
- Header meta reads `2 tasks · 2 levels` (real: the tree title, then `2 tasks · 2 levels · 1 project · auto`).
- Combobox drawn as a dropdown list with an Add button (real NeedsPicker is a native select that adds on pick); Remove stands in for the real per-need remove icon. Before trees the panel linked tasks (Runs after, Link, Unlink); strings were updated to the tree model.

Not checked: dark-mode loop (only the dark poster), compact dark, live rAF pacing, offscreen pause/resume, reduced-motion toggle mid-play, touch.
