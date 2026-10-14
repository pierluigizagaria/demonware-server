package main

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
)

// Admission is selected once per instance, never as a verification fallback.
type publisherPlatform interface {
	Authenticate([]byte) (playerID, error)
	AuthData(playerID) map[string]string
}

const guestTrustID = "ctrnf:anonymous-guests:v1"

type guestPlatform struct {
	random io.Reader
}

func (p guestPlatform) Authenticate(body []byte) (playerID, error) {
	if _, ok := readPSNEnvelope(body); !ok || p.random == nil {
		return playerID{}, fmt.Errorf("invalid guest admission")
	}
	// A fresh publisher identity for every authentication, independent of
	// client names, tokens, network addresses and any platform account.
	var raw [8]byte
	for attempt := 0; attempt < 4; attempt++ {
		if _, err := io.ReadFull(p.random, raw[:]); err != nil {
			return playerID{}, fmt.Errorf("guest identity generation failed")
		}
		pid := binary.LittleEndian.Uint64(raw[:]) & ((1 << 63) - 1)
		if pid == 0 {
			continue
		}
		return playerID{
			PID: pid, Username: "G-" + strconv.FormatUint(pid, 36),
			Sub: "guest:" + strconv.FormatUint(pid, 10), Kind: "guest", Title: ctrTitle,
		}, nil
	}
	return playerID{}, fmt.Errorf("guest identity generation failed")
}

func (guestPlatform) AuthData(id playerID) map[string]string {
	return map[string]string{"extended_data": fmt.Sprintf("nextendo:guest:%d", id.PID)}
}

func loadPublisherPlatform(c serverConfig) (publisherPlatform, string, error) {
	switch c.admission {
	case "guest":
		if !c.unlockAll {
			return nil, "", fmt.Errorf("guest admission requires an unlock-all instance")
		}
		return guestPlatform{random: rand.Reader}, guestTrustID, nil
	case "shadnet":
		return loadPSNPlatform(c.shadnet, c.jwksURL)
	default:
		return nil, "", fmt.Errorf("unknown publisher admission")
	}
}
