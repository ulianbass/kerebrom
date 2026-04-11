# Repository History

Kerebrom keeps previous implementation lines available for audit while making `v1` the only active public branch.

## Timeline

| Order | Ref | Type | Purpose |
|---:|---|---|---|
| 1 | `history/legacy-main-2026-04-10` | tag | Legacy default-branch history before the v1 reset. Preserved for audit and continuity. |
| 2 | `history/go-rewrite-2026-04-10` | tag | Previous Go rewrite experiment. Preserved for reference, not mixed into v1. |
| 3 | `v1` | branch | Current default branch. Stable v1.0.0 product line under `versions/v1/`. |

## Policy

- New stable work should target `v1` until `v1.1` or a later line is opened.
- `main` should not be reused for legacy history because GitHub users expect `main` to be the active line.
- Historical implementations should be tags, not branches, so GitHub does not present them as active PR candidates.
- Technical publishing branches should not be kept after their commits are reachable from a semantic branch or tag.
- The `v1.0.0` tag points to the first clean v1 release commit.
