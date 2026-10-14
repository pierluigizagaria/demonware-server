package guest

import (
	binary "encoding/binary"
	fmt "fmt"
	platformpkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform"
	ps4pkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform/ps4"
	io "io"
	strconv "strconv"
)

const GuestTrustID = "ctrnf:anonymous-guests:v1"

type GuestPlatform struct {
	Random io.Reader
}

func (p GuestPlatform) Authenticate(body []byte) (platformpkg.PlayerID, error) {
	if _, ok := ps4pkg.ReadPSNEnvelope(body); !ok || p.Random == nil {
		return platformpkg.PlayerID{}, fmt.Errorf("invalid guest admission")
	}
	// A fresh publisher identity for every authentication, independent of
	// client names, tokens, network addresses and any platform account.
	var raw [8]byte
	for attempt := 0; attempt < 4; attempt++ {
		if _, err := io.ReadFull(p.Random, raw[:]); err != nil {
			return platformpkg.PlayerID{}, fmt.Errorf("guest identity generation failed")
		}
		pid := binary.LittleEndian.Uint64(raw[:]) & ((1 << 63) - 1)
		if pid == 0 {
			continue
		}
		return platformpkg.PlayerID{
			PID: pid, Username: "G-" + strconv.FormatUint(pid, 36),
			Sub: "guest:" + strconv.FormatUint(pid, 10), Kind: "guest", Title: ps4pkg.CtrTitle,
		}, nil
	}
	return platformpkg.PlayerID{}, fmt.Errorf("guest identity generation failed")
}

func (GuestPlatform) AuthData(id platformpkg.PlayerID) map[string]string {
	return map[string]string{"extended_data": fmt.Sprintf("nextendo:guest:%d", id.PID)}
}
