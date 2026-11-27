# Unified API engineering agreements

This repository is `github.com/okaaryanata/svaha-uni-api`. The sibling UI is `../oka-portfolio`.

- Start from this repository root. Read the README and relevant architecture notes before broad changes; inspect only the needed feature for routine fixes.
- Keep `cmd/api` minimal. `internal/app` wires configuration and lifecycle. Domain packages own pure models/rules; services own use cases and consumer interfaces; repositories own SQL; handlers own HTTP/WebSocket transport; clients own external HTTP integration.
- Follow dependency direction: handlers/adapters depend on service/domain contracts. Services never import concrete repositories or network transports. Prefer small consumer interfaces, constructor injection, and explicit errors over speculative generic frameworks.
- Keep business decisions outside SQL adapters. Use transaction ports for atomic operations; propagate commit errors and roll back failures. Preserve mutation + command-outcome atomicity and publish only after commit.
- Preserve existing HTTP/WebSocket responses, permission rules, idempotency, database migrations, and privacy/receipt semantics unless the task explicitly changes them.
- Use contexts, bounded work, and deterministic resource cleanup. Check meaningful errors, including row iteration, writes, and commits. Do not silence linters broadly; document any narrow upstream false positive.
- Add meaningful domain/service tests and real SQLite/transport regression tests where appropriate. Run focused tests while iterating; before completion run `make check`. Do not claim unrun or blocked checks passed.
- Make incremental conventional commits with the existing Git identity. Preserve unrelated changes; do not rewrite history or push without instruction.
- Keep instructions short, use targeted reads, and reuse verified findings. Use one agent unless delegation is explicitly requested. Refer to `docs/ai-workflow.md` for efficient handoffs.
