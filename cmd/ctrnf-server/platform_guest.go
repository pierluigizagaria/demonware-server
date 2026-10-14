package main

import (
	rand "crypto/rand"
	fmt "fmt"
	authpkg "github.com/NextendoNetwork/nextendo-demonware/internal/auth"
	configpkg "github.com/NextendoNetwork/nextendo-demonware/internal/config"
	guestpkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform/guest"
	ps4pkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform/ps4"
)

func loadPublisherPlatform(c configpkg.ServerConfig) (authpkg.PublisherPlatform, string, error) {
	switch c.Admission {
	case "guest":
		if !c.UnlockAll {
			return nil, "", fmt.Errorf("guest admission requires an unlock-all instance")
		}
		return guestpkg.GuestPlatform{Random: rand.Reader}, guestpkg.GuestTrustID, nil
	case "shadnet":
		return ps4pkg.LoadPSNPlatform(c.Shadnet, c.JwksURL)
	default:
		return nil, "", fmt.Errorf("unknown publisher admission")
	}
}
