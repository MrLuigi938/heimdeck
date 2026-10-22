// Heimdeck – a simple, good-looking dashboard for your home server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/docker"
	"github.com/heimdeck/heimdeck/internal/logging"
	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/server"
	"github.com/heimdeck/heimdeck/internal/store"
	"github.com/heimdeck/heimdeck/web"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// version is set at build time (-ldflags "-X main.version=…").
var version = "dev"

func main() {
	// heimdeck healthcheck – used by Docker's HEALTHCHECK (the image has no curl).
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		healthcheck(env("HEIMDECK_ADDR", ":3000"))
		return
	}
	logging.Setup()
	dataDir := env("HEIMDECK_DATA", "/data")
	addr := env("HEIMDECK_ADDR", ":3000")

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		log.Fatalf("Datenordner %s: %v", dataDir, err)
	}
	box, err := secret.Open(dataDir)
	if err != nil {
		log.Fatalf("Schlüssel: %v", err)
	}
	opened := time.Now()
	st, err := store.Open(filepath.Join(dataDir, "heimdeck.db"), box)
	if err != nil {
		log.Fatalf("Datenbank: %v", err)
	}
	defer st.Close()
	if d := time.Since(opened); d > time.Second {
		slog.Warn("Datenbank brauchte lange zum Öffnen", "dauer", d.Round(time.Millisecond))
	}
	if settings, err := st.Settings(); err == nil {
		logging.SetDebug(settings["debug"] == "true")
	}

	// heimdeck reset-password <neues-passwort>
	if len(os.Args) > 1 && os.Args[1] == "reset-password" {
		resetPassword(st)
		return
	}

	a := auth.New(st, env("HEIMDECK_SECURE_COOKIE", "") == "true")
	dock := docker.New()
	app := server.New(st, a, dock, web.FS())
	app.LogStartup(version, dataDir, addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go app.Monitor(ctx)
	go app.Watch(ctx)
	go func() {
		slog.Info("Heimdeck läuft", "adresse", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	slog.Info("Heimdeck wird beendet")
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdown)
}

func healthcheck(addr string) {
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/api/state")
	if err != nil || resp.StatusCode != http.StatusOK {
		os.Exit(1)
	}
}

func resetPassword(st *store.Store) {
	if len(os.Args) < 3 || len(os.Args[2]) < 8 {
		fmt.Println("Aufruf: heimdeck reset-password <neues-passwort>  (mindestens 8 Zeichen)")
		os.Exit(1)
	}
	name, err := st.FirstUsername()
	if err != nil {
		fmt.Println("Es gibt noch keinen Benutzer – einfach die Weboberfläche öffnen.")
		os.Exit(1)
	}
	hash, _ := auth.HashPassword(os.Args[2])
	if err := st.SetPassword(name, hash); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Passwort für „%s“ wurde geändert.\n", name)
}
