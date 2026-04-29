# Kerebrom v2 release checklist

Run through this list before tagging a new v2.x release.

## 1. Code is clean

- [ ] `go build ./...` succeeds with no warnings.
- [ ] `go test ./...` reports `ok` for every package.
- [ ] `go vet ./...` succeeds.
- [ ] `kerebrom doctor --deep` reports no FAIL after installing the release candidate.
- [ ] Any `go.mod` or `go.sum` change is deliberate: review the diff, record dependency scope in release notes, run `go mod verify`, and run `govulncheck ./...`.
- [ ] No active v2 surface exposes legacy `mem_*` tools. Intentional references are allowed only in migration/ADR docs, cleanup tests, or explicit v1 compatibility text:
  ```bash
  rg -n 'mcp__Kerebrom__mem_|\bmem_(bootstrap|context|save|save_prompt|search|session_start|session_end|session_summary|capture_passive|get_observation|stats|suggest_topic_key|update|delete|timeline|merge_projects)\b' \
    versions/v2/internal versions/v2/cmd \
    --glob '*.go' \
    --glob '!versions/v2/internal/setup/setup_test.go'
  ```
  Review any remaining hit; it must not be a registered v2 tool or an active setup instruction.

## 2. Manifest is current

- [ ] `versions/v2/manifest.json` `semver` matches `versions/v2/internal/version/version.go` `Version`.
- [ ] `mcp_tools` array lists exactly the seven tools registered in `internal/transport/mcp/server.go`.
- [ ] `mcp_tool_count` matches the array length.
- [ ] `status` describes the focus of this release (kebab-style with underscores).

## 3. READMEs reflect reality

- [ ] Root `README.md` and `README.es.md` show the latest release link.
- [ ] `README.md`, `README.es.md`, `versions/v2/README.md`, and `versions/v2/README.es.md` describe install, update, and the cycle.
- [ ] `AGENTS.md`, `CLAUDE.md`, and `docs/AI_AGENT_INSTALL.md` match the current setup behavior.
- [ ] GitHub About description/topics still describe the current released product line.
- [ ] `docs/migration-v1-to-v2.md` is accurate for any new behavior added since the previous v2.x release.

## 4. Smoke test on a clean machine

If possible, on a fresh user account or VM:

- [ ] Clone the repo, run `make install-user` from `versions/v2/`.
- [ ] Restart Claude Desktop. Open a fresh chat. Ask "what do you know about my projects?".
- [ ] Verify in `~/Library/Logs/Claude/mcp-server-Kerebrom.log` (macOS) that a `tools/call` for `context` fires immediately after the user message.
- [ ] Verify `~/.claude/settings.json` contains the six default agent-profile `mcp__Kerebrom__*` entries in `permissions.allow` and **no** stale `mcp__Kerebrom__mem_*` or admin-profile entries.

## 5. Self-update works

- [ ] `kerebrom update --check` prints "already up to date" when run against the latest tag.
- [ ] On a machine running an older v2.x, `kerebrom update --yes` upgrades cleanly and the new binary reports the new version.

## 6. Git hygiene

- [ ] `git status` is clean before tagging.
- [ ] Commit message follows the repository convention: `release: Kerebrom vX.Y.Z` for release commits, `feat:`/`fix:`/`docs:` for everything else.
- [ ] Tag is annotated: `git tag -a vX.Y.Z -m "Kerebrom vX.Y.Z"`.
- [ ] Push branch and tag: `git push origin v2 && git push origin vX.Y.Z`.

## 7. GitHub Release

- [ ] `gh release create vX.Y.Z --title "Kerebrom vX.Y.Z" --notes "$(cat release-notes.md)"`.
- [ ] Release notes cover: what's new, what changed, how to upgrade.
- [ ] Verify the release shows as "Latest" on the GitHub repository page.

## 8. Post-release

- [ ] Save a Kerebrom observation describing the release, the commit, and any decisions worth carrying forward.
- [ ] If any item from the smoke test failed, file an issue and fix in a patch release before announcing the version more broadly.
