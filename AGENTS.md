# Agent Guide for Prometheus MCP Server

An MCP server that exposes a running Prometheus instance (or compatible backend, e.g. Thanos) to LLM clients as typed tools and resources, plus search over an embedded snapshot of the official Prometheus docs (Bleve-indexed at startup). [README.md](README.md) is the canonical reference for the tool list, install methods, auth, telemetry, and flags — when a change affects any of those, update the README rather than duplicating it here.

## Build, test, lint

Standard Prometheus tooling: `Makefile` includes `Makefile.common` and drives [`promu`](https://github.com/prometheus/promu); CI is [`prometheus/promci`](https://github.com/prometheus/promci) (`.github/workflows/ci.yml`). Release artifacts are built in CI, not locally.

| Task | Command |
|---|---|
| Tests | `make test` |
| Single test while iterating | `go test -race -run TestName ./pkg/mcp/` |
| Lint | `make lint` |
| Full pre-commit check (style, license headers, lint, yamllint, tidy, build, test) | `make` |
| Build host binary | `make build` |
| Refresh embedded docs snapshot | `make docs` |
| List project-specific targets | `make help` |

Things CI enforces that are easy to miss locally:

- **The docs embed.** `cmd/prometheus-mcp/main.go` declares `//go:embed all:external/docs`, and that directory is *gitignored* — `make docs` populates it from a pinned `prometheus/docs` tarball (`DOCS_VERSION` in the `Makefile`). On a fresh clone, raw `go build ./...` / `go test ./...` fails with a "no matching files" embed error; run any make build/test target once and raw `go` commands work from then on (package-scoped commands like `go test ./pkg/mcp/` don't need the snapshot at all).
- **License headers.** Every source file needs the Apache-2.0 header (`check_license` runs as part of bare `make`). Copy it from any existing `.go` file when creating a new one.
- **No drift.** CI runs `make` and then `git diff --exit-code` — un-tidied `go.mod`/`go.sum` or anything else the build regenerates must be committed.
- golangci-lint (version taken from `make print-golangci-lint-version`) and `govulncheck` also gate PRs.
- The Go version is pinned in three places — `go.mod`, `.promu.yml`, and the builder image in `ci.yml`. Bump them together.

## Architecture in brief

- `cmd/prometheus-mcp/main.go` — kingpin flag definitions (every flag is also settable via an auto-generated env var, via `DefaultEnvars()`), transport selection (`--mcp.transport=stdio|http`; the stdio loop lives *here*, HTTP mounts `mcp.NewStreamableHTTPHandler` at `/mcp`), the docs `go:embed`, and the exporter-toolkit web server (metrics, pprof, landing page, embedded docs at `/docs/`). Version info comes from `prometheus/common/version`, populated by promu ldflags.
- `pkg/mcp/` — the server proper. `ServerContainer` (`server.go`) is the DI struct every handler hangs off; tool definitions live in `tools.go`, input types in `types.go`, handlers in `handlers.go`, toolset composition in `registration.go`, MCP resources in `resources.go`, docs search in `docs.go`/`docs_updater.go`. The MCP instructions blob sent to clients is `pkg/mcp/assets/instructions.md`.
- **Backend toolsets** derive from the base Prometheus toolset: each backend has a `<backend>RemovedTools` list and an `init<Backend>Toolset` initializer that prunes unsupported tools and adds backend-specific ones (`initThanosToolset` is the canonical example). To add a backend, extend `PrometheusBackends`, add the removal list + initializer, and wire it into `getToolset`'s switch — don't fork a parallel map. `CoreTools` always loads even when `--mcp.tools` narrows the set; `PrometheusTsdbAdminTools` is the destructive set gated behind `--dangerous.enable-tsdb-admin-tools`.
- **Docs subsystem**: the Bleve index is built at startup from the embedded docs FS; `--docs.auto-update` swaps it at runtime, hence `docsMu sync.RWMutex` in `ServerContainer` — read under `RLock`, write under `Lock`.
- Supporting: `pkg/prometheus/` (API client builder, `UserAgent()`, time parsing), `internal/metrics/` (metrics registry + namespace).

## Adding a new tool

1. **Input type** in `types.go`: struct with `jsonschema` tags and a `LogValue() slog.Value` method so structured logs group fields cleanly. Reuse `TimeRangeInput`, `TruncatableInput` where applicable.
2. **Tool definition** in `tools.go`: `*mcp.Tool` with `Annotations.ReadOnlyHint` *or* `Annotations.DestructiveHint` (use the `ptr(true)` helper). Zero-input tools use `InputSchema: emptyInputSchema`, not the SDK's `EmptyInput` — OpenAI strict-schema workaround, see [#119](https://github.com/prometheus/prometheus-mcp/issues/119).
3. **Handler** method on `*ServerContainer` in `handlers.go`, signature `(ctx, req, input) (*mcp.CallToolResult, any, error)`. Get clients via `s.GetAPIClient(ctx)` — see the HTTP plumbing gotcha below; never reach for default client/transport fields directly.
4. **Register** in `initPrometheusToolset()` in `registration.go`; add the tool's name to `thanosRemovedTools` if Thanos doesn't support it. Leave `CoreTools` alone unless the tool must load even when `--mcp.tools` is narrowed.
5. **Test** in `handlers_test.go`: `api_mock_test.go` has the mock Prometheus, and `mcptest.NewTestServer` provides an in-memory client↔server MCP session. `registration_test.go` pins expected toolset contents — update its expectations whenever tools are added or removed.
6. Update the tool table in `README.md`.

## Gotchas

- **Time inputs** accept epoch seconds, RFC3339, *or* Go duration strings relative to now (`5m`, `1h30m`). Use `ParseTimestampOrDuration` (`pkg/prometheus`) / `parseTimeWithDefault` (`handlers.go`) — don't hand-parse.
- **HTTP plumbing.** `s.GetAPIClient(ctx)` returns both the prom Go client *and* the matching `http.RoundTripper`; `authContextMiddleware` makes the RoundTripper request-scoped when an `Authorization` header is present, so going around it bypasses per-request auth. For anything outside `promv1.API`, call `s.doHTTPRequest(ctx, method, rt, path, expectJSON)`, or `s.doManagementAPICall(ctx, method, path)` for `/-/...` endpoints — both share the connection pool, emit `target_path`-labelled telemetry, and surface 404s as `ErrEndpointNotSupported`. Reference implementations: `ThanosStoresHandler` (`/api/v1/...`), the management API handlers (`/-/...`).
- **TSDB admin tools** (`PrometheusTsdbAdminTools`) set `DestructiveHint` and only register when `--dangerous.enable-tsdb-admin-tools` is set; `delete_series` additionally requires both `start_time` and `end_time` to prevent accidental full-data deletion. Preserve both properties when touching these handlers.
- **Metrics** register through `metrics.Registry` with `prometheus.BuildFQName(metrics.MetricNamespace, ...)` — never the global registry.
- **`--mcp.enable-toon-output`** serializes tool results as TOON instead of JSON — don't hard-code JSON shape assumptions in end-to-end tests.

## Helm chart

`charts/prometheus-mcp-server` has its own CI (`.github/workflows/helm-chart.yaml`, chart-testing) that runs only when `charts/**` or `grafana/**` change; check locally with `make helm-lint` / `make helm-template`. `charts/prometheus-mcp-server/dashboards/` is generated and gitignored — the dashboard source of truth is `grafana/*.json`, synced in by `make helm-sync-dashboards`.
