# Kerebrom Copilot Instructions

Kerebrom's current product line is `versions/v2`.

If a user asks to install this repository, follow `docs/AI_AGENT_INSTALL.md` and treat the task as an end-user product install:

```bash
cd versions/v2
make install-user
```

Verify with:

```bash
kerebrom version
kerebrom stats
```

Do not enable remote `mcp-http`, delete memory, import backups, or modify unrelated AI-client settings unless the user explicitly asks. Kerebrom is local-first by default and stores runtime memory in `~/.kerebrom/`.
