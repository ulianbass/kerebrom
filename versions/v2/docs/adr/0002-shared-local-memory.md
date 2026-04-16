# ADR 0002: Shared Local Memory as the v1 Source of Truth

## Status

Accepted

## Context

Kerebrom must let Codex, Claude Code, Cursor, Claude Desktop, and other supported local clients benefit from the same memory corpus. Per-agent databases would break the product promise.

## Decision

Kerebrom v1 will use one local canonical store per user and retrieve from it with project-aware filtering.

The store contract will include:
- one physical database under the user's Kerebrom data directory
- logical segmentation by project, session, scope, topic, and origin metadata
- local-only integrations for v1
- future adapters may be added when a client exposes local MCP or equivalent local extension hooks

## Consequences

- Cross-agent recall becomes a first-order behavior, not an optional sync path.
- Context quality depends on good normalization and filtering, not on separate stores.
- Remote-only products such as ChatGPT custom remote MCP apps stay out of v1 scope.

