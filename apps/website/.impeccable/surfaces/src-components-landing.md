---
version: 1
slug: "src-components-landing"
primary_target: "src/components/landing"
related_targets: ["src/pages/index.mdx","src/components/demos"]
---

## Scope
Marketing landing page at `/` (src/pages/index.mdx, src/components/landing, src/components/demos). Mode: Persuade.

## Audience and task
A T3 Code user deciding whether to self-host stage. They must believe stage removes the babysitting of many threads (backlog, run order, schedules, planned batches across models, push to answer) and act: Get started (Deploy docs) or GitHub. Proof is the product itself, shown in stageshow demos of stage's real UI with illustrative data. No metrics or testimonials.

## Direction contract
THESIS: The page is a workbench run in public: each section is one stage panel doing its job, ruled and inked, not a SaaS feature grid of icon cards. Refuses the gradient hero, the bento of icon tiles, and the neon terminal.
OWN-WORLD: Stage's Ink Workbench: white / #1a1d23 ground, 0 radius, 1px hairlines at 11% ink, uppercase JetBrains Mono labels, Geist display tracked -0.03em, ink primary buttons that invert per theme, color only from the six state tones; needs-input yellow is the single loud mark.
STORY: The visitor sees a thread flip to needs input and get answered in place, then watches five workflows in turn: backlog, linked run order with handoff, schedules, a planning session creating a linked batch on different models, a push answered from a phone. They click Get started.
FIRST VIEWPORT: Desktop: vocs top nav; left-aligned H1 (large Geist, 2 lines), one-sentence subline, `//` mono meta line, CTAs Get started (ink) + GitHub (outline); below, full-width animated stage dashboard (~1080x460) bleeding to the page grid edge. Phone: H1, subline, stacked CTAs, compact 358 dashboard.
FORM: Hero then five alternating-density feature rows, copy left (H2, sentence, 2-3 mono facts, docs link) and demo right, separated by full-width hairlines; closing CTA band. seed: none (pinned) "adopt the theme to use the same/similar theme as stage." + "make the landing page a marketing page for stage. have top section and feature sections follow. feature section will highlight top features and will have an animated ui on the right side."
FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Signature interaction
Each demo plays only while in view and holds its poster frame (the payoff) under reduced motion; the hero answer loop is the page's one authored moment.

## Motion grammar
Stageshow choreography inside demos only; page chrome keeps stage's 150ms color/opacity transitions; no section entrance animations.

## Constraints
No eyebrow/kicker above headings; no section numbers; no invented claims; sharp corners; demos follow site light/dark.
