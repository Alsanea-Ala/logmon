package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Alsanea-Ala/logmon/internal/server"
	"github.com/spf13/cobra"
)

type options struct {
	config         string
	listen         string
	dataDir        string
	maxConnections int
	noTUI          bool
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newCommand(server.Run).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newCommand(run func(context.Context, server.Config, bool) error) *cobra.Command {
	defaults := server.DefaultConfig()
	var opts options
	cmd := &cobra.Command{
		Use:          "server",
		Short:        "Receive and browse logs from Logmon agents",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := server.LoadConfig(opts.config)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			flags := cmd.Flags()
			if flags.Changed("listen") {
				cfg.Listen = opts.listen
			}
			if flags.Changed("data-dir") {
				cfg.DataDir = opts.dataDir
			}
			if flags.Changed("max-connections") {
				cfg.MaxConnections = opts.maxConnections
			}
			return run(cmd.Context(), cfg, opts.noTUI)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.config, "config", "", "YAML config path containing the agent allowlist")
	flags.StringVar(&opts.listen, "listen", defaults.Listen, "TCP listen host:port")
	flags.StringVar(&opts.dataDir, "data-dir", defaults.DataDir, "log storage directory")
	flags.IntVar(&opts.maxConnections, "max-connections", defaults.MaxConnections, "maximum simultaneous agent connections")
	flags.BoolVar(&opts.noTUI, "no-tui", false, "run without TUI (headless)")
	return cmd
}
