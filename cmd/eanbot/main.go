// Command eanbot is the CLI for eanbot: it can run a one-off crawl, serve
// the web UI and REST API, or query a previous crawl's results. All the
// logic lives in run (and the functions it calls), which never touches
// os.Args/os.Stdout/os.Stderr/os.Exit directly, so it is fully testable.
package main

import "os"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
