---
version: 1
slug: "src-pages-docs"
primary_target: "src/pages/docs"
related_targets: ["src/pages/_root.css"]
---

## Scope
Docs at `/docs/*` (src/pages/docs, vocs layout themed by src/pages/_root.css). Mode: Read.

## Audience and task
A T3 user deploying stage, installing the PWA, wiring agents via CLI/MCP, or looking up a REST endpoint. Structure: Introduction, Deploy, Features, Agents, REST API (split by resource). Wayfinding via vocs sidebar, outline and search; Ask AI disabled on every page.

## Direction contract
THESIS: Vocs' reading layout kept intact, re-inked in stage's world so the docs feel like the app's manual, not a generic docs theme.
OWN-WORLD: Stage tokens mapped onto vocs variables: white / #1a1d23 ground, ink links and accent, hairline borders, 0 radius on every vocs element, Geist body, JetBrains Mono code, state tones only for callouts.
STORY: The reader finds the page, follows exact commands, copies code, and leaves with stage running.
FIRST VIEWPORT: Vocs top nav (logo, search, theme toggle, GitHub), sidebar, content at readable measure, outline right.
FORM: Vocs default full layout, restyled only through CSS variables and minimal overrides. seed: none (pinned) "use vocs to bootstrap the website ... adopt the theme to use the same/similar theme as stage. disable the ask ai in docs."
FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Constraints
Ask AI never shown; no invented claims; content sourced from stage README, docs/api.md, PRODUCT.md.
