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
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
	"github.com/pierluigizagaria/demonware-server/internal/companion"
	"github.com/pierluigizagaria/demonware-server/internal/config"
	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/platform/local"
	"github.com/pierluigizagaria/demonware-server/internal/platform/steam"
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
	if cfg.Admission == "local" {
		log.SetOutput(os.Stderr)
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
	var pipe *companion.Pipe
	var platform auth.PublisherPlatform
	var trustID string
	var ownerPID uint64
	var virtual *local.VirtualTransport
	var err error
	if cfg.Admission == "local" {
		pipe = companion.New(ctx, os.Stdin, os.Stdout)
		defer func() { _ = pipe.Close(); pipe.Wait() }()
		owner, ownerErr := pipe.Owner(ctx)
		if ownerErr != nil {
			return ownerErr
		}
		localPlatform, localErr := local.NewWithWeakToken(owner, cfg.LocalWeakToken)
		if localErr != nil {
			return localErr
		}
		platform, trustID = localPlatform, localPlatform.TrustID()
		ownerPID = localPlatform.PID()
		virtual = owner.Transport
		if virtual != nil && cfg.Matchmaking != "steam" {
			return errors.New("virtual transport requires peer matchmaking")
		}
	} else {
		platform, trustID, err = loadPublisherPlatform(cfg)
	}
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
	if steamPlatform, ok := platform.(*steam.Platform); ok {
		steamPlatform.Store = database
	}
	i := newInstance(cfg, database)
	if owner, ok := platform.(*local.Platform); ok {
		if err := i.presence.SetLocalOwnerName(owner.Current(), time.Now()); err != nil {
			log.Printf("[server] local owner name persistence unavailable")
		}
		pipe.EnableOwnerNames(func(pid, name string) error {
			id, err := owner.Rename(pid, name)
			if err != nil {
				return err
			}
			if err := i.presence.SetLocalOwnerName(id, time.Now()); err != nil {
				log.Printf("[server] local owner name persistence unavailable")
			}
			return nil
		})
	}
	if cfg.Matchmaking == "steam" {
		var canonical netip.AddrPort
		if virtual != nil {
			game, endpoint, err := virtual.Endpoints()
			if err != nil {
				return finish(err)
			}
			if err := i.nat.SetLocalVirtualEndpoint(game, endpoint); err != nil {
				return finish(err)
			}
			canonical = endpoint
			if virtual.Mode == "loopback-v1" {
				pipe.EnableLoopbackEndpoints()
			} else {
				pipe.EnableVirtualEndpoints()
			}
		}
		var timing *ctr.P2PTiming
		if cfg.P2PTiming {
			timing = ctr.NewP2PTiming(os.Stderr)
		}
		backend, err := ctr.InstallP2P(ctx, i.lobby.Table, pipe, ownerPID, canonical, timing)
		if err != nil {
			return finish(err)
		}
		resources.stop = append(resources.stop, backend.Close)
		resources.wait = append(resources.wait, func() error { backend.Wait(); return nil })
	}
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
	var companionDone <-chan struct{}
	if pipe != nil {
		if err := pipe.Ready(ctx); err != nil {
			return finish(err)
		}
		companionDone = pipe.Done()
	}

	select {
	case <-companionDone:
		log.Printf("[server] companion closed: draining admitted work (reason=%s)", pipe.CloseReason())
		return finish(nil)
	case <-ctx.Done():
		log.Print("[server] operator shutdown: draining admitted work")
		return finish(nil)
	case <-authServer.Done():
		return finish(fmt.Errorf("auth stopped: %w", authServer.Err()))
	case <-dashboard.Done():
		return finish(fmt.Errorf("dashboard stopped: %w", dashboard.Err()))
	}
}
