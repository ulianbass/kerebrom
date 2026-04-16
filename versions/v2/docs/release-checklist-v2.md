# Kerebrom v2 release checklist

Run through this list before tagging a new v2.x release.

## 1. Code is clean

- [ ] `go build ./...` succeeds with no warnings.
- [ ] `go test ./...` reports `ok` for every package.
- [ ] No new external dependencies in `go.mod` (check `git diff go.mod`).
- [ ] No leftover `mem_*` references in user-visible strings:
  ```bash
  grep -rn 'mem_' versions/v2/ --include='*.go' --include='*.md' \
    | grep -v 'memory\|memories\|moremem\|remember'
  ```
  Should print nothing.

## 2. Manifest is current

- [ ] `versions/v2/manifest.json` `semver` matches `versions/v2/internal/version/version.go` `Version`.
- [ ] `mcp_tools` array lists exactly the seven tools registered in `internal/transport/mcp/server.go`.
- [ ] `mcp_tool_count` matches the array length.
- [ ] `status` describes the focus of this release (kebab-style with underscores).

## 3. READMEs reflect reality

- [ ] Root `README.md` and `README.es.md` show the latest release link.
- [ ] `versions/v2/README.md` and `README.es.md` describe install, update, and the cycle.
- [ ] `docs/migration-v1-to-v2.md` is accurate for any new behavior added since the previous v2.x release.

## 4. Smoke test on a clean machine

If possible, on a fresh user account or VM:

- [ ] Clone the repo, run `make install-user` from `versions/v2/`.
- [ ] Restart Claude Desktop. Open a fresh chat. Ask "what do you know about my projects?".
- [ ] Verify in `~/Library/Logs/Claude/mcp-server-Kerebrom.log` (macOS) that a `tools/call` for `context` fires immediately after the user message.
- [ ] Verify `~/.claude/settings.json` contains the seven `mcp__Kerebrom__*` entries in `permissions.allow` and **no** `mcp__Kerebrom__mem_*` entries.

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
