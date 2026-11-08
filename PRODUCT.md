# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users
One developer (the owner) running many T3 Code agent threads across several local projects. Uses t3-stage mostly from a laptop and a phone on the LAN, rarely a desktop: queueing work, checking what needs input, and glancing at what is running or done.

## Product Purpose
A personal stage manager for T3 Code. It queues tasks (prompt + provider/model/effort + runtime/workspace), launches them as T3 threads, tracks thread status, runs sequential chains (next step starts when the previous thread is settled in T3), and gives a cross-project history of every T3 thread. Success: never miss a thread that needs input, keep a queue moving without babysitting, and see recent work at a glance.

## Positioning
Sits beside T3 rather than replacing it: T3 remains where conversations happen ("Open in T3"); t3-stage owns the queue, chains, and the cross-project view T3 does not provide.

## Operating Context
- Self-hosted on the T3 host via docker compose, bound to 0.0.0.0:7380 on a home LAN, no auth.
- Talks to T3 only through T3's MCP endpoint; polls active tasks every 5s, full sync every 5 min.
- Settling a thread happens in T3, not here; chains wait for it.

## Capabilities and Constraints
- Projects mirror T3 projects 1:1; existing T3 projects import automatically.
- Task states: backlog, launching, running, needs input, completed, settled, failed, interrupted, cancelled.
- Task actions: start/restart, follow-up (auto/queue/steer/restart), stop, duplicate, delete, open in T3.
- Chains: ordered tasks (may span projects), start/pause/resume/skip/reorder; failure pauses.
- History: all mirrored threads (about 785, of which about 487 are subagent threads), time-window and attribute filters.
- Templates for prompt + model presets. Settings: connection, pairing, sync now.

## Brand Commitments
- Name: t3-stage (lowercase).
- User-pinned visual direction: inspired by zed.dev and px0.ai; sharp borders, mildly colored borders; Ink accent (black on white / near-white on near-black); pure white light mode; color only on states; Geist UI text with JetBrains Mono for metadata.

## Evidence on Hand
Real data lives in the user's T3 instance; there are no testimonials, customers, or metrics to show and none should be invented.

## Product Principles
1. Needs-input is never missable: it leads every view it appears in.
2. T3 is the place to talk; t3-stage is the place to queue and watch. Every task links straight to its thread.
3. Phone and laptop are both first-class; phone gets the glance and the quick action.
4. Dense and scannable over decorative; state carries the color.
