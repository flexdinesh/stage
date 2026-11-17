# pwa-push rehearsal

## Gold copied
- `phone-touch`: structure (lone phone pinned a gutter from the top, running off the bottom, touch cursor, one object opening into its screen).
- `dynamic-island` / `device-pair`: `LockScreen` + `Notification` usage, phone centered on the page ground.
- Launch: kit `Expand` with `face` (notification to app, as iOS opens a push), per kit.md.

## What changed
- Product: stage's installed PWA. Push copy is stage's real payload (`server/internal/notify/notify.go`): title `Needs input`, body `<task title> · <project>`. App icon is `public/logo.svg`, inlined.
- App screen rebuilt from stage's phone layout: phone top bar (mark, bell, search, `+ Task`, 40pt buttons), PageHeader (24px title, `//` mono meta), chips (state chip with marker, model chip), Needs input panel (2px yellow edge, Bench Gray header with `NEEDS INPUT` label, need chip, `Open in T3`; header chip, question, square radios in a ruled list, footer with `n/1 answered` and primary `Send answer` with square busy spinner), then a confirmation row matching `NoticeRow` (7px green marker, `Answer sent to T3`, mono `Cutover: Grace period`) and the header changing to `ANSWERED` with the edge going transparent.
- Story (~14s): lock screen, push arrives (0.7s); tap, app launches from the banner; pick `Grace period`; `Send answer` (busy 0.5s); form folds into the confirmation; state chip rolls `needs input` to `running` (sky) 0.65s later; 2.4s hold; reset with a seen cause: a swipe down from the top-left edge pulls the lock screen down over the app (iOS lock screen pull-down), app state snapped back under it, then the cover is lifted unseen off an identical lock screen (`data-snap` on the cover only).
- Poster: answered panel, chip running (result left on screen).
- Wallpaper: ink lock screen with a faint sky light, built from `--st-*` tokens, dark variant keyed off `:root[data-vocs-theme='dark']`.
- Sizes: wide 680x480, phone 380 centered; compact 358x440, phone 326, mono metadata stepped up to 13.5pt so it reads at the column width.
- No laptop satellite: the frame stays calm, phone-only.

## Checked
- `film --gate` loop, wide and `--width 390`: `rehearsal passed` (using a scratch copy of film, see below).
- `--states` sheet, slowed close-ups of the send press and the sent roll (detector `jump:` lines there are sampling gaps; slowed frames show the chip and label easing).
- Dark mode (`--dark`), reduced-motion poster still.
- `oxlint --deny-warnings` on this folder, `tsc --noEmit`: clean.

## Film caveats (outside this folder)
- Film's `kit override` check exempts only stylesheets under `/kit/`, so the vendored kit (`/src/vendor/stageshow/`) can be flagged as "the example's own CSS". It also checks every stylesheet on the page, so other demos' selectors (`.bl .bl-kbd .ss-kbd__key`, `.hdb-kbd .ss-kbd__key`) fail this stage's gate. The scratch copy adds `/vendor/stageshow/` to the exemption and only counts selectors that match inside the filmed stage. Unpatched film fails only on `.bl .bl-kbd .ss-kbd__key` (backlog demo).
- Host CSS layer order isn't deterministic across dev reloads: when kit CSS declares `@layer stageshow` before vocs's `index.css`, vocs_base's `* { margin: 0; padding: 0 }` beats the kit (lock screen clock margin, button padding, banner margins). It was correct on the final runs. A fix outside this folder: declare the order once, early, e.g. `@layer vocs_theme, theme, base, vocs_base, vocs_components, components, vocs_utilities, utilities, stageshow;` in a stylesheet that loads before any demo.

## Not checked
- Production build (`vocs build`) CSS order and the static render.
- Fresh-eye review (`references/review.md`) by another agent.
- Remaining non-gate lines: `small text` on the kit notification's `now` (9px at the 326 compact phone, kit's true iOS size, same as the `phone-touch` gold), and `near aligned` between `ANSWERED` and `Answer sent to T3` (the confirmation is indented by its marker column on purpose, as in stage).

## Logo update, 2026-11-15

Both notification placements now load the canonical `public/logo.svg`; the old duplicated rounded artwork is removed. The app header uses StageMark, yellow while the Needs input panel is unanswered and monochrome after sending. Existing choreography and phone framing are unchanged.

Verification: direct browser sampling across full loops at 1440/light and 390/dark observed both glyph states with zero mismatches against the panel's waiting state (1255 and 1250 frames). Both notification images loaded. Reduced-motion glyph transitions are disabled.

Rehearsal: `.scratch/logo-push-390.png` (8 frames, 2s intervals). The tool printed `rehearsal passed (necessary, not sufficient: references/verify.md and the fresh eye in review.md still stand)` but exited 2: sampling gaps and deferred moving geometry made coverage inconclusive; scanner pre-hydration instrumentation produced a hydration diagnostic. Existing advisories included the 9px notification time and the panel heading/confirmation indentation. No new logo defect was visible.

Fresh-eye review: no new logo defects in the 390 contact sheet. Fine transition motion and Safari/Firefox remain unverified; direct state checks establish state agreement only.
