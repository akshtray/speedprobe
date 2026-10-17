// Package cli wires the command line: the run and list commands, option
// parsing, the repeat loop, and exit codes. Rendering lives in output and
// measurement in engine; this package only connects them.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/akshtray/speedprobe/internal/engine"
	"github.com/akshtray/speedprobe/internal/output"
	"github.com/akshtray/speedprobe/internal/provider"
	"github.com/akshtray/speedprobe/internal/tui"
)

// Exit codes.
const (
	ExitOK          = 0
	ExitFailed      = 1 // at least one provider failed
	ExitUsage       = 2
	ExitInterrupted = 130
)

// App holds what the commands need from the outside world.
type App struct {
	Version string
	Stdout  io.Writer
	Stderr  io.Writer
}

// Main runs the CLI with args (excluding the program name) and returns the exit code.
func Main(version string, args []string, stdout, stderr io.Writer) int {
	app := &App{Version: version, Stdout: stdout, Stderr: stderr}
	root := app.rootCommand()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	var ec exitCode
	if errors.As(err, &ec) {
		return int(ec)
	}
	fmt.Fprintf(stderr, "speedprobe: %v\n", err)
	var ue *provider.UsageError
	if errors.As(err, &ue) || isFlagError(err) {
		return ExitUsage
	}
	return ExitFailed
}

// exitCode is returned through cobra to carry a non-zero status without a message.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

func isFlagError(err error) bool {
	s := err.Error()
	return strings.HasPrefix(s, "unknown flag") || strings.HasPrefix(s, "unknown shorthand flag") ||
		strings.HasPrefix(s, "invalid argument") || strings.HasPrefix(s, "unknown command") ||
		strings.HasPrefix(s, "flag needs an argument") || strings.HasPrefix(s, "required flag")
}

type runFlags struct {
	providers    []string
	all          bool
	measurements string
	tests        string // hidden alias kept for prototype compatibility
	duration     float64
	connections  int
	pings        int
	json         bool
	log          string
	repeat       int
	interval     float64
	quiet        bool
	mlabServer   int
	ooklaServer  int
	ooklaLicense bool
}

func (a *App) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "speedprobe",
		Short:         "Multi-provider internet speed test",
		Long:          "speedprobe measures your connection against several independent providers from one tool.\nWith no arguments it runs Cloudflare with default settings.",
		Version:       a.Version,
		Example:       "  speedprobe list\n  speedprobe run                      # Cloudflare, all measurements\n  speedprobe run -p cf -t download     # one measurement\n  speedprobe run --all --json > result.json\n  speedprobe run -p cf --repeat 0 --interval 600 --log ~/speed.jsonl -q",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.interactive() {
				return a.tui(cmd.Context())
			}
			return a.execute(cmd.Context(), runFlags{measurements: "all", duration: 10, connections: 6, pings: 20, repeat: 1, interval: 300})
		},
	}
	root.SetVersionTemplate("speedprobe {{.Version}}\n")
	root.CompletionOptions.HiddenDefaultCmd = true
	root.AddCommand(a.runCommand(), a.listCommand(), a.tuiCommand())
	return root
}

// interactive reports whether both stdin and stdout are terminals.
func (a *App) interactive() bool {
	return output.IsTerminal(a.Stdout) && output.IsTerminal(os.Stdin)
}

func (a *App) tuiCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the interactive dashboard (the default on a terminal)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.interactive() {
				fmt.Fprintln(a.Stderr, "speedprobe: the TUI needs an interactive terminal; use `speedprobe run` instead")
				return exitCode(ExitUsage)
			}
			return a.tui(cmd.Context())
		},
	}
}

func (a *App) tui(ctx context.Context) error {
	return tui.Run(ctx, tui.Options{
		Version:  a.Version,
		Registry: provider.New(a.Version, provider.Config{}),
		Output:   a.Stdout,
	})
}

func (a *App) runCommand() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run measurements against one or more providers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.tests != "" && !cmd.Flags().Changed("measurements") {
				f.measurements = f.tests
			}
			return a.execute(cmd.Context(), f)
		},
	}
	fl := cmd.Flags()
	fl.StringSliceVarP(&f.providers, "provider", "p", nil, "provider(s), repeatable or comma-separated (default: cloudflare)")
	fl.BoolVarP(&f.all, "all", "a", false, "every provider")
	fl.StringVarP(&f.measurements, "measurements", "t", "all", "comma-separated subset of: latency,download,upload")
	fl.StringVar(&f.tests, "tests", "", "")
	_ = fl.MarkHidden("tests")
	fl.Float64VarP(&f.duration, "duration", "d", 10, "seconds per direction (HTTP providers)")
	fl.IntVarP(&f.connections, "connections", "c", 6, "parallel streams (HTTP providers)")
	fl.IntVar(&f.pings, "pings", 20, "idle latency samples")
	fl.BoolVar(&f.json, "json", false, "JSON to stdout instead of a table")
	fl.StringVar(&f.log, "log", "", "append each result as a JSON line to FILE")
	fl.IntVar(&f.repeat, "repeat", 1, "rounds to run; 0 = until Ctrl-C")
	fl.Float64Var(&f.interval, "interval", 300, "seconds between rounds")
	fl.BoolVarP(&f.quiet, "quiet", "q", false, "no progress output on stderr")
	fl.IntVar(&f.mlabServer, "mlab-server", 0, "index into M-Lab's nearest-server list (0 = closest)")
	fl.IntVar(&f.ooklaServer, "ookla-server-id", 0, "pin an Ookla server (see `speedtest -L`)")
	fl.BoolVar(&f.ooklaLicense, "accept-ookla-license", false, "pass --accept-license --accept-gdpr to Ookla's binary")
	return cmd
}

func (a *App) listCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show providers and whether each is usable on this machine",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return output.ProviderList(a.Stdout, provider.New(a.Version, provider.Config{}).All())
		},
	}
}

func (a *App) execute(ctx context.Context, f runFlags) error {
	reg := provider.New(a.Version, provider.Config{
		MLabServer:         f.mlabServer,
		OoklaServerID:      f.ooklaServer,
		AcceptOoklaLicense: f.ooklaLicense,
	})
	tokens := f.providers
	if f.all {
		tokens = append([]string{"all"}, tokens...)
	}
	providers, err := reg.Resolve(tokens)
	if err != nil {
		return err
	}
	if len(providers) == 0 {
		providers = reg.All()[:1]
	}
	measurements, err := provider.ParseMeasurements(f.measurements)
	if err != nil {
		return err
	}
	opts := engine.DefaultOptions()
	opts.Measurements = measurements
	opts.Duration = time.Duration(f.duration * float64(time.Second))
	opts.Connections = max(1, f.connections)
	opts.Pings = max(2, f.pings)

	progress := output.NewProgress(a.Stderr, f.quiet)
	code := ExitOK
	for round := 1; ; round++ {
		results := engine.RunAll(ctx, providers, opts, progress.Handle)
		progress.Done()
		if f.json {
			if err := output.JSON(a.Stdout, results, f.repeat == 1); err != nil {
				return err
			}
		} else if err := output.Table(a.Stdout, results, reg.Title, time.Now()); err != nil {
			return err
		}
		if f.log != "" {
			if err := output.AppendLog(f.log, results); err != nil {
				return fmt.Errorf("writing log: %w", err)
			}
		}
		for _, r := range results {
			if !r.OK {
				code = ExitFailed
			}
		}
		if ctx.Err() != nil {
			progress.Note("interrupted")
			return exitCode(ExitInterrupted)
		}
		if f.repeat > 0 && round >= f.repeat {
			break
		}
		progress.Note(fmt.Sprintf("round %d done; next in %.0fs (Ctrl-C to stop)", round, f.interval))
		select {
		case <-ctx.Done():
			progress.Note("interrupted")
			return exitCode(ExitInterrupted)
		case <-time.After(time.Duration(f.interval * float64(time.Second))):
		}
	}
	if code != ExitOK {
		return exitCode(code)
	}
	return nil
}
