// CTR:NF PS4 publisher server derived from Nextendo. One executable composes
// authentication, lobby tasks, NAT, optional relay and the dashboard API.
// Settings retain their existing names and validation; see example.env.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
	"github.com/pierluigizagaria/demonware-server/internal/config"
	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/store"
)

func main() {
	log.SetOutput(os.Stdout)
	envFile := flag.String("env", "", "load this instance's settings from a KEY=VALUE `file` (see example.env); variables already in the environment take precedence")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: demonware-server [-env file] [pubfiles]")
		flag.PrintDefaults()
	}
	flag.Parse()
	pubfilesOnly := flag.NArg() == 1 && flag.Arg(0) == "pubfiles"
	if flag.NArg() > 0 && !pubfilesOnly {
		flag.Usage()
		os.Exit(2)
	}
	cfg, err := config.Read(*envFile, !pubfilesOnly)
	if err != nil {
		log.Printf("[server] configuration:\n%v", err)
		os.Exit(1)
	}
	if pubfilesOnly {
		log.Print("[diablo] pubfiles is inactive in this CTR:NF server; no files generated")
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runServer(ctx, cfg); err != nil {
		log.Printf("[server] %v", err)
		// A drain timeout MUST terminate the process. No deferred database close
		// or unlock may run while unfinished workers still exist.
		os.Exit(1)
	}
}

const shutdownLimit = 30 * time.Second

func runServer(ctx context.Context, cfg config.Config) error {
	var resources drainPlan
	finish := func(cause error) error { return errors.Join(cause, drainWithin(shutdownLimit, resources)) }
	platform, trustID, err := loadPublisherPlatform(cfg)
	if err != nil {
		return fmt.Errorf("publisher admission: %w", err)
	}
	if closer, ok := platform.(io.Closer); ok {
		resources.closePlatform = closer.Close
	}
	log.Printf("[server] admission: %s", cfg.Admission)
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return finish(err)
	}
	database, err := store.OpenSQLite(filepath.Join(cfg.DataDir, store.DBFile), cfg.UnlockAll, trustID)
	if err != nil {
		return finish(fmt.Errorf("CTR database: %w", err))
	}
	resources.closeDatabase = database.Close
	i := newInstance(cfg, database)
	log.Printf("[server] unlock-all: %v", cfg.UnlockAll)
	resources.admission = append(resources.admission, i.lobby.BeginShutdown)
	resources.stop = append(resources.stop, i.registry.StopReaper, i.nat.Close, i.relay.Close)
	resources.wait = append(resources.wait,
		func() error { i.lobby.Wait(); return nil },
		func() error { i.registry.WaitReaper(); return nil },
		func() error { i.nat.Wait(); return nil },
		func() error { i.relay.Wait(); return nil })

	dashboard, err := i.monitor.StartDashboard(newDashboardViews(i.presence, i.matchmaker, i.registry), i.diagnostics)
	if err != nil {
		return finish(fmt.Errorf("dashboard: %w", err))
	}
	resources.admission = append(resources.admission, dashboard.BeginShutdown)
	resources.wait = append(resources.wait, dashboard.Wait)
	if err := i.nat.Start(cfg.NATPort); err != nil {
		return finish(fmt.Errorf("NAT: %w", err))
	}
	i.relay.Start(cfg.RelayLow, cfg.RelayHigh, cfg.PublicIP, i.presence.OnlinePIDs)
	i.registry.StartReaper()
	if err := i.lobby.Start(); err != nil {
		return finish(err)
	}
	authServer, err := auth.StartAuth(platform, i.tickets, i.presence, cfg, i.diagnostics, i.monitor, ctr.NewUmbrellaHandler(i.diagnostics, i.tickets))
	if err != nil {
		return finish(fmt.Errorf("auth: %w", err))
	}
	resources.admission = append(resources.admission, authServer.BeginShutdown)
	resources.wait = append(resources.wait, authServer.Wait)

	select {
	case <-ctx.Done():
		log.Print("[server] operator shutdown: draining admitted work")
		return finish(nil)
	case <-authServer.Done():
		return finish(fmt.Errorf("auth stopped: %w", authServer.Err()))
	case <-dashboard.Done():
		return finish(fmt.Errorf("dashboard stopped: %w", dashboard.Err()))
	}
}
