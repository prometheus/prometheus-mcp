# Prometheus MCP Server

[![license](https://img.shields.io/github/license/tjhop/prometheus-mcp-server)](https://github.com/tjhop/prometheus-mcp-server/blob/master/LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/tjhop/prometheus-mcp-server)](https://goreportcard.com/report/github.com/tjhop/prometheus-mcp-server)
[![golangci-lint](https://github.com/tjhop/prometheus-mcp-server/actions/workflows/golangci-lint.yml/badge.svg)](https://github.com/tjhop/prometheus-mcp-server/actions/workflows/golangci-lint.yml)
[![Latest Release](https://img.shields.io/github/v/release/tjhop/prometheus-mcp-server)](https://github.com/tjhop/prometheus-mcp-server/releases/latest)
[![GitHub Downloads (all assets, all releases)](https://img.shields.io/github/downloads/tjhop/prometheus-mcp-server/total)](https://github.com/tjhop/prometheus-mcp-server/releases/latest)

![Prometheus MCP Server Logo](images/logo-small.png)

## About
This is an [MCP](https://modelcontextprotocol.io/introduction) server to allow LLMs to interact with a running [Prometheus](https://prometheus.io/) instance via the API to do things like generate and execute promql queries, list and analyze metrics, etc.

### Demos and Examples

#### Asking Claude to Investigate Slow Queries

The prompt used was:
> querying my metrics is slow, can you help me figure out why? 

[![Demo prompt to ask claude to investigate slow queries](images/claude_high_cardinality.gif)](https://asciinema.org/a/8KxZgJxrXXX2zjwwDRSP4UtqL)

#### Investigate metrics produced by the MCP server itself and suggest recording rules for SLOs

The prompt used was:
> use the tools from the prometheus mcp server to investigate the metrics from
> the mcp server and suggest prometheus recording rules for SLOs

[![Demo prompt to investigate metrics produced by the MCP server and create rules for SLOs](images/gemini_slo.gif)](https://asciinema.org/a/av3WhfD122A1HHOq2d4SEZgMn)

#### Summarize Prometheus metric/label naming best practices

The prompt used was:
> summarize prometheus metric/label name best practices

[![Demo prompt to summarize prometheus metric and label name best practies](images/gemini_docs_search.gif)](https://asciinema.org/a/o9RKpCXBmBmG4zqhuFsvTHf2C)

#### Report on the health of the Prometheus instance that powers prometheus.demo.prometheus.io

The prompt used was:
> please provide a comprehensive review and summary of the prometheus server.
> review it's configuration, flags, runtime/build info, and anything else that
> you feel may provide insight into the status of the prometheus instance,
> including analyzing metrics and executing queries

[![Demo prompt to review the health of the demo.prometheus.io prometheus instance](images/demo-usage-with-prometheus-demo-server.gif)](https://asciinema.org/a/733513)

### Tools

The Prometheus HTTP API outputs JSON data, and the tools in this MCP server return that JSON to the LLM for processing as it's structured and well understood by LLMs.

#### LLMs and Token/Context Efficiency

This MCP server supports the following options which have the potential to reduce token/context usage:

##### TOON Encoding

If token/context usage is a concern, this MCP server also supports converting the API's JSON data to the [Token-Oriented Object Notation (TOON) format](https://github.com/toon-format/toon).
While it is not guaranteed to reduce token usage, it is designed with token efficiency in mind.
As noted on TOON's documentation, it excels at uniform arrays of objects; non-uniform/complex objects may still be more token-efficient in JSON.
Real world token usage will depend on usage patterns, please review common workflows to determine if TOON output may be beneficial.
Please see [Flags](#command-line-flags) for more information on the available flags and their corresponding environment variables.

##### API Response Truncation

This feature allows you to set a maximum limit on the number of lines or entries returned from the Prometheus API for, which can help in reducing the amount of data sent to the LLM.
Setting the limit to `0` disables truncation.
Truncation is disabled by default.
Note that LLMs capable of handling tool request arguments can override this global truncation limit on a per-tool-call basis for supported tools.
Please see [Flags](#command-line-flags) for more information on the available flags and their corresponding environment variables.

#### Full Tool List

| Tool Name | Description |
| --- | --- |
| `alertmanagers` | Get overview of Prometheus Alertmanager discovery |
| `build_info` | Get Prometheus build information |
| `config` | Get Prometheus configuration |
| `docs_list` | List of Official Prometheus Documentation Files |
| `docs_read` | Read the named markdown file containing official Prometheus documentation from the prometheus/docs repo |
| `docs_search` | Search the markdown files containing official Prometheus documentation from the prometheus/docs repo |
| `exemplar_query` | Performs a query for exemplars by the given query and time range |
| `flags` | Get runtime flags |
| `healthy` | Management API endpoint that can be used to check Prometheus health |
| `label_names` | Returns the unique label names present in the block in sorted order by given time range and matchers |
| `label_values` | Performs a query for the values of the given label, time range and matchers |
| `list_alerts` | List all active alerts |
| `list_rules` | List all alerting and recording rules that are loaded |
| `list_targets` | Get overview of Prometheus target discovery |
| `metric_metadata` | Returns metadata about metrics currently scraped by the metric name | 
| `query` | Execute an instant query against the Prometheus datasource, returning one value per series at a single point in time |
| `quit` | Management API endpoint that can be used to trigger a graceful shutdown of Prometheus |
| `range_query` | Execute a range query against the Prometheus datasource, returning values over a time window |
| `ready` | Management API endpoint that can be used to check Prometheus is ready to serve traffic (i.e. respond to queries |
| `reload` | Management API endpoint that can be used to trigger a reload of the Prometheus configuration and rule files |
| `runbooks_list` | List the runbooks embedded in this server: guided workflows (Agent Skills) for common Prometheus tasks |
| `runbooks_read` | Read the named runbook by skill name (e.g. `check-system-health`) |
| `runtime_info` | Get Prometheus runtime information |
| `series` | Finds series by label matchers |
| `targets_metadata` | Returns metadata about metrics currently scraped by the target |
| `tsdb_stats` | Get usage and cardinality statistics from the TSDB |
| `wal_replay_status` | Get current WAL replay status |

__NOTE:__ 
> Because the [TSDB Admin API endpoints](https://prometheus.io/docs/prometheus/latest/querying/api/#tsdb-admin-apis)
> allow for potentially destructive operations like deleting data, they are not
> enabled by default. In order to enable the TSDB Admin API endpoints, the MCP
> server must be started with the flag `--dangerous.enable-tsdb-admin-tools` to
> acknowledge the associated risk these endpoints carry.

| Tool Name | Description |
| --- | --- |
| `clean_tombstones` | Removes the deleted data from disk and cleans up the existing tombstones |
| `delete_series` | deletes data for a selection of series in a time range |
| `snapshot` | creates a snapshot of all current data into snapshots/<datetime>-<rand> under the TSDB's data directory and returns the directory as response |

#### Tool Sets

The server exposes many tools to interact with Prometheus. There are tools to interact with Prometheus via the API, as well as additional tools to do things like read documentation, etc.
By default, they are all registered and available for use (TSDB Admin API tools need an extra flag).

To be considerate to LLMs with smaller context windows, it's possible to pass in a whitelist of specific tools to register with the server.
The following 'core' tools are always loaded: `[docs_list, docs_read, docs_search, runbooks_list, runbooks_read, query, range_query, metric_metadata, label_names, label_values, series]`.
Additional tools can be specified with the [`--mcp.tools` flag](#command-line-flags).

For example, the command line:

```shell
prometheus-mcp-server --mcp.tools=build_info --mcp.tools=flags --mcp.tools=runtime_info
```

Would result in the following tools being loaded:

- `build_info`
- `docs_list`
- `docs_read`
- `docs_search`
- `flags`
- `label_names`
- `label_values`
- `metric_metadata`
- `query`
- `range_query`
- `runbooks_list`
- `runbooks_read`
- `runtime_info`
- `series`

#### Runbooks (Agent Skills) and Prompts

The server embeds a set of runbooks: guided workflows for common Prometheus tasks, expressed in terms of the server's tools. Each runbook orients the model on the relevant tools, then suggests topics to explore with example queries rather than prescribing a fixed sequence of steps.
Runbooks cover tasks like system health checks, missing-data triage, error-rate investigation, high-cardinality optimization, recording/alerting rule review, and configuration/performance tuning.

Each runbook is packaged as a full [Agent Skill](https://agentskills.io/specification): a directory containing a `SKILL.md` with `name`/`description` frontmatter.
Runbooks are exposed three ways:

- **Tools**: the model can discover and read them itself via the `runbooks_list`/`runbooks_read` tools when a request matches a runbook's purpose. Tools are the most portable path and work in every MCP client.
- **Skill resources**: per the [SEP-2640 skills extension draft](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2640), each runbook is a `skill://<name>/SKILL.md` resource, enumerated by the well-known `skill://index.json` discovery index, and the server declares the `io.modelcontextprotocol/skills` extension capability. Skill-aware hosts can consume these like local filesystem skills; other clients can still read them as ordinary MCP resources.
- **MCP prompts**: each runbook is also registered as an [MCP prompt](https://modelcontextprotocol.io/docs/concepts/prompts) under its skill name (e.g. `check-system-health`, `optimize-high-cardinality`), so clients with prompt support can invoke a guided workflow directly (often surfaced as slash commands).

#### Prometheus Compatible Backends

There are many Prometheus compatible backends that can be used to extend prometheus in a variety of ways, often with the goals of offering long term storage or query aggregation from multiple prometheus instances.
Some examples can be found in the [Remote Storage](https://prometheus.io/docs/operating/integrations/#remote-endpoints-and-storage) of prometheus' docs.

Many of those services also offer a "prometheus compatible" API that can be used to query/interact with the data using native promQL.
In general, this MCP server should at a minimum work for other prometheus API compatible services to execute queries and interact with the series/labels/metadata endpoints for metric and label discovery.
Beyond that, there may be API differences as the different systems implement different parts/extensions of the API for their needs.

Examples:
- Thanos does not use a centralized config, so the config endpoint is not implemented and thus the config tool fails.
- Mimir and Cortex implement extra endpoints to manage/add/remove rules

To workaround this and provide a better experience on some of the commonly used Prometheus compatible systems, this project may add direct support for select systems to provide different/more tools.
Choosing a specific prometheus backend implementation can be done with the [`--prometheus.backend` flag](#command-line-flags).
The list of available backend implementations on a given release of the MCP server can be found in the output of the [`--help` flag](#command-line-flags).
Qualifications and support criteria are still under consideration, please open an issue to request support/features for a specific backend for further discussion.

##### Prometheus Backend Implementation Differences

| Backend | Tool | Add/Remove/Change | Notes |
| --- | --- | --- | --- |
| `prometheus` | n/a | none | Standard prometheus tools. Functionally equivalent to `--mcp.tools="all"`. The default MCP server toolset. |
| [`thanos`](https://thanos.io/) | `alertmanagers` | remove | Thanos does not implement the endpoint and the tool returns a `404`. |
| [`thanos`](https://thanos.io/) | `clean_tombstones` | remove | Prometheus TSDB admin endpoint |
| [`thanos`](https://thanos.io/) | `config` | remove | Thanos does not use a centralized config, so it doesn't implement the endpoint and the tool returns a `404`. |
| [`thanos`](https://thanos.io/) | `delete_series` | remove | Prometheus TSDB admin endpoint |
| [`thanos`](https://thanos.io/) | `list_stores` | add | Thanos provides an additional endpoint to list store API servers. |
| [`thanos`](https://thanos.io/) | `quit` | remove | Thanos does not implement the endpoint and the tool returns a `404`. |
| [`thanos`](https://thanos.io/) | `reload` | remove | Thanos does not implement the endpoint and the tool returns a `404`. |
| [`thanos`](https://thanos.io/) | `snapshot` | remove | Prometheus TSDB admin endpoint |
| [`thanos`](https://thanos.io/) | `wal_replay_status` | remove | Thanos does not implement the endpoint and the tool returns a `404`. |

### Resources

| Resource Name | Resource URI | Description |
| --- | --- | --- |
| List of Official Prometheus Documentation Files | `prometheus://docs` | List of official Prometheus Documentation files |
| Read Official Prometheus Documentation | `prometheus://docs/{+file}` | Read official Prometheus Documentation files by name |
| Agent Skills Discovery Index | `skill://index.json` | [SEP-2640](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2640) discovery index of the embedded runbooks/skills |
| Prometheus Runbook (Agent Skill) | `skill://<name>/SKILL.md` | Read the named runbook, packaged as an Agent Skill (one resource per runbook) |

## Installation and Usage

This MCP server is most useful when fully integrated with tooling and/or installed as a tool server with another system.
Installation procedures and integration support will vary depending on the tools being used.
For example: 
- some systems can only interact with MCP tools and not resources/prompts 
- some systems use mcp.json config file format to manage MCP servers and some require custom formats
- some systems don't speak MCP directly and require tools like mcp-to-openapi to proxy 

Please check the documentation for the tool being used/integrated for specific instructions and level of support.

### Binary
Download a release appropriate for your system from the [Releases](https://github.com/tjhop/prometheus-mcp-server/releases) page.
Please see [Flags](#command-line-flags) for more information on the available flags and their corresponding environment variables.

```shell
/path/to/prometheus-mcp-server <flags>

# or using env vars
PROMETHEUS_MCP_SERVER_PROMETHEUS_URL="https://$yourPrometheus:9090" /path/to/prometheus-mcp-server
```

### Docker
Please see [Flags](#command-line-flags) for more information on the available flags and their corresponding environment variables.

```shell
# Stdio transport
docker run --rm -i ghcr.io/tjhop/prometheus-mcp-server:latest --prometheus.url "https://$yourPrometheus:9090" 

# or using env vars
docker run --rm -i -e PROMETHEUS_MCP_SERVER_PROMETHEUS_URL="https://$yourPrometheus:9090" ghcr.io/tjhop/prometheus-mcp-server:latest
```

```shell
# Streamable HTTP transport (capable of SSE as well)
docker run --rm -p 8080:8080 ghcr.io/tjhop/prometheus-mcp-server:latest --prometheus.url "https://$yourPrometheus:9090" --mcp.transport "http" --web.listen-address ":8080"

# or using env vars
docker run --rm -p 8080:8080 -e PROMETHEUS_MCP_SERVER_PROMETHEUS_URL="https://$yourPrometheus:9090" -e PROMETHEUS_MCP_SERVER_MCP_TRANSPORT="http" -e PROMETHEUS_MCP_SERVER_WEB_LISTEN_ADDRESS=":8080" ghcr.io/tjhop/prometheus-mcp-server:latest
```

### Helm Chart (Kubernetes)

A Helm chart is available for deploying to Kubernetes. The chart is published as an OCI artifact on each release.

```bash
helm install prometheus-mcp-server oci://ghcr.io/tjhop/charts/prometheus-mcp-server \
  --version <version> \
  --set prometheus.url=http://prometheus:9090
```

See the [chart README](./charts/prometheus-mcp-server/README.md) for the full list of configuration values and examples.

### Kubernetes (Raw Manifests)

If you prefer deploying without Helm, example Kubernetes manifests are available:

- [Kubernetes Deployment](./examples/k8s-deployment.yml)
- [OpenShift Deployment](./examples/openshift-deployment.yml)

### System Packages
Download a release appropriate for your system from the [Releases](https://github.com/tjhop/prometheus-mcp-server/releases) page. A Systemd service file is included in the system packages that are built.

```shell
# install system package (example assuming Debian based)
apt install /path/to/package
# create unit override, add any needed flags or environment variables
systemctl edit prometheus-mcp-server.service
systemctl enable --now prometheus-mcp-server.service
```

_Note_: While packages are built for several systems, there are currently no plans to attempt to submit packages to upstream package repositories.

## Security and Authentication
### Connecting to Secure Prometheus Instances

The MCP server supports [Prometheus HTTP config](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#http_config) files to connect to secured Prometheus instances.
An example config can be found [in the examples folder here](./examples/http-config.yml).
Use the `--http.config` command-line flag to provide an HTTP configuration file.
Please see [Flags](#command-line-flags) for more information.

### Forwarding Client Credentials

When using the HTTP transport, the MCP server forwards the `Authorization` header on each MCP request to Prometheus for that request's API calls.
Requests that carry no `Authorization` header, and all requests over the stdio transport, use the default client built from flags (see [Connecting to Secure Prometheus Instances](#connecting-to-secure-prometheus-instances)).

The server forwards client credentials to Prometheus as-is and does not validate them.
A credential that does not include an auth scheme is sent as a `Bearer` token.
Anyone who can reach the MCP endpoint can query Prometheus with at least the default client's credentials, so restrict access to the endpoint with a [web configuration file](#securing-the-mcp-server-endpoints) or network-level controls.
Note that basic authentication in the web configuration file (`basic_auth_users`) conflicts with credential forwarding: the same `Authorization` header a client uses to authenticate to the MCP server is then forwarded to Prometheus in place of the default client's credentials.
TLS-only web configurations do not use the `Authorization` header and are unaffected.

### Securing the MCP Server Endpoints

The MCP server supports [Prometheus Web Configuration files](https://github.com/prometheus/exporter-toolkit/blob/master/docs/web-configuration.md) files to expose it's endpoints behind optional basic auth and custom TLS configs.
Use the `--web.config.file` command-line flag to provide an HTTP configuration file.
Please see [Flags](#command-line-flags) for more information.

## Telemetry
### Health Endpoints

The web server exposes health endpoints on the configured listen address, following the `/-/healthy` and `/-/ready` convention used across the Prometheus ecosystem:

| Endpoint | Description |
| --- | --- |
| `/-/healthy` | Liveness: always returns `200 OK` while the process is up and serving HTTP. |
| `/-/ready` | Readiness: returns `200 OK` once the MCP transport is mounted and able to accept client sessions, and `503 Service Unavailable` before that and during shutdown. |

These map directly onto Kubernetes liveness/readiness probes. The `prom_mcp_server_ready` metric reports the same readiness state.

### Metrics

Once running, the server exposes Prometheus metrics on the configured listen address and telemetry path (`:8080/metrics`, by default).
Please see [Flags](#command-line-flags) for more information on how to change the listening interface, port, or telemetry path.

<details>
<summary>Prometheus MCP Server Metrics</summary>

| Metric name | Type | Description | Labels |
| --- | --- | --- | --- |
| `prom_mcp_build_info` | `Gauge` | A metric with a constant '1' value with labels for version, commit and build_date from which prometheus-mcp-server was built. | `version`, `commit`, `build_date`, `goversion` |
| `prom_mcp_server_ready` | `Gauge` | Info metric with a static '1' if the MCP server is ready, and '0' otherwise. | |
| `prom_mcp_api_calls_failed_total` | `Counter` | Total number of Prometheus API failures, per endpoint. | `target_path` |
| `prom_mcp_api_call_duration_seconds` | `Histogram` | Duration of Prometheus API calls, per endpoint, in seconds. | `target_path` |
| `prom_mcp_tool_calls_failed_total` | `Counter` | Total number of failures per tool. | `tool_name` |
| `prom_mcp_tool_call_duration_seconds` | `Histogram` | Duration of tool calls, per tool, in seconds. | `tool_name` |
| `prom_mcp_resource_calls_failed_total` | `Counter` | Total number of failures per resource. | `resource_uri` |
| `prom_mcp_resource_call_duration_seconds` | `Histogram` | Duration of resource calls, per resource, in seconds. | `resource_uri` |
| `prom_mcp_docs_last_update_timestamp_seconds` | `Gauge` | Unix timestamp of last successful docs auto-update. | |
| `prom_mcp_docs_update_failures_total` | `Counter` | Total number of docs auto-update failures. | |
| `go_*` | `Gauge`/`Counter` | Standard Go runtime metrics from the `client_golang` library. | |
| `process_*` | `Gauge`/`Counter` | Standard process metrics from the `client_golang` library. | |

</details>

### Grafana Dashboard

A pre-built [Grafana](https://grafana.com/) dashboard is included in the [`grafana/`](./grafana/) directory for visualizing the metrics exposed by the MCP server. Import the dashboard json into grafana and it should be ready to go.

### Logs

This project makes heavy use of structured, leveled logging.
Please see [Flags](#command-line-flags) for more information on how to set the log format, level, and optional file.

## Development
### Development Environment with Devbox + Direnv
If you use [Devbox](https://www.jetify.com/devbox) and
[Direnv](https://direnv.net/), then simply entering the directory for the repo
should set up the needed software.

### Local LLM with Ollama
See [`mcp.json`](./examples/mcp.json) for an example MCP config for use with tooling.
Requires [`ollama`](https://github.com/ollama/ollama) to be installed.

_NOTE:_ 
> To override the default LLM (`ollama:gpt-oss:20b`), run `export
> OLLAMA_MODEL="ollama:your_model"` to override it before running `make` .

<details>
<summary>Using MCP Inspector and a local ollama instance:</summary>

Requires [MCP Inpsector](https://github.com/modelcontextprotocol/inspector) to be installed:

```bash
make inspector
```
</details>

<details>
<summary>Using mcphost and a local ollama instance:</summary>

Requires [`mcphost`](https://github.com/mark3labs/mcphost) to be installed:

```bash
make mcphost
```
</details>

### Gemini with gemini-cli
See [`settings.json`](./examples/.gemini/settings.json) for an example config file to run gemini-cli with the prometheus-mcp-server.
Requires [gemini-cli](https://github.com/google-gemini/gemini-cli) to be installed. 

<details>
<summary>Using `gemini-cli` and hosted models:</summary>

```bash
make gemini
```
</details>

### Manual Setup
Required software:
- Working Go environment
- Docker for local tests/linting/image building/etc
- GNU Make
- [ollama](https://github.com/ollama/ollama)
- [mcp inspector](https://github.com/modelcontextprotocol/inspector)
- [mcphost](https://github.com/mark3labs/mcphost)

### Building

This project uses the standard Prometheus build tooling: [`promu`](https://github.com/prometheus/promu)
driven through `Makefile` / `Makefile.common`. The binary embeds a pinned
snapshot of `prometheus/docs`, which the build targets download and extract
automatically (the pin lives in `DOCS_VERSION` in the `Makefile`).

```bash
make build       # build the binary for the host platform (via promu)
make test        # run the test suite
make crossbuild  # build binaries for all release platforms
make             # run the full check suite: style, license, yamllint, lint, build, test
```

Project-specific helper targets (helm packaging and the local LLM client
integrations) are listed by `make help`:

```bash
make help

Usage:
  make <target>

Project targets:
  help                           print this help message (see Makefile.common for the standard prometheus targets)
  docs                           download and extract the pinned prometheus/docs snapshot for embedding
  helm-sync-dashboards           copy grafana dashboards into helm chart for packaging
  helm-lint                      run helm chart linting
  helm-template                  render helm templates for inspection
  helm-test                      install helm chart and run tests (requires a running cluster)
  mcphost                        use mcphost to run the prometheus-mcp-server against a local ollama model
  inspector                      use inspector to run the prometheus-mcp-server in STDIO transport mode
  inspector-http                 use inspector to run the prometheus-mcp-server in streamable HTTP transport mode
  open-webui                     use open-webui to run the prometheus-mcp-server
  gemini                         use gemini-cli to run the prometheus-mcp-server against Google Gemini models
```
## Command Line Flags

The available command line flags are documented in the help flag:

```bash
~/go/src/github.com/tjhop/prometheus-mcp-server (main [ ]) -> ./prometheus-mcp-server --help
usage: prometheus-mcp-server [<flags>]


Flags:
  -h, --[no-]help                Show context-sensitive help (also
                                 try --help-long and --help-man).
                                 ($PROMETHEUS_MCP_SERVER_HELP)
      --mcp.tools=all ...        List of mcp tools to load. The target
                                 `all` can be used to load all tools.
                                 The target `core` loads only the core tools:
                                 docs_list,docs_read,docs_search,runbooks_list,runbooks_read,query,range_query,metric_metadata,label_names,label_values,series
                                 Otherwise, it is treated as an allow-list
                                 of tools to load, in addition to the core
                                 tools. Please see project README for more
                                 information and the full list of tools.
                                 ($PROMETHEUS_MCP_SERVER_MCP_TOOLS)
      --[no-]mcp.enable-toon-output  
                                 Enable Token-Oriented Object Notation
                                 (TOON) output for tools instead of JSON
                                 ($PROMETHEUS_MCP_SERVER_MCP_ENABLE_TOON_OUTPUT)
      --[no-]mcp.enable-client-logging  
                                 Enable sending log messages to connected
                                 MCP clients as protocol notifications.
                                 When enabled, tool execution logs are
                                 sent both to the server's primary
                                 log output and to the MCP client,
                                 allowing LLMs to observe server activity.
                                 ($PROMETHEUS_MCP_SERVER_MCP_ENABLE_CLIENT_LOGGING)
      --mcp.transport="stdio"    The type of transport to use for
                                 the MCP server [`stdio`, `http`].
                                 ($PROMETHEUS_MCP_SERVER_MCP_TRANSPORT)
      --prometheus.backend=PROMETHEUS.BACKEND  
                                 Customize the toolset for a specific
                                 Prometheus API compatible backend.
                                 Supported backends include: prometheus,thanos
                                 ($PROMETHEUS_MCP_SERVER_PROMETHEUS_BACKEND)
      --prometheus.url="http://127.0.0.1:9090"  
                                 URL of the Prometheus instance to connect to
                                 ($PROMETHEUS_MCP_SERVER_PROMETHEUS_URL)
      --prometheus.timeout=1m    Timeout for API calls to the Prometheus backend
                                 ($PROMETHEUS_MCP_SERVER_PROMETHEUS_TIMEOUT)
      --prometheus.truncation-limit=0  
                                 If enabled, this controls the maximum query
                                 response size in number of lines/entries
                                 provided to the LLM from the API response.
                                 LLMs can override truncation limits if
                                 needed on a per-tool-call basis via tool
                                 request arguments on supported tools.
                                 To disable truncation limits, set to 0.
                                 ($PROMETHEUS_MCP_SERVER_PROMETHEUS_TRUNCATION_LIMIT)
      --http.config=HTTP.CONFIG  Path to config file to set
                                 Prometheus HTTP client options
                                 ($PROMETHEUS_MCP_SERVER_HTTP_CONFIG)
      --web.telemetry-path="/metrics"  
                                 Path under which to expose metrics.
                                 ($PROMETHEUS_MCP_SERVER_WEB_TELEMETRY_PATH)
      --web.max-requests=40      Maximum number of parallel scrape
                                 requests. Use 0 to disable.
                                 ($PROMETHEUS_MCP_SERVER_WEB_MAX_REQUESTS)
      --[no-]dangerous.enable-tsdb-admin-tools  
                                 Enable and allow using tools that access
                                 Prometheus' TSDB Admin API endpoints
                                 (`snapshot`, `delete_series`, and
                                 `clean_tombstones` tools). This is dangerous,
                                 and allows for destructive operations
                                 like deleting data. It is not the fault
                                 of this MCP server if the LLM you're
                                 connected to nukes all your data. Docs:
                                 https://prometheus.io/docs/prometheus/latest/querying/api/#tsdb-admin-apis
                                 ($PROMETHEUS_MCP_SERVER_DANGEROUS_ENABLE_TSDB_ADMIN_TOOLS)
      --[no-]docs.auto-update    Enable automatic documentation updates
                                 from the official prometheus/docs
                                 repository. Checks every 24h0m0s.
                                 ($PROMETHEUS_MCP_SERVER_DOCS_AUTO_UPDATE)
      --log.file=LOG.FILE        The name of the file to log to (file
                                 rotation policies should be configured
                                 with external tools like logrotate)
                                 ($PROMETHEUS_MCP_SERVER_LOG_FILE)
      --[no-]web.systemd-socket  Use systemd socket activation listeners
                                 instead of port listeners (Linux only).
                                 ($PROMETHEUS_MCP_SERVER_WEB_SYSTEMD_SOCKET)
      --web.listen-address=:8080 ...  
                                 Addresses on which to expose metrics and
                                 web interface. Repeatable for multiple
                                 addresses. Examples: `:9100` or `[::1]:9100`
                                 for http, `vsock://:9100` for vsock
                                 ($PROMETHEUS_MCP_SERVER_WEB_LISTEN_ADDRESS)
      --web.config.file=""       Path to configuration file that can
                                 enable TLS or authentication. See:
                                 https://github.com/prometheus/exporter-toolkit/blob/master/docs/web-configuration.md
                                 ($PROMETHEUS_MCP_SERVER_WEB_CONFIG_FILE)
      --log.level=info           Only log messages with the given severity
                                 or above. One of: [debug, info, warn, error]
                                 ($PROMETHEUS_MCP_SERVER_LOG_LEVEL)
      --log.format=logfmt        Output format of log messages. One of: [logfmt,
                                 json] ($PROMETHEUS_MCP_SERVER_LOG_FORMAT)
      --[no-]version             Show application version.
                                 ($PROMETHEUS_MCP_SERVER_VERSION)
```
