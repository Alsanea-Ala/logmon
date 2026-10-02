package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Alsanea-Ala/logmon/internal/agent"
	"github.com/Alsanea-Ala/logmon/internal/protocol"
	"github.com/spf13/cobra"
)

type options struct {
	config     string
	id         string
	hostname   string
	server     string
	retry      bool
	retryDelay time.Duration
	files      []string
	journals   []string
	docker     []string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newCommand(agent.Run).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newCommand(run func(context.Context, agent.Config) error) *cobra.Command {
	defaults := agent.DefaultConfig()
	var opts options
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Collect Linux logs and send them to a Logmon server",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := agent.LoadConfig(opts.config)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			flags := cmd.Flags()
			if flags.Changed("id") {
				cfg.ID = opts.id
			}
			if flags.Changed("hostname") {
				cfg.Hostname = opts.hostname
			}
			if flags.Changed("server") {
				cfg.Server = opts.server
			}
			if flags.Changed("retry") {
				cfg.Retry = opts.retry
			}
			if flags.Changed("retry-delay") {
				cfg.RetryDelay = opts.retryDelay
			}
			if flags.Changed("file") || flags.Changed("journal") || flags.Changed("docker") {
				cfg.Sources = nil
				groups := []struct {
					kind  string
					specs []string
				}{{"file", opts.files}, {"journald", opts.journals}, {"docker", opts.docker}}
				for _, group := range groups {
					for _, spec := range group.specs {
						source, err := agent.ParseSource(group.kind, spec)
						if err != nil {
							return err
						}
						cfg.Sources = append(cfg.Sources, source)
					}
				}
			}

			// resolve hostname before validation + banner
			if cfg.Hostname == "" {
				if h, err := os.Hostname(); err == nil {
					cfg.Hostname = h
				} else {
					cfg.Hostname = "unknown"
				}
			}

			validationErr := cfg.Validate()

			// start animated banner
			bannerCtx, bannerCancel := context.WithCancel(cmd.Context())
			bannerDone := make(chan struct{})
			go animateBanner(bannerCtx, cfg, validationErr, bannerDone)

			if validationErr != nil {
				bannerCancel()
				<-bannerDone
				return validationErr
			}

			err = run(cmd.Context(), cfg)

			bannerCancel()
			<-bannerDone
			return err
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.config, "config", "", "optional YAML config path")
	flags.StringVar(&opts.id, "id", "", "agent ID")
	flags.StringVar(&opts.hostname, "hostname", "", "hostname override")
	flags.StringVar(&opts.server, "server", defaults.Server, "server host:port")
	flags.BoolVar(&opts.retry, "retry", defaults.Retry, "retry failed sources and connections")
	flags.DurationVar(&opts.retryDelay, "retry-delay", defaults.RetryDelay, "delay before retry")
	flags.StringArrayVar(&opts.files, "file", nil, "file source: path,app,category,from-beginning")
	flags.StringArrayVar(&opts.journals, "journal", nil, "journald source: unit,app,category,from-beginning")
	flags.StringArrayVar(&opts.docker, "docker", nil, "Docker source: container,app,category,from-beginning")
	return cmd
}

const (
	colorReset  = "\x1b[0m"
	colorDark   = "\x1b[38;5;31m" // dark cyan-blue
	colorCyan   = "\x1b[38;5;38m" // cyan
	colorTeal   = "\x1b[38;5;44m" // teal
	colorAqua   = "\x1b[38;5;51m" // aqua
	colorWhite  = "\x1b[37m"
	colorGreen  = "\x1b[32m"
	colorRed    = "\x1b[31m"
	colorYellow = "\x1b[33m"
	colorDim    = "\x1b[2m"
)

// LOGMON banner - user provided design
var logoFrames = []string{
	`██╗      ██████╗  ██████╗ ███╗   ███╗ ██████╗ ███╗   ██╗
██║     ██╔═══██╗██╔════╝ ████╗ ████║██╔═══██╗████╗  ██║
██║     ██║   ██║██║  ███╗██╔████╔██║██║   ██║██╔██╗ ██║
██║     ██║   ██║██║   ██║██║╚██╔╝██║██║   ██║██║╚██╗██║
███████╗╚██████╔╝╚██████╔╝██║ ╚═╝ ██║╚██████╔╝██║ ╚████║
╚══════╝ ╚═════╝  ╚═════╝ ╚═╝     ╚═╝ ╚═════╝ ╚═╝  ╚═══╝`,
	`██╗      ██████╗  ██████╗ ███╗   ███╗ ██████╗ ███╗   ██╗
██║     ██╔═══██╗██╔════╝ ████╗ ████║██╔═══██╗████╗  ██║
██║     ██║   ██║██║  ███╗██╔████╔██║██║   ██║██╔██╗ ██║
██║     ██║   ██║██║   ██║██║╚██╔╝██║██║   ██║██║╚██╗██║
███████╗╚██████╔╝╚██████╔╝██║ ╚═╝ ██║╚██████╔╝██║ ╚████║
╚══════╝ ╚═════╝  ╚═════╝ ╚═╝     ╚═╝ ╚═════╝ ╚═╝  ╚═══╝`,
	`██╗      ██████╗  ██████╗ ███╗   ███╗ ██████╗ ███╗   ██╗
██║     ██╔═══██╗██╔════╝ ████╗ ████║██╔═══██╗████╗  ██║
██║     ██║   ██║██║  ███╗██╔████╔██║██║   ██║██╔██╗ ██║
██║     ██║   ██║██║   ██║██║╚██╔╝██║██║   ██║██║╚██╗██║
███████╗╚██████╔╝╚██████╔╝██║ ╚═╝ ██║╚██████╔╝██║ ╚████║
╚══════╝ ╚═════╝  ╚═════╝ ╚═╝     ╚═╝ ╚═════╝ ╚═╝  ╚═══╝`,
}

var logoColors = []string{colorDark, colorCyan, colorTeal, colorAqua}

func animateBanner(ctx context.Context, cfg agent.Config, validationErr error, done chan struct{}) {
	defer close(done)

	useColor := shouldColor()
	if !useColor {
		printStaticBanner(cfg, validationErr, false)
		return
	}

	fmt.Fprint(os.Stdout, "\x1b[H\x1b[2J")

	frameIdx := 0
	colorIdx := 0
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Fprint(os.Stdout, "\x1b[H")
			printStaticBanner(cfg, validationErr, true)
			return
		case <-ticker.C:
			fmt.Fprint(os.Stdout, "\x1b[H")
			renderFrame(frameIdx, colorIdx, cfg, validationErr, useColor)
			frameIdx = (frameIdx + 1) % len(logoFrames)
			colorIdx = (colorIdx + 1) % len(logoColors)
		}
	}
}

func renderFrame(frameIdx, colorIdx int, cfg agent.Config, validationErr error, useColor bool) {
	c := logoColors[colorIdx]

	fmt.Fprint(os.Stdout, c)
	fmt.Fprint(os.Stdout, logoFrames[frameIdx])
	fmt.Fprint(os.Stdout, colorReset)
	fmt.Fprint(os.Stdout, "\n\n")

	// separator
	if useColor {
		fmt.Fprint(os.Stdout, colorDim)
	}
	fmt.Fprint(os.Stdout, "---info---")
	if useColor {
		fmt.Fprint(os.Stdout, colorReset)
	}
	fmt.Fprint(os.Stdout, "\n\n")

	printInfo("id", cfg.ID, validationErr != nil && !protocol.ValidName(cfg.ID), useColor)
	printInfo("hostname", cfg.Hostname, false, useColor)
	printInfo("server", cfg.Server, cfg.Server == "", useColor)
	printInfo("retry", fmt.Sprintf("%v (%v)", cfg.Retry, cfg.RetryDelay), false, useColor)
	tokenStatus := "set"
	tokenBad := false
	if cfg.Token == "" {
		tokenStatus = "missing"
		tokenBad = true
	}
	printInfo("token", tokenStatus, tokenBad, useColor)
	printInfo("sources", fmt.Sprintf("%d", len(cfg.Sources)), len(cfg.Sources) == 0, useColor)

	for i, src := range cfg.Sources {
		var detail string
		bad := false
		switch src.Type {
		case "file":
			detail = fmt.Sprintf("file %s/%s -> %s", src.App, src.Category, src.Path)
			bad = src.Path == ""
		case "journald":
			detail = fmt.Sprintf("journald %s/%s -> %s", src.App, src.Category, src.Unit)
			bad = src.Unit == ""
		case "docker":
			detail = fmt.Sprintf("docker %s/%s -> %s", src.App, src.Category, src.Container)
			bad = src.Container == ""
		default:
			detail = fmt.Sprintf("unknown %s/%s", src.App, src.Category)
			bad = true
		}
		printInfo(fmt.Sprintf("  [%d]", i+1), detail, bad, useColor)
	}

	if validationErr != nil {
		prefix := "error"
		if useColor {
			prefix = colorRed + "error" + colorReset
		}
		fmt.Fprintf(os.Stdout, "%s: %v\n", prefix, validationErr)
	}
}

func printStaticBanner(cfg agent.Config, validationErr error, useColor bool) {
	if useColor {
		fmt.Fprint(os.Stdout, colorDark)
	}
	fmt.Fprint(os.Stdout, logoFrames[0])
	if useColor {
		fmt.Fprint(os.Stdout, colorReset)
	}
	fmt.Fprint(os.Stdout, "\n\n")

	// separator
	if useColor {
		fmt.Fprint(os.Stdout, colorDim)
	}
	fmt.Fprint(os.Stdout, "---info---")
	if useColor {
		fmt.Fprint(os.Stdout, colorReset)
	}
	fmt.Fprint(os.Stdout, "\n\n")

	printInfo("id", cfg.ID, validationErr != nil && !protocol.ValidName(cfg.ID), useColor)
	printInfo("hostname", cfg.Hostname, false, useColor)
	printInfo("server", cfg.Server, cfg.Server == "", useColor)
	printInfo("retry", fmt.Sprintf("%v (%v)", cfg.Retry, cfg.RetryDelay), false, useColor)
	tokenStatus := "set"
	tokenBad := false
	if cfg.Token == "" {
		tokenStatus = "missing"
		tokenBad = true
	}
	printInfo("token", tokenStatus, tokenBad, useColor)
	printInfo("sources", fmt.Sprintf("%d", len(cfg.Sources)), len(cfg.Sources) == 0, useColor)

	for i, src := range cfg.Sources {
		var detail string
		bad := false
		switch src.Type {
		case "file":
			detail = fmt.Sprintf("file %s/%s -> %s", src.App, src.Category, src.Path)
			bad = src.Path == ""
		case "journald":
			detail = fmt.Sprintf("journald %s/%s -> %s", src.App, src.Category, src.Unit)
			bad = src.Unit == ""
		case "docker":
			detail = fmt.Sprintf("docker %s/%s -> %s", src.App, src.Category, src.Container)
			bad = src.Container == ""
		default:
			detail = fmt.Sprintf("unknown %s/%s", src.App, src.Category)
			bad = true
		}
		printInfo(fmt.Sprintf("  [%d]", i+1), detail, bad, useColor)
	}

	if validationErr != nil {
		prefix := "error"
		if useColor {
			prefix = colorRed + "error" + colorReset
		}
		fmt.Fprintf(os.Stdout, "%s: %v\n", prefix, validationErr)
	}
}

func printInfo(label, value string, bad bool, useColor bool) {
	l := label
	v := value
	if useColor {
		l = colorWhite + label + colorReset
		if bad {
			v = colorRed + value + colorReset
		} else {
			v = colorGreen + value + colorReset
		}
	} else if bad {
		v = "[ERROR] " + value
	}
	fmt.Fprintf(os.Stdout, "%s: %s\n", l, v)
}

func shouldColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, _ := os.Stdout.Stat()
	return (fi.Mode() & os.ModeCharDevice) != 0
}
