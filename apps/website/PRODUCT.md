# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users
Developers who already run T3 Code and juggle many agent threads across several projects. They arrive at this site deciding whether stage is worth self-hosting, then come back to the docs to install it (desktop or headless), pair it with T3, and wire up agents (CLI/MCP). They read on a laptop most of the time and skim on a phone.

## Product Purpose
The marketing and documentation site for stage, a self-hosted task manager for T3 Code. The landing page shows why stage exists; the docs get a T3 user from zero to a running stage and to agents queueing their own work. Success: a T3 user understands in one viewport what stage does beside T3, sees each core workflow working, and reaches Install in one click.

## Positioning
stage sits beside T3 rather than replacing it: T3 is where conversations happen; stage owns the backlog, run order, schedules and the cross-project view T3 does not provide, and lets a waiting thread be answered from a phone. The site sells exactly those reasons, the ones stage was built for:
1. Backlog tasks: save prompts with provider, model, effort and workspace now, start them later.
2. Trees: a task waits for the tasks it needs; it starts when they settle, carrying a handoff from each.
3. Scheduled and cron tasks: one-shot at a time, or a recurring schedule that spawns a task per fire.
4. Planning sessions: a session plans a complex job, breaks it into independent work, and uses stage's MCP to create tasks and a tree of the dependent ones, each on a different model.
5. PWA and push notifications: install stage, get a push when a thread needs input, answer it from the lock screen in a tap.

## Operating Context
- Static site (vocs, full-static) on Cloudflare Workers (static assets), worker `stage-website`.
- Source: github.com/flexdinesh/stage (stage; this site in `apps/website`).
- Facts come from stage's engineering contracts (docs/*.md: install, desktop, service, runtime, features, api), the code, and PRODUCT.md; docs/architecture.md maps each contract to its website page. stage is self-hosted, single-user, no auth, on a private network. The site has no REST API reference (repo-only, docs/api.md).

## Brand Commitments
- Name: stage (lowercase).
- Same visual world as the stage app: inspired by zed.dev and px0.ai; sharp borders, mildly colored borders; Ink accent (black on white / near-white on near-black); pure white light mode; color only on states; Geist UI text with JetBrains Mono for metadata.
- Product visuals on the landing page are stageshow demos of stage's real UI, not screenshots.

## Evidence on Hand
No testimonials, customers, user counts, or metrics exist and none may be invented. Demo data shown in visuals is illustrative (task titles, project names, model labels) and must read as such.

## Product Principles
1. Show the workflow, don't claim it: every feature section demonstrates the thing happening in stage's own UI.
2. Needs-input yellow is the one loud color, as in the app.
3. Install is always one click away.
4. Docs read like the contracts: precise, dense, no marketing in guide pages.
