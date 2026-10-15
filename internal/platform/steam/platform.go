package steam

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
)

type Validator interface {
	Validate(context.Context, string) (Validation, error)
	End(uint64)
	ServerID() uint64
	Close() error
}

type AdmissionStore interface {
	SteamTicketUsed([32]byte) (bool, error)
	RecordSteamAdmission([32]byte, uint64, string, time.Time) error
}

type Platform struct {
	Validator Validator
	Store     AdmissionStore
	Realm     string
}

var realmPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func TrustID(realm string) (string, error) {
	if !realmPattern.MatchString(realm) {
		return "", errors.New("Steam realm must contain 1-64 letters, digits, underscores or hyphens")
	}
	return "ctrnf:steam:480:5760:v1:" + realm, nil
}

func (p *Platform) Authenticate(body []byte) (platform.PlayerID, error) {
	fail := func() (platform.PlayerID, error) { return platform.PlayerID{}, errors.New("Steam admission refused") }
	value, ok := ps4.ReadPSNEnvelope(body)
	if !ok || p.Validator == nil || p.Store == nil {
		return fail()
	}
	token, err := ParseToken(value)
	if err != nil {
		return fail()
	}
	used, err := p.Store.SteamTicketUsed(token.Hash)
	if err != nil || used {
		return fail()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	validated, err := p.Validator.Validate(ctx, value)
	if err != nil {
		return fail()
	}
	if validated.Lease == 0 {
		return fail()
	}
	lease := platform.NewAdmission(func() { p.Validator.End(validated.Lease) })
	if validated.AccountID != token.AccountID || validated.OwnerID == 0 {
		lease.Close()
		return fail()
	}
	if err := p.Store.RecordSteamAdmission(token.Hash, token.AccountID, token.OnlineID, time.Now()); err != nil {
		lease.Close()
		return fail()
	}
	id := platform.PlayerID{PID: token.AccountID, Username: token.OnlineID,
		Sub: "steam:" + strconv.FormatUint(token.AccountID, 10), Kind: "steam", Title: ps4.CTRTitle, Admission: lease}
	return id, nil
}

func (p *Platform) AuthData(id platform.PlayerID) map[string]string {
	return map[string]string{"extended_data": fmt.Sprintf("nextendo:steam:%d", id.PID)}
}

func (p *Platform) SteamDiscovery() map[string]string {
	return map[string]string{"protocol": "osw1", "app_id": "480", "title_id": "5760",
		"realm": p.Realm, "server_id": strconv.FormatUint(p.Validator.ServerID(), 10)}
}

func (p *Platform) Close() error { return p.Validator.Close() }
