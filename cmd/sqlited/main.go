// Command sqlited is a lightweight, single-binary HTTP daemon that exposes
// local SQLite databases via a Cloudflare D1-compatible JSON API.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/kiddyuchina/sqlited/internal/config"
	"github.com/kiddyuchina/sqlited/internal/server"
)

func main() {
	log.SetFlags(log.LstdFlags)

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	cmd := os.Args[1]
	switch cmd {
	case "init":
		if err := config.Init(config.DefaultConfigFile); err != nil {
			log.Fatalf("init failed: %v", err)
		}
	case "run", "serve":
		if err := runServer(); err != nil {
			log.Fatalf("server error: %v", err)
		}
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %q\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`sqlited - a single-binary HTTP daemon for SQLite with a Cloudflare D1-compatible API.

Usage:

  sqlited init        Generate a sqlited.json config in the current directory
  sqlited run         Start the HTTP server (reads sqlited.json by default)
  sqlited serve       Alias for run
  sqlited help        Show this help

Flags for run/serve:

  -config string      Path to the configuration file (default "sqlited.json")
`)
}

func runServer() error {
	configPath := flag.String("config", config.DefaultConfigFile, "path to the sqlited configuration file")
	flag.CommandLine.Parse(os.Args[2:])

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w\nRun `sqlited init` to generate one.", err)
	}

	srv, err := server.NewServer(cfg)
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}
	defer srv.Close()

	addr := fmt.Sprintf(":%d", cfg.Port)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("sqlited listening on %s with %d database(s)", addr, len(cfg.Databases))
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
