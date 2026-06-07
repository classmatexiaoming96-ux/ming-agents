# Self-Evolving Memory System

A small, file-backed memory store for agents. Incoming notes are scored, filed
into an Obsidian-style vault, retrieved with simple filters, reinforced by usage
feedback, and archived when they expire.

This package is the Go rewrite of the original `memory_api.py`. The Python files
are retained alongside it for reference and gradual migration.

## Layout

```
server/memory/
├── memory.go          # Go core API (Ingest / Recall / Feedback / Cleanup / Stats)
├── memory_test.go     # unit tests
├── memory_api.py      # legacy Python core (kept for reference)
├── memory_cli.py      # legacy Python CLI (kept for reference)
├── cmd/
│   └── memory-cli/    # Go CLI: add / search / list / feedback / cleanup / stats
│       └── main.go
└── MEMORY.md          # this document
```

## The vault

Memories are **not** stored in the repository. They live in an external vault so
they can be shared across tools (including Obsidian and the legacy Python CLI):

```
$HOME/.hermes/vault/
├── notes/{project}/{id}.md   # accepted memories (score >= 3.0)
├── inbox/{id}.md             # below-threshold memories awaiting review
└── archive/{project}/{id}.md # expired / archived memories
```

The path is resolved with `os.ExpandEnv("$HOME/.hermes/vault")`, so it works for
any user without a hard-coded home directory. Tests override the package-level
`VaultDir` variable to point at a temp directory. (No symlink is committed into
the repo, since an absolute-path symlink would not be portable across machines.)

Each memory is a markdown file: a YAML frontmatter block followed by the body.

```markdown
---
id: mem_ab12cd34
type: decision
project: ming-agents
tags: [db, pool]
title: ...
score: 4.6
novelty: 1
specificity: 0.8
reusability: 0.7
hit_count: 0
created_at: 2026-06-08
expires_at: 9999-12-31
status: active
source: manual
links: []
---
<body>
```

## Core API (`memory.go`)

| Function | Purpose |
| --- | --- |
| `Ingest(content, type, project, tags, source, title)` | Score content and write to `notes/` (accepted) or `inbox/` (below threshold). Empty fields are auto-classified. |
| `Recall(query, project, type, tags, minScore, status, limit)` | Filter active memories and return them sorted by score, highest first. |
| `Feedback(id, used, helpful)` | Increment `hit_count`; nudge score (`used` +0.05, `helpful` +0.1). |
| `Cleanup()` | Move expired active memories into `archive/{project}` and flip status to `archived`. |
| `Stats()` | Return total / active / archived counts and a by-type breakdown. |

## Scoring

The score (0–5, threshold 3.0) is a weighted blend, matching the Python version:

```
score = round( (0.3*novelty + 0.3*specificity + 0.25*reusability + 0.15*source) * 5, 1 )
```

- **novelty** — `1 - maxJaccard(content, existing active memories)`; an empty
  vault yields `1.0`. A near-duplicate scores near `0`.
- **specificity** — `0.5` base, `+0.1` for a 4+ digit number, `+0.15` for code
  (`` ` `` / ```` ``` ````), `+0.15` for a causal word (因为/所以/决定/原因/why/because); capped at `1.0`.
- **reusability** — `0.5 + len(tags)/10`, capped at `1.0`.
- **source** — manual `1.0`, code-review `0.9`, debug-session `0.8`,
  agent-run `0.7`, meeting `0.6`, otherwise `0.5`.

## TTL by type

| type | TTL (days) |
| --- | --- |
| decision | ∞ (permanent) |
| gotcha | ∞ (permanent) |
| incident | 365 |
| requirement | 180 |
| snippet | 90 |
| meeting | 30 |
| agent-trace | 7 |

Permanent memories use the sentinel `expires_at: 9999-12-31`.

## CLI

```bash
# run from server/ directory
go build -o memory-cli ./memory/cmd/memory-cli

memory-cli add "决定采用 connection pooling because 高并发" --type decision --project ming-agents --tags db,pool
memory-cli search --query pooling
memory-cli list --type decision
memory-cli feedback mem_ab12cd34 --helpful
memory-cli cleanup
memory-cli stats
```

Flags may appear before or after positional arguments.

## Tests

```bash
go test ./server/memory/...
```

Covers ingest scoring/placement, novelty drop on duplicates, recall
filtering/sorting, feedback increments, expiry archival, and frontmatter
round-tripping. Tests use a temp vault and a pinned clock for determinism.
