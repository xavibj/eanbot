package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"xavi.net/eanbot/server"
	"xavi.net/eanbot/store"
)

// shutdownGrace is how long serve waits for in-flight requests (and running
// crawls) to finish once SIGINT/SIGTERM cancels the context.
const shutdownGrace = 10 * time.Second

// listenAndServe runs h on addr until ctx is cancelled, at which point it
// shuts the HTTP server down gracefully (shutdownGrace) before returning.
// It is a package variable, overridden in tests, so cmdServe can be
// exercised without opening any real socket.
var listenAndServe = func(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		<-errCh // ListenAndServe returns http.ErrServerClosed once Shutdown completes
		return nil
	}
}

// cmdServe implements "eanbot serve [flags]": it opens the store, builds
// the server.Server, prints the listening banner and blocks in
// listenAndServe until ctx is cancelled (SIGINT/SIGTERM), then shuts down
// both the HTTP server and any crawl still running in the background.
func cmdServe(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("serve", stderr)
	dbPath := fs.String("db", "eanbot.db", "ruta de la base de datos SQLite")
	addr := fs.String("addr", ":8345", "dirección de escucha")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "error: argumentos no reconocidos: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	defer st.Close()

	srv := server.New(st, server.Options{Logger: log.New(stderr, "", log.LstdFlags)})

	display := *addr
	if strings.HasPrefix(*addr, ":") {
		display = "localhost" + *addr
	}
	fmt.Fprintf(stdout, "eanbot escuchando en http://%s\n", display)

	serveErr := listenAndServe(ctx, *addr, srv.Handler())

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
	}

	if serveErr != nil && serveErr != http.ErrServerClosed {
		fmt.Fprintf(stderr, "error: %v\n", serveErr)
		return 1
	}
	return 0
}
