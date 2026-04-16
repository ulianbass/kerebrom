# Repository History

Kerebrom keeps previous implementation lines available for audit while making `v2` the active development branch and `v1` a maintained line.

## Timeline

| Order | Ref | Type | Purpose |
|---:|---|---|---|
| 1 | `history/legacy-main-2026-04-10` | tag | Legacy default-branch history before the v1 reset. Preserved for audit and continuity. |
| 2 | `history/go-rewrite-2026-04-10` | tag | Previous Go rewrite experiment. Preserved for reference, not mixed into v1 or v2. |
| 3 | `v1` | branch | Stable v1 product line under `versions/v1/`. Maintained for users who depend on the `mem_*` MCP tool surface. |
| 4 | `v2` | branch | Current active branch. Clean semantic MCP surface (seven verb-named tools), self-update command, plug-and-play in MCP-only clients. Lives under `versions/v2/`. |

## Policy

- New stable work targets `v2` from v2.0.0 onward.
- `v1` keeps receiving critical fixes but no new features. Releases on `v1` use the `v1.x` tag prefix.
- `main` is not reused for legacy history because GitHub users expect `main` to be the active line. The default branch stays on the most recent stable line.
- Historical implementations remain as tags, not branches, so GitHub does not present them as active PR candidates.
- The `v1.0.0` tag points to the first clean v1 release commit.
- The `v2.0.0` tag points to the first v2 release commit.
- The latest public release tag remains the release anchor; the active branch may include verified post-release fixes while changes are batched for the next release.
