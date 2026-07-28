// Copyright The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin/v2"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	config_util "github.com/prometheus/common/config"
	"github.com/prometheus/common/promslog"
	"github.com/prometheus/common/promslog/flag"
	promversion "github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
	"github.com/prometheus/exporter-toolkit/web/kingpinflag"

	"github.com/prometheus/prometheus-mcp/internal/metrics"
	"github.com/prometheus/prometheus-mcp/pkg/mcp"
)

const (
	programName = "prometheus-mcp"
	defaultPort = 8080
)

var (
	//go:embed all:external/docs
	assetsDocs embed.FS
	docsFs     fs.FS

	// TODO (@tjhop): change this to an enum?
	flagMcpTools = kingpin.Flag(
		"mcp.tools",
		"List of mcp tools to load."+
			" The target `all` can be used to load all tools."+
			" The target `core` loads only the core tools: "+strings.Join(mcp.CoreTools, ",")+
			" Otherwise, it is treated as an allow-list of tools to load, in addition to the core tools."+
			" Please see project README for more information and the full list of tools.",
	).Default("all").Strings()

	flagMcpToonOutputEnabled = kingpin.Flag(
		"mcp.enable-toon-output",
		"Enable Token-Oriented Object Notation (TOON) output for tools instead of JSON",
	).Default("false").Bool()

	flagMcpClientLogging = kingpin.Flag(
		"mcp.enable-client-logging",
		"Enable sending log messages to connected MCP clients as protocol notifications."+
			" When enabled, tool execution logs are sent both to the server's primary log output"+
			" and to the MCP client, allowing LLMs to observe server activity.",
	).Default("false").Bool()

	// TODO (@tjhop): change this to an enum?
	flagMcpTransport = kingpin.Flag(
		"mcp.transport",
		"The type of transport to use for the MCP server [`stdio`, `http`].",
	).Default("stdio").String()

	flagPrometheusBackend = kingpin.Flag(
		"prometheus.backend",
		"Customize the toolset for a specific Prometheus API compatible backend."+
			" Supported backends include: "+strings.Join(mcp.PrometheusBackends, ","),
	).String()

	flagPrometheusURL = kingpin.Flag(
		"prometheus.url",
		"URL of the Prometheus instance to connect to",
	).Default("http://127.0.0.1:9090").String()

	flagPrometheusTimeout = kingpin.Flag(
		"prometheus.timeout",
		"Timeout for API calls to the Prometheus backend",
	).Default("1m").Duration()

	flagPrometheusTruncationLimit = kingpin.Flag(
		"prometheus.truncation-limit",
		"If enabled, this controls the maximum query response size in number of lines/entries provided to the LLM from the API response."+
			" LLMs can override truncation limits if needed on a per-tool-call basis via tool request arguments on supported tools."+
			" To disable truncation limits, set to 0.",
	).Default("0").Int()

	flagHTTPConfig = kingpin.Flag(
		"http.config",
		"Path to config file to set Prometheus HTTP client options",
	).String()

	flagWebTelemetryPath = kingpin.Flag(
		"web.telemetry-path",
		"Path under which to expose metrics.",
	).Default("/metrics").String()

	flagWebMaxRequests = kingpin.Flag(
		"web.max-requests",
		"Maximum number of parallel scrape requests. Use 0 to disable.",
	).Default("40").Int()

	flagEnableTsdbAdminTools = kingpin.Flag(
		"dangerous.enable-tsdb-admin-tools",
		"Enable and allow using tools that access Prometheus' TSDB Admin API endpoints"+
			" (`snapshot`, `delete_series`, and `clean_tombstones` tools)."+
			" This is dangerous, and allows for destructive operations like deleting data."+
			" It is not the fault of this MCP server if the LLM you're connected to nukes all your data."+
			" Docs: https://prometheus.io/docs/prometheus/latest/querying/api/#tsdb-admin-apis",
	).Default("false").Bool()

	flagMcpKeepaliveInterval = kingpin.Flag(
		"mcp.keepalive-interval",
		"Interval for sending keepalive pings to connected MCP sessions."+
			" If the peer fails to respond, the session is closed."+
			" Most useful for stdio transport to prevent idle connections from dropping.",
	).Default("30s").Duration()

	flagDocsAutoUpdate = kingpin.Flag(
		"docs.auto-update",
		"Enable automatic documentation updates from the official prometheus/docs repository."+
			" Checks every "+mcp.DocsUpdateInterval.String()+".",
	).Default("false").Bool()

	flagLogToFile = kingpin.Flag(
		"log.file",
		"The name of the file to log to (file rotation policies should be configured with external tools like logrotate)",
	).String()

	toolkitFlags = kingpinflag.AddFlags(kingpin.CommandLine, fmt.Sprintf(":%d", defaultPort))
)

// docsCommit is the commit hash of the embedded prometheus/docs, recorded at
// build time by the `docs` Makefile target. It is shown in version info and
// used by the docs updater. It is read from the embedded COMMIT_HASH file at
// the start of main, before any consumer (e.g. the HTTP landing page) runs.
var docsCommit string

func main() {
	if data, err := assetsDocs.ReadFile("external/docs/COMMIT_HASH"); err == nil {
		docsCommit = strings.TrimSpace(string(data))
	}

	promslogConfig := &promslog.Config{}
	flag.AddFlags(kingpin.CommandLine, promslogConfig)
	kingpin.Version(fmt.Sprintf("%s\n  docs commit:      %s\n", promversion.Print(programName), docsCommit))
	kingpin.CommandLine.UsageWriter(os.Stdout)
	kingpin.HelpFlag.Short('h')
	kingpin.CommandLine.DefaultEnvars()
	kingpin.Parse()

	if *flagLogToFile != "" {
		f, err := os.OpenFile(*flagLogToFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			slog.Error("Failed to open log file for writing", "file", *flagLogToFile, "err", err)
			os.Exit(1)
		}
		defer f.Close()

		promslogConfig.Writer = f
	}

	logger := promslog.New(promslogConfig)
	slog.SetDefault(logger)
	logger.Info("Starting "+programName, "version", promversion.Version, "build_date", promversion.BuildDate, "commit", promversion.Revision, "docs_commit", docsCommit, "go_version", runtime.Version())

	// Optionally load HTTP config file to configure HTTP client for Prometheus API.
	rt, err := getRoundTripperFromConfig(*flagHTTPConfig)
	if err != nil {
		logger.Error("Failed to load HTTP config file, using default HTTP round tripper", "err", err)
	}

	ctx, rootCtxCancel := context.WithCancel(context.Background())
	defer rootCtxCancel()

	// Setup static file server for embedded prometheus docs.
	docs, err := fs.Sub(assetsDocs, "external/docs/docs")
	if err != nil {
		logger.Error("Failed to create sub FS for embedded docs", "err", err)
	} else {
		docsFs = docs
	}

	mcpServer, mcpContainer, err := mcp.NewServer(ctx, mcp.ServerConfig{
		Logger:                logger,
		PrometheusURL:         *flagPrometheusURL,
		PrometheusBackend:     *flagPrometheusBackend,
		PrometheusTimeout:     *flagPrometheusTimeout,
		TruncationLimit:       *flagPrometheusTruncationLimit,
		RoundTripper:          rt,
		TSDBAdminToolsEnabled: *flagEnableTsdbAdminTools,
		EnabledTools:          *flagMcpTools,
		DocsFS:                docsFs,
		ToonOutputEnabled:     *flagMcpToonOutputEnabled,
		ClientLoggingEnabled:  *flagMcpClientLogging,
		KeepAlive:             *flagMcpKeepaliveInterval,
	})
	if err != nil {
		logger.Error("Failed to create MCP server", "err", err)

		// Explicitly cancel context before exit.
		rootCtxCancel()
		os.Exit(1) //nolint:gocritic
	}
	srv := initHTTPServer(logger)

	var g run.Group
	{
		// termination and cleanup
		term := make(chan os.Signal, 1)
		signal.Notify(term, os.Interrupt, syscall.SIGTERM)
		cancel := make(chan struct{})
		g.Add(
			func() error {
				select {
				case sig := <-term:
					logger.Warn("caught signal, exiting gracefully.", "signal", sig.String())
				case <-cancel:
				}

				return nil
			},
			func(err error) {
				close(cancel)
				rootCtxCancel()
			},
		)
	}
	{
		// web server for metrics, pprof, and HTTP if transport is configured
		cancel := make(chan struct{})

		g.Add(
			func() error {
				err := web.ListenAndServe(srv, toolkitFlags, logger)
				if !errors.Is(err, http.ErrServerClosed) {
					logger.Error("webserver failed", "err", err)
					return err
				}

				<-cancel

				return nil
			},
			func(error) {
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer shutdownCancel()
				if err := srv.Shutdown(shutdownCtx); err != nil {
					logger.Error("failed to shut down webserver gracefully", "err", err)
				}
				close(cancel)
				rootCtxCancel()
			},
		)
	}
	{
		// MCP transport setup and server
		cancel := make(chan struct{})

		g.Add(
			func() error {
				switch *flagMcpTransport {
				case "stdio":
					logger.Debug("starting MCP server", "transport", "stdio")

					if err := mcpServer.Run(ctx, &sdkmcp.StdioTransport{}); err != nil {
						return fmt.Errorf("MCP server failed: %w", err)
					}

				case "http":
					logger.Debug("starting MCP server", "transport", "http")

					httpMcpHandler := mcp.NewStreamableHTTPHandler(mcpServer, logger)
					http.Handle("/mcp", httpMcpHandler)
					<-cancel
				default:
					return fmt.Errorf("unsupported transport type: %s", *flagMcpTransport)
				}

				return nil
			},
			func(err error) {
				close(cancel)
				rootCtxCancel()
			},
		)
	}

	if *flagDocsAutoUpdate {
		updater := mcp.NewDocsUpdater(logger, mcpContainer, docsCommit)
		cancel := make(chan struct{})
		g.Add(
			func() error {
				logger.Info("docs auto-updater started", "embedded_commit", docsCommit)
				ticker := time.NewTicker(mcp.DocsUpdateInterval)
				defer ticker.Stop()

				for {
					if err := updater.Update(ctx); err != nil {
						logger.Warn("failed to auto update prometheus documentation", "err", err)
					}

					select {
					case <-ticker.C:
						continue
					case <-ctx.Done():
						return nil
					case <-cancel:
						return nil
					}
				}
			},
			func(error) {
				close(cancel)
			},
		)
	}

	if err := g.Run(); err != nil {
		logger.Error("Failed to run daemon goroutines", "err", err)

		// Explicitly cancel context before exit.
		rootCtxCancel()
		os.Exit(1)
	}
	logger.Info("See you next time!")
}

func initHTTPServer(logger *slog.Logger) *http.Server {
	server := &http.Server{
		// These are TCP-level timeouts for individual HTTP
		// request/response cycles. Stateless HTTP transport uses
		// application/json responses that complete in a single round
		// trip, so standard timeouts apply.
		ReadTimeout:  30 * time.Second,
		WriteTimeout: *flagPrometheusTimeout,
		IdleTimeout:  30 * time.Second,
	}

	metricsHandler := promhttp.HandlerFor(
		prometheus.Gatherers{metrics.Registry},
		promhttp.HandlerOpts{
			ErrorLog:            slog.NewLogLogger(logger.Handler(), slog.LevelError),
			ErrorHandling:       promhttp.ContinueOnError,
			MaxRequestsInFlight: *flagWebMaxRequests,
			Registry:            metrics.Registry,
		},
	)
	metricsHandler = promhttp.InstrumentMetricHandler(
		metrics.Registry, metricsHandler,
	)
	http.Handle("/metrics", metricsHandler)

	landingPageLinks := []web.LandingLinks{
		{
			Address: *flagWebTelemetryPath,
			Text:    "Metrics",
		},
	}

	if *flagMcpTransport == "http" {
		landingPageLinks = append(landingPageLinks,
			web.LandingLinks{
				Address: "/mcp",
				Text:    "Prometheus MCP Server",
			},
		)
	}

	if docsFs != nil {
		http.Handle("/docs/", http.StripPrefix("/docs/", http.FileServer(http.FS(docsFs))))
		landingPageLinks = append(landingPageLinks,
			web.LandingLinks{
				Address: "/docs/",
				Text:    "Prometheus Documentation",
			},
		)
	}

	if *flagWebTelemetryPath != "/" {
		landingConfig := web.LandingConfig{
			Name:        "Prometheus MCP Server",
			Description: "MCP Server to interact with Prometheus",
			Version:     fmt.Sprintf("%s (docs_commit=%s)", promversion.Info(), docsCommit),
			Links:       landingPageLinks,
		}
		landingPage, err := web.NewLandingPage(landingConfig)
		if err != nil {
			logger.Error("Failed to create landing page", "err", err)
			os.Exit(1)
		}
		http.Handle("/", landingPage)
	}

	return server
}

func getRoundTripperFromConfig(httpConfig string) (http.RoundTripper, error) {
	httpClient := http.DefaultClient
	if httpConfig != "" {
		httpCfg, _, err := config_util.LoadHTTPConfigFile(httpConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to load HTTP configuration file %s: %w", httpConfig, err)
		}

		if err = httpCfg.Validate(); err != nil {
			return nil, fmt.Errorf("failed to validate HTTP configuration file %s: %w", httpConfig, err)
		}

		httpClient, err = config_util.NewClientFromConfig(*httpCfg, programName)
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP client from configuration file %s: %w", httpConfig, err)
		}
	}

	rt := http.DefaultTransport
	if httpClient.Transport != nil {
		rt = httpClient.Transport
	}

	return rt, nil
}
