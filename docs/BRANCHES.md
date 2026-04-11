# Branch History

Kerebrom keeps previous implementation lines available for audit while making `v1` the public product line.

## Active Branch

| Branch | Purpose |
|---|---|
| `v1` | Current default branch. Clean v1.0.0 release line under `versions/v1/`. |

## Archived Branches

| Branch | Purpose |
|---|---|
| `archive/pre-v1-main-2026-04-10` | Previous default-branch history before the v1 reset. Preserved for audit and continuity. |
| `archive/go-rewrite-2026-04-10` | Previous Go rewrite experiment. Preserved for reference, not mixed into v1. |

## Policy

- New stable work should target `v1` until `v1.1` or a later line is opened.
- Archived branches are read-only history unless a specific recovery task requires them.
- Technical publishing branches should not be kept after their commits are reachable from a semantic branch or tag.
- The `v1.0.0` tag points to the first clean v1 release commit.
