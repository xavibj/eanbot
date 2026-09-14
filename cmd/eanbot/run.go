package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// version is printed by the "version" subcommand.
const version = "0.1.0"

// usageText documents every subcommand, as fixed by specs/005-cli.md.
const usageText = `Uso:
  eanbot crawl <url> [-db eanbot.db] [-max-pages 500] [-max-depth 10]
                     [-concurrency 4] [-delay 500ms] [-timeout 15s]
                     [-user-agent UA] [-include-subdomains] [-ignore-robots]
                     [-no-sitemaps] [-header "Nombre: valor"]... [-json] [-quiet]
  eanbot serve       [-db eanbot.db] [-addr :8345]
  eanbot crawls      [-db eanbot.db] [-json]
  eanbot pages <crawl-id> [-db eanbot.db] [-status 2xx|3xx|4xx|5xx|error|blocked] [-q texto] [-json]
  eanbot broken <crawl-id> [-db eanbot.db] [-json]
  eanbot version
  eanbot help | -h | --help
`

// run wires the CLI to a real OS process: it builds a context that is
// cancelled on SIGINT or SIGTERM and delegates everything else to runCtx.
func run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runCtx(ctx, args, stdout, stderr)
}

// runCtx implements every subcommand and never touches the process directly
// (no os.Args, no os.Stdout/os.Stderr, no signals), which makes it fully
// testable: pass context.Background() for the happy path, or cancel ctx
// (immediately or mid-flight, e.g. against a slow httptest.Server) to
// simulate Ctrl-C.
func runCtx(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	case "version":
		fmt.Fprintf(stdout, "eanbot %s\n", version)
		return 0
	case "crawl":
		return cmdCrawl(ctx, rest, stdout, stderr)
	case "serve":
		return cmdServe(ctx, rest, stdout, stderr)
	case "crawls":
		return cmdCrawls(rest, stdout, stderr)
	case "pages":
		return cmdPages(rest, stdout, stderr)
	case "broken":
		return cmdBroken(rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "error: comando desconocido: %s\n\n", cmd)
		fmt.Fprint(stderr, usageText)
		return 2
	}
}

// newFlagSet builds a FlagSet that writes its usage/error output to stderr
// and never calls os.Exit (flag.ContinueOnError): a parse error simply
// becomes a non-zero return from runCtx.
func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}
