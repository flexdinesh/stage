# Claude Code parser fixtures

Captured from `claude` 2.1.332 on 2026-11-25 for
[ADR 0005](../../../../docs/adr/0005-claude-code-harness.md) (#370). Real
output, redacted: scratch paths are `/home/user/src/spike`, the home is
`/home/user`, account, organization and machine ids are zeroed, and
transcripts keep only `user`, `assistant`, `system`, `mode`,
`permission-mode`, `ai-title`, `agent-name`, `last-prompt`, `tools-used` and
`custom-title` records (`api-request-blob` and the others hold the system
prompt and account data). Thinking signatures read `redacted`.

| Path | What |
| --- | --- |
| `roster/all-working.json` | `agents --json --all`: a `working` session (`status: busy`), T3 interactive entries, idle `done` sessions |
| `roster/all-blocked-permission.json` | `blocked`, `status: waiting`, `waitingFor: "permission prompt"` |
| `roster/all-blocked-question.json` | `blocked`, `status: idle`, no `waitingFor`: the turn ended asking the user |
| `roster/all-after-stop.json` | `stopped` (no pid); a `done` entry with no pid whose `cwd` reverted to the launch repository |
| `roster/active-after-stop.json` | `agents --json` (no `--all`) at the same moment: stopped and pid-less entries absent |
| `roster/all-failed.json` | `failed` (`--bg --resume` of an unknown UUID) |
| `transcripts/done-text.jsonl` | finished turn, text reply |
| `transcripts/blocked-question.jsonl` | tool errors, then a final text asking the user; turn over |
| `transcripts/blocked-permission.jsonl` | tail is a `tool_use` waiting for approval; no reply |
| `transcripts/working.jsonl` | mid-turn; no reply yet |
| `transcripts/worktree.jsonl` | `-w` session; `cwd` is the worktree, `gitBranch` `worktree-spikewt` |
| `transcripts/print-then-bg.jsonl` | `-p --session-id` seed (`entrypoint: sdk-cli`), then a `--bg --resume` turn (`cli`) |
| `transcripts/t3-code.jsonl` | first records of a T3-launched session (`entrypoint: t3-code`); message text replaced |
| `jobs/*-state.json`, `jobs/*-timeline.jsonl` | `~/.claude/jobs/<short>/` for the blocked, question, working and done cases (undocumented; Stage does not depend on them) |
| `sessions/bg.json`, `sessions/t3-code.json` | `~/.claude/sessions/<pid>.json` for a background and a T3 session |

The prompts carry `stage-launch:req-*` or `[stage-marker:*]` markers.
