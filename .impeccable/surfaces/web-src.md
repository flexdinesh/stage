---
version: 1
slug: "web-src"
primary_target: "web/src"
related_targets: []
---

## Scope
Whole t3-stage web app (web/src): Dashboard, Projects, Project detail, Task detail (with Run order panel), Sessions, Session detail (with continuation Run order and child inspection), History, Templates, Settings, New task and Queue continuation drawers, Ctrl K palette. Mode: Operate.

## Audience and task
Owner, on laptop and phone, queueing T3 tasks and continuations of existing sessions, catching needs-input, watching running/completed work from every starting point, browsing history. Session state remains independent from a waiting continuation; run-order entries explain each condition before delivery.

## Direction contract
THESIS: A workbench, not a dashboard of cards: one dense, ruled list per state where color appears only when a task's state earns it. Refuses the rounded-card SaaS dashboard and the neon-terminal costume.
OWN-WORLD: White (light) / #0c0e13 (dark) ground, 0 radius, 1px hairlines rgba(ink,.11), uppercase JetBrains Mono panel headers on a #f6f7f9 bar, Geist for titles and actions, ink-black (light) / near-white (dark) primary buttons, tinted-border state chips with square markers: soft yellow needs input, sky running, teal completed, green settled, muted red failed.
STORY: The owner sees at once what is waiting on them, opens it in T3 in one tap, then scans running and completed work and queues the next task without leaving the page.
FIRST VIEWPORT: Laptop: 48px top bar (mark, tabs with needs-input count, Ctrl K search, T3 chip, theme, black New task). Under it: Dashboard H1 with a `//` meta line, a single-row compact stat strip of state counts (click filters), then ruled state groups starting with Needs input (yellow 2px left edge, primary Open in T3). Phone: compact top bar, 5-cell stat strip, stacked rows, bottom tabs.
FORM: User-pinned Workbench direction (px0/zed inspired), first of three presented (A Workbench, B Panes, C Lanes), refined to Ink accent, white ground, compact strip, Geist + mono details. seed: waived by user ("Waive (Recommended)", 2026-11-08: roll was unavailable when the direction was chosen; user picked the direction and approved screens directly from previews)
FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Signature interaction
The stat strip is the filter: clicking a count isolates that state's group (URL `?state=`); the Ctrl K palette reaches any task, thread, or project by title.

## Motion grammar
150-200ms color/opacity transitions on state change only; no entrance choreography.

## Constraints
Sharp corners everywhere; no eyebrow above headings (meta line sits below H1); authored SVG icons; no modals (drawers/sheets); color only on states.
