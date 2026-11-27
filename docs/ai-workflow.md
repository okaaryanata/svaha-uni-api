# Using AI efficiently on these repositories

Open the correct repository as your workspace and start the agent there. Codex discovers the root `AGENTS.md` when the session starts. Ask it to list the loaded instruction files if unsure. A new chat does not require pasting the entire previous conversation: the repository, instructions, tests, and a short handoff are the source of truth.

For a task, provide the desired behavior, the observed problem, constraints, and acceptance checks. Example: “Fix unread preview after revoke. Preserve the wire contract and local-first ordering. Add a regression test, run make check, and commit separately.”

Use planning for architecture changes and migrations. For a small known bug, ask for a targeted fix directly. Choose a less expensive available model for routine edits; reserve a more capable model for concurrency, architecture, unfamiliar failures, or difficult reviews. Model availability and credit pricing depend on your app/account; these files do not change billing settings or select a model automatically.

Keep always-loaded rules concise. Put explanations in architecture docs and read them when relevant. Avoid asking for a full-repository audit on every small edit, repeated internet research on settled decisions, redundant full test runs, or multiple agents by default. Tests and linters catch mechanical errors without repeated AI review. A useful handoff records the current commit, changed behavior, validation results, and remaining work rather than an entire transcript.

For a new project, copy the appropriate AGENTS.md, adapt its architecture and commands, and add CI checks. A parent `personal/AGENTS.md` outside a child repository’s Git root is not a reliable substitute for a root instruction file. These changes intentionally apply only to the two approved repositories. Other coding agents may use different instruction discovery; consult their documentation rather than assuming universal enforcement.

References: [Codex instruction discovery](https://developers.openai.com/codex/guides/agents-md), [Go module organization](https://go.dev/doc/modules/layout), [Go review conventions](https://go.dev/wiki/CodeReviewComments).
