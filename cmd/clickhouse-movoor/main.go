// Command clickhouse-movoor is the entrypoint binary. It wires up the CLI,
// loads configuration, and runs the application.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/lmittmann/tint"
	"github.com/spf13/cobra"

	movoor "github.com/ethpandaops/clickhouse-movoor"
)

// Build-time metadata, overridden via -ldflags. See the Makefile.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var loadConfigForRun = loadConfig

func main() {
	mainWith(run, os.Exit, os.Stderr)
}

func mainWith(runFunc func() error, exitFunc func(int), stderr io.Writer) {
	if err := runFunc(); err != nil {
		if errors.Is(err, context.Canceled) {
			_, _ = fmt.Fprintln(stderr, "Cancelled.")

			return
		}

		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		exitFunc(1)
	}
}

func run() error {
	var (
		configFile string
		verbose    bool
		logFormat  string
		logLevel   slog.LevelVar // defaults to LevelInfo
	)

	rootCmd := &cobra.Command{
		Use:           "clickhouse-movoor",
		Short:         "clickhouse-movoor moves cold ClickHouse partitions to a configured target disk",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfigForRun(configFile)
			if err != nil {
				return err
			}
			cfg.ResolveDefaults()

			if levelErr := setLogLevel(&logLevel, cfg.Logging); levelErr != nil {
				return levelErr
			}
			if verbose {
				logLevel.Set(slog.LevelDebug)
			}

			log := newLogger(&logLevel, logFormat, verbose)
			slog.SetDefault(log)

			movoor.Version = version

			app, err := movoor.New(log, cfg)
			if err != nil {
				return fmt.Errorf("init clickhouse-movoor: %w", err)
			}

			return app.Run(cmd.Context())
		},
	}

	rootCmd.Flags().StringVar(&configFile, "config", "", "path to YAML config file")
	rootCmd.Flags().BoolVar(&verbose, "verbose", false, "enable debug logging")
	rootCmd.PersistentFlags().StringVar(&logFormat, "log-format", "text", "log output format (text, json)")

	rootCmd.AddCommand(versionCommand())

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	return rootCmd.ExecuteContext(ctx)
}

func setLogLevel(level *slog.LevelVar, configured string) error {
	switch strings.ToLower(configured) {
	case "debug":
		level.Set(slog.LevelDebug)
	case "info":
		level.Set(slog.LevelInfo)
	case "warn", "warning":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	default:
		return fmt.Errorf("invalid logging level %q", configured)
	}

	return nil
}

// newLogger builds an *slog.Logger using a colourised text handler by default,
// or a JSON handler when format is "json".
func newLogger(level *slog.LevelVar, format string, addSource bool) *slog.Logger {
	if format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level:     level,
			AddSource: addSource,
		}))
	}

	return slog.New(tint.NewHandler(os.Stdout, &tint.Options{
		Level:     level,
		AddSource: addSource,
	}))
}

// loadConfig loads the config from the given path, falling back to
// ~/.clickhouse-movoor/config.yaml when --config is omitted.
func loadConfig(path string) (movoor.Config, error) {
	if path == "" {
		path = discoverConfigPath()
	}

	if path == "" {
		return movoor.Config{}, errors.New("config file is required; pass --config or create ~/.clickhouse-movoor/config.yaml")
	}

	cfg, err := movoor.LoadConfig(path)
	if err != nil {
		return movoor.Config{}, fmt.Errorf("load config: %w", err)
	}

	return cfg, nil
}

// discoverConfigPath returns ~/.clickhouse-movoor/config.yaml when it exists,
// or an empty string.
func discoverConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	candidate := filepath.Join(home, ".clickhouse-movoor", "config.yaml")
	if _, statErr := os.Stat(candidate); statErr != nil {
		return ""
	}

	return candidate
}

// versionCommand prints build metadata.
func versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(
				cmd.OutOrStdout(),
				"clickhouse-movoor %s (commit: %s, built: %s)\n",
				version, commit, date,
			)

			return err
		},
	}
}
