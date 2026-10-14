package auth

import (
	"net/http"

	"github.com/pierluigizagaria/demonware-server/internal/config"
	"github.com/pierluigizagaria/demonware-server/internal/dashboard"
	"github.com/pierluigizagaria/demonware-server/internal/diagnostics"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
)

func testAuthHandler(tickets *TicketStore, p PublisherPlatform, s *CTRSigner, dumps string) http.Handler {
	return NewPlatformAuthHandler(&diagnostics.Diagnostics{RedactLogs: true}, dashboard.NewTelemetry(config.Config{}, func() int64 { return 0 }), tickets, presence.NewStore(nil), p, s, dumps, nil)
}
