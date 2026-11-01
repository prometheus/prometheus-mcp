# Changelog

## 0.19.0-rc.0 / 2026-11-01

This is the first release under the Prometheus org! See the changelog below for details on the renamed binaries, environment variables, images, and packages. See the README for up to date documentation.

* [CHANGE] Move the project to `github.com/prometheus/prometheus-mcp`; the Go module path is now `github.com/prometheus/prometheus-mcp`. #144
* [CHANGE] Rename the binary from `prometheus-mcp-server` to `prometheus-mcp`. #144 #145
* [CHANGE] Rename flag env vars from `PROMETHEUS_MCP_SERVER_*` to `PROMETHEUS_MCP_*`, following the binary rename; old variables are silently ignored. #144 #145
* [CHANGE] Publish container images to `docker.io/prom/prometheus-mcp`, `quay.io/prometheus/prometheus-mcp`, and `ghcr.io/prometheus/prometheus-mcp`; `ghcr.io/tjhop/prometheus-mcp-server` receives no new releases. #145
* [CHANGE] Move the binary inside the container image from `/usr/bin/prometheus-mcp-server` to `/bin/prometheus-mcp`. #145
* [CHANGE] Build and release with promu/promci instead of goreleaser: deb, rpm, and Arch Linux packages are no longer published, and macOS universal binaries are replaced by per-architecture darwin tarballs. #145
* [CHANGE] Rename the bundled systemd unit to `prometheus-mcp.service` and run it with `--mcp.transport=http`. #144 #198
* [CHANGE] helm: stop publishing the chart to `oci://ghcr.io/tjhop/charts`; the chart is moving to prometheus-community/helm-charts. #145
* [CHANGE] Publish to the MCP registry as `io.github.prometheus/prometheus-mcp`, pointing at `docker.io/prom/prometheus-mcp`. #160
* [CHANGE] mcp: serve streamable HTTP statelessly and remove stateful HTTP; legacy clients that send `initialize` are still served. #191
* [CHANGE] mcp: deprecate `--mcp.session-timeout`; it is ignored and logs a warning when set. #191
* [CHANGE] mcp: `--mcp.keepalive-interval` applies only to the stdio transport; keepalive pings are disabled on HTTP. #191
* [CHANGE] mcp: rewrite and condense the server instructions, rendered from a template to describe only the enabled features. #158
* [CHANGE] web: `prom_mcp_server_ready` tracks transport readiness instead of being set on the first client `initialize`. #173
* [CHANGE] Exit at startup when the `--http.config` file fails to load instead of continuing without it. #190
* [CHANGE] thanos: remove the TSDB admin tools and `targets_metadata` from the Thanos toolset, since Thanos does not serve those endpoints. #137 #196
* [FEATURE] mcp: support MCP protocol revision `2026-07-28` (no `initialize`, per-request `_meta`, `server/discover`) on all transports via go-sdk v1.8.0. #191
* [FEATURE] mcp: cancel in-flight Prometheus API calls when a `2026-07-28` HTTP client disconnects mid-call. #191
* [FEATURE] mcp: add guided runbooks, exposed as `skill://<name>/SKILL.md` resources, prompts, and the new core `runbooks_list` and `runbooks_read` tools. #158
* [FEATURE] web: add `/-/healthy` and `/-/ready` endpoints; `/-/ready` returns 503 until the MCP transport is up and during shutdown. #173
* [FEATURE] Accept Grafana-style relative times (`now`, `now-1h`, `now+1h`) and negative durations (`-5m`) in time inputs. #187
* [ENHANCEMENT] Build for many more platforms, including the BSDs, 386, ppc64/ppc64le, s390x, mips64, arm, and riscv64. #145 #195
* [ENHANCEMENT] Publish container images for linux/armv7, ppc64le, riscv64, and s390x in addition to amd64 and arm64. #145 #195
* [ENHANCEMENT] mcp: log client name, version, and protocol version from `server/discover` in the telemetry middleware. #191
* [ENHANCEMENT] mcp: floor the go-sdk's own logs at warn unless `--log.level=debug`, to suppress per-request session bookkeeping noise under stateless HTTP. #191
* [ENHANCEMENT] mcp: do not count or log client-canceled tool calls as failures; log the cancellation cause instead. #191
* [ENHANCEMENT] web: raise the HTTP server idle timeout to 120s. #191
* [ENHANCEMENT] Update the embedded Prometheus docs snapshot.
* [BUGFIX] mcp: resolve the forwarded `Authorization` header per request instead of pinning the value from session establishment. #174
* [BUGFIX] mcp: log the tool error text for failed tool calls instead of an unreadable result dump and an empty error. #181
* [BUGFIX] mcp: fix a panic on requests carrying a forwarded `Authorization` header when no HTTP round tripper was configured, e.g. after `--http.config` failed to load. #190
* [BUGFIX] mcp: only warn about `metric_metadata`/`targets_metadata` truncation when the limit was actually reached. #188
* [BUGFIX] mcp: serve string slice inputs as plain `array` types in tool schemas, fixing 400s from Gemini and other single-type schema dialects. #189
* [BUGFIX] mcp: deliver client log notifications on the request that produced them, so HTTP clients and `2026-07-28` clients receive them. #191
* [BUGFIX] mcp: stop dropping the completion message of every client log start/finish pair. #191
* [BUGFIX] mcp: stop advertising `listChanged` for tools, prompts, and resources, which never change at runtime. #191
* [BUGFIX] web: serve metrics on the path given by `--web.telemetry-path` instead of hardcoding `/metrics`; custom telemetry paths no longer 404. #176
* [BUGFIX] The systemd unit no longer exits immediately after start; it ran the stdio transport with stdin attached to `/dev/null`. #198
