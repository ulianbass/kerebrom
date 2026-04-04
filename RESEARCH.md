# Research Notes

## Motivation

Every AI coding tool today — Claude Code, Codex, Cursor, Copilot — suffers from the same structural flaw: session amnesia. Each new conversation starts from zero. The AI does not remember who you are, what you decided yesterday, or what patterns your project follows.

This is not a limitation of the models themselves — it is an infrastructure gap. The Model Context Protocol (MCP) provides a standardized way for LLMs to interact with external tools. Kerebrom explores whether a local-first, SQLite-backed memory engine can fill this gap effectively: giving AI tools persistent, shared, privacy-respecting memory without cloud dependencies.

## Open Questions This Project Explores

### 1. Retrieval Quality Without Neural Embeddings

Can a memory system achieve useful recall accuracy using deterministic feature hashing instead of neural embeddings? Neural models (sentence-transformers, ONNX) offer better semantic similarity but add 200MB+ of dependencies and cold-start latency.

This project's approach: start with deterministic hash embeddings that work offline and require zero dependencies, then measure where retrieval quality degrades. The hybrid scoring system (keyword + semantic + recency + access frequency + entity graph) compensates for weaker embeddings by combining multiple weak signals.

### 2. Memory Lifecycle Management

Human memory decays, consolidates, and reorganizes over time. Should AI memory do the same? A system that never forgets becomes noisy — a system that forgets too aggressively loses valuable context.

This project implements a multi-tier lifecycle: episodic memories (events, sessions) decay and eventually consolidate into semantic memories (facts, patterns). Core memories (identity, preferences) never decay. The decay function uses importance, access frequency, and age to determine what fades.

### 3. Contradiction Detection in Knowledge Graphs

When a user says "I prefer Rust" in March and "I'm switching to Go" in April, the system must invalidate the old fact without being told explicitly. Entity extraction builds a graph of `(subject, predicate, object)` triples, and new facts automatically invalidate conflicting older ones.

The open question: how aggressive should contradiction detection be? Too aggressive and it invalidates valid nuance ("I prefer Rust for CLI tools but Go for servers"). Too conservative and stale facts persist. The current approach requires exact predicate matches for invalidation.

### 4. Privacy at the Storage Layer

AI memory systems inherently store sensitive information — names, locations, credentials, technical decisions. Unlike cloud-based memory services, a local-first approach keeps data on the user's machine. But local storage still needs protection.

This project implements PII scrubbing before storage (patterns for API keys, tokens, passwords, SSNs), optional encrypted-at-rest mode (AES-256 via system openssl), and no network communication whatsoever. The tradeoff: no cross-device sync, no backup to cloud, no sharing between machines.

### 5. Multi-Tool Memory Sharing

If a user works in Claude Code in the morning and Codex in the afternoon, both tools should share the same memory. But each tool has different configuration formats (JSON, TOML), different instruction systems (CLAUDE.md, AGENTS.md), and different permission models.

This project solves it with a single MCP server that all tools connect to, plus an auto-setup system that detects installed tools and configures each one idiomatically. The open question: how do you handle concurrent writes from multiple tools accessing the same SQLite database?

### 6. Progressive Disclosure for Context Windows

An AI with access to 800+ memories cannot load all of them into a single prompt. How should the system decide what to surface?

This project implements three disclosure layers: Layer 1 (~50 tokens, compact index), Layer 2 (~200 tokens, chronological summary), Layer 3 (500+ tokens, full detail). The agent requests the layer appropriate for its task. Additionally, the hybrid scoring system ranks memories by relevance before any are surfaced.

### 7. Automated Maintenance Without User Intervention

Memory systems accumulate noise over time — duplicate entries, stale observations, contradicted facts, low-value captures from hooks. Manual curation does not scale.

This project's approach: a scheduled maintenance agent (Sopor Plenus) that periodically reads session transcripts, extracts valuable information, deduplicates against existing memories, detects contradictions, and generates health reports. The agent runs autonomously on a schedule with no user interaction required.

## Findings So Far

### Hybrid Scoring Compensates for Weak Embeddings

Deterministic hash embeddings alone produce mediocre recall. But combining them with FTS5 keyword search, entity graph overlap, recency weighting, and access frequency creates a retrieval system that is surprisingly effective in practice. The entity graph is the strongest signal for identity-related queries ("who am I", "what project am I working on").

### Auto-Setup is Critical for Adoption

Users will not manually edit JSON configs, TOML files, and markdown instructions for three different AI tools. The `kerebrom setup` command that detects and configures everything in one step is the single most important feature for real-world usage. Without it, the system would remain a developer curiosity.

### Memory Decay Prevents Noise Accumulation

Without decay, the system accumulates hundreds of low-value episodic memories from hook captures. With decay, old low-importance memories fade naturally, keeping the active memory pool relevant. The consolidation step (merging related episodic memories into semantic facts) further reduces noise while preserving knowledge.

### SQLite WAL Mode Handles Concurrent Access Well

Multiple AI tools reading and writing to the same SQLite database via WAL mode has worked reliably in practice. The MCP server handles concurrent requests without corruption or locking issues. The main limitation is that only one tool should run maintenance operations at a time.

### Transcript Consolidation Captures What Hooks Miss

Passive hooks (PostToolUse, Stop) capture limited context. Reading full session transcripts after the fact captures decisions, preferences, and architectural choices that no single tool call reveals. The combination of real-time hooks plus periodic transcript consolidation provides comprehensive coverage.

## Limitations

- Retrieval quality is bounded by hash embedding similarity — neural embeddings would improve semantic matching
- Entity extraction uses heuristic patterns, not NER models — misses some entities and relationships
- Single-machine only — no cross-device sync or cloud backup
- Encrypted mode uses a temporary plaintext file during active use
- Maintenance agent quality depends on the LLM running it
- No formal evaluation benchmark — findings are observational from daily use

## Related Work

- **Model Context Protocol** — Anthropic (2024). The protocol Kerebrom implements for LLM-tool communication.
- **MemGPT: Towards LLMs as Operating Systems** — Packer et al. (2023). Explores virtual memory management for LLMs with tiered storage and retrieval.
- **Reflexion: Language Agents with Verbal Reinforcement Learning** — Shinn et al. (2023). Self-reflective agents that learn from past experiences stored in memory.
- **Generative Agents: Interactive Simulacra of Human Behavior** — Park et al. (2023). Simulated agents with memory retrieval, reflection, and planning — closest academic analog to Kerebrom's architecture.
- **RAPTOR: Recursive Abstractive Processing for Tree-Organized Retrieval** — Sarthi et al. (2024). Hierarchical summarization for retrieval — related to progressive disclosure layers.
- **Toolformer: Language Models Can Teach Themselves to Use Tools** — Schick et al. (2023). Foundational work on LLMs learning to use external tools.
