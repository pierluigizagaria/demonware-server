package ctr

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
)

func TestMarketplaceRejectsShortTaskHeaders(t *testing.T) {
	testPresence := presence.NewStore(nil)
	testCTR := &Services{Economy: &EconomyStore{Dir: t.TempDir()}, Stats: &StatsStore{Dir: t.TempDir()}}

	l := newUnitPeer(t, &testPeer{Presence: testPresence, services: testCTR})
	for _, task := range []byte{TaskGetInventoryPaginated, TaskPurchaseSKUs} {
		request := []byte{80, 3, task}
		for n := 0; n <= len(request); n++ {
			reply := l.onMarketplace(task, request[:n]).Reply
			if len(reply) != 14 || binary.LittleEndian.Uint32(reply[10:14]) != lobby.ErrUnhandled {
				t.Fatalf("task %d accepted %d-byte request", task, n)
			}
		}
	}
}

// The exact bytes Crash Team Racing sent for bdMarketplace::getBalancesV3 (service 80,
// task 245), captured from production on 2026-09-21.
const realBalancesRequest = "5003f51708110000000a0d6f6374616e655f7377697463681003"

func TestParseBalancesRequestFromRealCapture(t *testing.T) {
	payload, err := hex.DecodeString(realBalancesRequest)
	if err != nil {
		t.Fatal(err)
	}
	ctx, max := ParseBalancesRequest(payload)
	if ctx != "octane_switch" {
		t.Fatalf("context = %q, want %q", ctx, "octane_switch")
	}
	if max != 3 {
		t.Fatalf("maxNumResults = %d, want 3", max)
	}
}

// A reply the game cannot deserialize leaves Pit Stop with no currency, so check the
// wire shape: repeated field 1, each holding currency id, amount and kind.
func TestBalancesResponseShape(t *testing.T) {
	w := map[uint64]int64{10: 1234, 11: 0, 12: 7}
	sb := GetPlayerBalancesResponse(w, 3)

	var got []struct {
		id     uint64
		amount uint64
	}
	for i := 0; i < len(sb); {
		if sb[i] != 0x0A { // field 1, length delimited
			t.Fatalf("byte %d: tag %#x, want 0x0A", i, sb[i])
		}
		i++
		ln, next, err := demonware.ReadVarint(sb, i)
		if err != nil {
			t.Fatal(err)
		}
		i = next
		e := sb[i : i+int(ln)]
		i += int(ln)

		var id, amount uint64
		for j := 0; j < len(e); {
			field := e[j] >> 3
			j++
			v, n, err := demonware.ReadVarint(e, j)
			if err != nil {
				t.Fatal(err)
			}
			j = n
			switch field {
			case 1:
				id = v
			case 2:
				amount = v
			}
		}
		got = append(got, struct {
			id     uint64
			amount uint64
		}{id, amount})
	}

	if len(got) != 3 {
		t.Fatalf("%d balances, want 3", len(got))
	}
	// Amounts are demonware.ZigZag encoded; a raw varint arrives halved (10000 showed as 5000).
	if got[0].id != 10 || got[0].amount != demonware.ZigZag(1234) {
		t.Fatalf("first balance = %+v, want id 1 amount demonware.ZigZag(1234)=%d", got[0], demonware.ZigZag(1234))
	}
	if got[2].id != 12 || got[2].amount != demonware.ZigZag(7) {
		t.Fatalf("third balance = %+v, want id 3 amount demonware.ZigZag(7)=%d", got[2], demonware.ZigZag(7))
	}
}

// The response is capped at 16 entries: bdGetPlayerBalancesResponse::deserialize stops
// reading past the sixteenth and the rest would be silently dropped.
func TestBalancesResponseCap(t *testing.T) {
	t.Setenv("CTR_CURRENCY_IDS", "1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18")
	sb := GetPlayerBalancesResponse(map[uint64]int64{}, 0)
	n := 0
	for i := 0; i < len(sb); {
		i++
		ln, next, err := demonware.ReadVarint(sb, i)
		if err != nil {
			t.Fatal(err)
		}
		i = next + int(ln)
		n++
	}
	if n != 3 {
		t.Fatalf("%d entries, want the three catalog currencies", n)
	}
}

// Both achievements responses end on a string read and return its result, so a missing
// string field fails the whole deserialize. That is what broke the Pit Stop refresh.
func TestAchievementsRepliesCarryTheirStringField(t *testing.T) {
	testPresence := presence.NewStore(nil)
	testCTR := &Services{Economy: &EconomyStore{Dir: t.TempDir()}, Stats: &StatsStore{Dir: t.TempDir()}}

	l := newUnitPeer(t, &testPeer{Presence: testPresence, services: testCTR})

	states := l.onAchievements(TaskGetAchievementStates, nil).Reply
	if !hasField(structBodyOf(t, states), 2) {
		t.Fatal("getAchievementStates reply lacks field 2, deserialize would return false")
	}

	user := l.onAchievements(TaskGetUserState, nil).Reply
	if !hasField(structBodyOf(t, user), 1) {
		t.Fatal("getUserState reply lacks field 1, CAEUserStateRefreshed never fires")
	}
}

// structBodyOf pulls the struct buffer back out of a task reply.
func structBodyOf(t *testing.T, reply []byte) []byte {
	t.Helper()
	const off = 1 + 8 + 1 + 4 // u64 transaction, u32 error
	if reply[off] != demonware.TagUint8 || reply[off+2] != demonware.TagStruct {
		t.Fatalf("not a struct reply: % x", reply[:off+3])
	}
	return reply[off+3+5:]
}

func hasField(sb []byte, want byte) bool {
	for i := 0; i < len(sb); {
		field, wire := sb[i]>>3, sb[i]&7
		i++
		if wire != 2 {
			return false
		}
		ln, next, err := demonware.ReadVarint(sb, i)
		if err != nil {
			return false
		}
		if field == want {
			return true
		}
		i = next + int(ln)
	}
	return false
}

// A real purchaseSkus (task 154) request captured from production on 2026-09-21. The sku
// is the SECOND u32: the first is the count.
const realPurchase = "50039a106f6374616e655f73776974636800080100000008950f030008010000000801000000010008010000000a00000000000000001000080000000008000000000300080000000000"

func TestParsePurchaseSkuFromRealCapture(t *testing.T) {
	payload, err := hex.DecodeString(realPurchase)
	if err != nil {
		t.Fatal(err)
	}
	// The PS4 build sends the same request with its own context (ps4PurchaseRequest).
	payload = bytes.Replace(payload, []byte("octane_switch"), []byte(ps4.MarketplaceContext), 1)
	sku, ok := ParsePurchaseSKU(payload)
	if !ok {
		t.Fatal("could not read the sku out of a real purchase request")
	}
	if sku != 0x00030f95 {
		t.Fatalf("sku = %d (%#x), want 200085 (0x30f95)", sku, sku)
	}
}
