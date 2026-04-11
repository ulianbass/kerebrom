# Kerebrom Factory

This repository is the factory for versioned Kerebrom builds, not the final
installed runtime layout seen by end users.

## Active line

- Current build line: `versions/v1/`
- v1 status: final v1.0.0 release line, with functional Engram-parity surface closed
- Runtime target: one installed `kerebrom` binary plus its local data dir

## How to work on v1

```bash
cd versions/v1
make build
make test
go run ./cmd/kerebrom version
```

## Factory layout

```text
versions/v1/            Source tree for the v1 line
```
