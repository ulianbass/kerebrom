# ADR 0001: Practical Clean-Room Reimplementation

## Status

Accepted

## Context

Kerebrom v1 needs to match the product behavior of Engram without inheriting the codebase as a disguised fork. The goal is product parity, not source lineage.

## Decision

Kerebrom v1 will be implemented as a practical clean-room rebuild:
- behavior and public docs may inform the specification
- code is authored in this repository from scratch
- copied artifacts are not allowed in the main product tree unless explicitly isolated with provenance and notice
- migration data from the previous Kerebrom install is quarantined in local staging and is not product source

## Consequences

- The repo starts cleaner and is easier to reason about legally and technically.
- Early implementation is slower than a direct fork but far safer for long-term ownership.
- Product parity must be enforced with contract tests and behavior specs, not by comparing source files.

