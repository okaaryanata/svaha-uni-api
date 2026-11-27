# Architecture and dependency rules

This is a small multi-feature service, organized by technical role and then feature. Go prescribes neither a universal “clean architecture” nor a mandatory service/repository framework. We use `cmd` and `internal`, explicit constructors, small consumer-owned interfaces, and focused feature packages.

`cmd/api` calls the application lifecycle. `internal/app` owns dependency wiring, HTTP configuration, shutdown, and pruning. `internal/domain/chat` contains application models, errors, and conversation projection; `internal/domain/sudoku` contains the cancellation-aware solver. `internal/service` owns use cases. `internal/repository/sqlite` implements chat transactions and migrations; `internal/client/cataas` handles external HTTP and provider response validation. HTTP/WebSocket handlers own decoding, wire responses, connections, rate limits, presence, and ordered fanout.

Dependencies point toward domain and service ports. Services do not import `database/sql`, handlers, or concrete repositories. Depguard checks these boundaries. No empty repository layer is created for features without persistence. No generic base repository, service locator, ORM, or public `pkg` tree is needed.

The chat service owns validation, authorization, retention policy, and command replay policy. A repository transaction callback exposes the required persistence operations without leaking `sql.Tx`. A savepoint rolls back rejected mutations before storing their permanent rejection. Infrastructure errors roll back the entire transaction and remain retryable. Commit failures propagate. The transport publishes snapshots and acknowledgments only after the transaction returns successfully. SQLite serializes operations and snapshots so no partial state is visible.

Existing JSON field names, command fingerprint serialization, revisions, migrations, and ordering are compatibility constraints. Compact serializable application command/result models remain stable so already-persisted command outcomes and queued clients continue working. HTTP-only response envelopes stay in transport code. Do not “simplify” the fingerprint serialization without a migration and compatibility tests.

Compared with `loan` and `user-svc`, we retain their understandable handler/service/repository roles. We avoid carrying concrete database connections, concrete repositories, and HTTP status handling into business services. This is a focused adaptation, not a copy of those repositories. Neither reference repository was modified.

Required checks: `make check`. GolangCI-Lint v2.15.0 is installed repository-locally by Make; formatting checks use gofmt/goimports. CI and the container build use Go 1.26; the module retains its existing Go 1.25 language baseline. The WebSocket library owns handshake response-body cleanup, so its specific dial sites carry documented bodyclose exclusions.

Sources: [official module layout](https://go.dev/doc/modules/layout), [Go review conventions and interfaces](https://go.dev/wiki/CodeReviewComments#interfaces), [GolangCI-Lint configuration](https://golangci-lint.run/docs/configuration/file/).
