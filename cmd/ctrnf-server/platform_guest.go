package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
	"github.com/pierluigizagaria/demonware-server/internal/config"
	"github.com/pierluigizagaria/demonware-server/internal/platform/guest"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
	"github.com/pierluigizagaria/demonware-server/internal/platform/steam"
)

func loadPublisherPlatform(c config.Config) (auth.PublisherPlatform, string, error) {
	switch c.Admission {
	case "guest":
		if !c.UnlockAll {
			return nil, "", fmt.Errorf("guest admission requires an unlock-all instance")
		}
		return guest.Platform{Random: rand.Reader}, guest.TrustID, nil
	case "shadnet":
		return ps4.LoadPSNPlatform(c.Shadnet, c.JWKSURL)
	case "steam":
		trust, err := steam.TrustID(c.SteamRealm)
		if err != nil {
			return nil, "", err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		helper, err := steam.StartHelper(ctx, c.SteamHelper, c.SteamLibrary)
		if err != nil {
			return nil, "", err
		}
		return &steam.Platform{Validator: helper, Realm: c.SteamRealm}, trust, nil
	default:
		return nil, "", fmt.Errorf("unknown publisher admission")
	}
}
