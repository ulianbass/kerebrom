# Repository History

Kerebrom keeps previous implementation lines available for audit while making `v1` the only active public branch.

## Active Branch

| Branch | Purpose |
|---|---|
| `v1` | Current default branch. Clean v1.0.0 release line under `versions/v1/`. |

## History Tags

| Tag | Purpose |
|---|---|
| `history/pre-v1-main-2026-04-10` | Previous default-branch history before the v1 reset. Preserved for audit and continuity. |
| `history/go-rewrite-2026-04-10` | Previous Go rewrite experiment. Preserved for reference, not mixed into v1. |

## Policy

- New stable work should target `v1` until `v1.1` or a later line is opened.
- Historical implementations should be tags, not branches, so GitHub does not present them as active PR candidates.
- Technical publishing branches should not be kept after their commits are reachable from a semantic branch or tag.
- The `v1.0.0` tag points to the first clean v1 release commit.
