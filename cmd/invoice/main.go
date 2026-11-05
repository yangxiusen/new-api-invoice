package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"invoice/internal/config"
	"invoice/internal/files"
	"invoice/internal/server"
	"invoice/internal/store"
	"invoice/web"
)

func main() {
	if e := run(); e != nil {
		slog.Error("invoice command failed", "error", e.Error())
		os.Exit(1)
	}
}
func run() error {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command != "serve" && command != "preflight" && command != "migrate" && command != "init-permissions" && command != "reconcile-files" {
		return fmt.Errorf("command must be serve, preflight, migrate, init-permissions, or reconcile-files")
	}
	cfg, e := config.Load(os.Getenv)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	poolCfg, e := pgxpool.ParseConfig(cfg.DatabaseURL)
	if e != nil {
		return fmt.Errorf("invalid DATABASE_URL")
	}
	poolCfg.MaxConns = 16
	poolCfg.ConnConfig.ConnectTimeout = 5 * time.Second
	poolCfg.ConnConfig.RuntimeParams["search_path"] = "public"
	poolCfg.ConnConfig.RuntimeParams["application_name"] = "invoice-v0.1"
	poolCfg.ConnConfig.RuntimeParams["statement_timeout"] = "15000"
	db, e := pgxpool.NewWithConfig(ctx, poolCfg)
	if e != nil {
		return fmt.Errorf("database connection failed")
	}
	defer db.Close()
	switch command {
	case "migrate":
		return store.Migrate(ctx, db)
	case "preflight":
		return store.Preflight(ctx, db)
	case "init-permissions":
		flags := flag.NewFlagSet(command, flag.ContinueOnError)
		dry := flags.Bool("dry-run", false, "report only")
		global := flags.String("legacy-global", "", "confirmed true/false override")
		if e = flags.Parse(os.Args[2:]); e != nil {
			return e
		}
		var override *bool
		if *global != "" {
			v, e := strconv.ParseBool(*global)
			if e != nil || (*global != "true" && *global != "false") {
				return fmt.Errorf("legacy-global requires true/false")
			}
			override = &v
		}
		stats, e := store.Initialize(ctx, db, override, *dry)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(stats)
	case "reconcile-files":
		fs, e := files.New(cfg.FileRoot)
		if e != nil {
			return e
		}
		defer fs.Close()
		paths, e := fs.Paths()
		if e != nil {
			return e
		}
		rows, e := db.Query(ctx, `SELECT id::text,invoice_file_path FROM invoice_applications WHERE coalesce(invoice_file_path,'')<>''`)
		if e != nil {
			return e
		}
		type missing struct {
			ApplicationID string `json:"application_id"`
			Path          string `json:"path"`
		}
		absent := []missing{}
		refs := map[string]bool{}
		for rows.Next() {
			var id, path string
			if e = rows.Scan(&id, &path); e != nil {
				rows.Close()
				return e
			}
			refs[path] = true
			f, e := fs.Open(path)
			if e != nil {
				absent = append(absent, missing{id, path})
			} else {
				f.Close()
			}
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		unreferenced := []string{}
		for _, path := range paths {
			if !refs[path] {
				unreferenced = append(unreferenced, path)
			}
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"read_only": true, "unreferenced": unreferenced, "missing_or_unsafe": absent})
	}
	if cfg.AutoMigrate {
		startup, cancel := context.WithTimeout(ctx, 60*time.Second)
		e = store.Ensure(startup, db, cfg.MigrationURL)
		cancel()
		if e != nil {
			return e
		}
	}
	if e = store.Preflight(ctx, db); e != nil {
		return e
	}
	if e = store.Ready(ctx, db); e != nil {
		return e
	}
	gin.SetMode(gin.ReleaseMode)
	handler, e := server.New(db, cfg, web.Assets())
	if e != nil {
		return fmt.Errorf("file root unavailable")
	}
	defer handler.Close()
	httpServer := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 90 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	done := make(chan error, 1)
	go func() { done <- httpServer.ListenAndServe() }()
	slog.Info("invoice listening", "address", cfg.Listen)
	select {
	case e := <-done:
		if e != http.ErrServerClosed {
			return e
		}
		return nil
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdown)
}
