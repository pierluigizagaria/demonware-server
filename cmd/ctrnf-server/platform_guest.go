package main

import (
	"crypto/rand"
	"fmt"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
	"github.com/pierluigizagaria/demonware-server/internal/config"
	"github.com/pierluigizagaria/demonware-server/internal/platform/guest"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
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
	default:
		return nil, "", fmt.Errorf("unknown publisher admission")
	}
}
