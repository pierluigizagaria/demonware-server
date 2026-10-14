package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"os"
	"testing"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

// Synthetic request derived from the original PS4 0xac1e70 -> 0x97410 path,
// including all zero/default SDK options and the task terminator.
const ps4PurchaseRequest = "50039a106f6374616e655f70736e00080100000008950f030008010000000801000000010008010000000a00000000000000001000080000000008000000000300080000000000"
const ps4BalancesRequest = "5003f517080e0000000a0a6f6374616e655f70736e100300"

func marketplaceTestStore(t *testing.T) *economyStore {
	t.Helper()
	old := economy
	economy = &economyStore{dir: t.TempDir()}
	t.Cleanup(func() { economy = old })
	setStartBalance(t, 10000)
	return economy
}

func marketplaceHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func marketplaceOutcome(t *testing.T, reply []byte, want uint32) {
	t.Helper()
	r := demonware.NewReader(reply)
	if _, err := r.Uint64(); err != nil {
		t.Fatal(err)
	}
	if code, err := r.Uint32(); err != nil || code != want {
		t.Fatalf("task error = %d, %v; want %d", code, err, want)
	}
	if want != 0 && r.Position() != len(reply) {
		t.Fatal("error reply contains success data")
	}
}

func marketplaceNoFiles(t *testing.T, s *economyStore) {
	t.Helper()
	entries, err := os.ReadDir(s.dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected request wrote ledger files: %v, %v", entries, err)
	}
}

func TestPlatformMarketplaceBalances(t *testing.T) {
	marketplaceTestStore(t)
	l, wire := sharedPS4Lobby()
	payload := marketplaceHex(t, ps4BalancesRequest)
	ctx, max := parseBalancesRequest(payload)
	if ctx != "octane_psn" || max != 3 {
		t.Fatalf("balances request = %q, %d", ctx, max)
	}
	reply := sharedTask(t, l, wire, payload)
	marketplaceOutcome(t, reply, 0)
	want := getPlayerBalancesResponse(map[uint64]int64{10: 10000}, 3)
	if !bytes.Equal(structBodyOf(t, reply), want) {
		t.Fatal("wrong persisted balances reply")
	}
	for _, max := range []byte{1, 16} {
		payload[len(payload)-2] = max
		reply = sharedTask(t, l, wire, payload)
		marketplaceOutcome(t, reply, 0)
		if !bytes.Equal(structBodyOf(t, reply), getPlayerBalancesResponse(map[uint64]int64{10: 10000}, int(max))) {
			t.Fatal("maxNumResults not honored")
		}
	}
}

// The Switch context is refused on every Pit Stop task.
func TestPlatformMarketplaceRejectsWrongContexts(t *testing.T) {
	for _, task := range []byte{taskGetBalancesV3, taskGetInventoryPaginated, taskPurchaseSkus} {
		s := marketplaceTestStore(t)
		l, wire := sharedPS4Lobby()
		w := demonware.NewWriter([]byte{svcMarketplace, demonware.TagUint8, task})
		switch task {
		case taskGetBalancesV3:
			w.Struct(demonware.AppendVarintField(demonware.AppendBytesField(nil, 1, []byte("octane_switch")), 2, 3))
			w.Raw([]byte{0})
		case taskGetInventoryPaginated:
			w.String("octane_switch")
			w.Uint32(1)
			w.Uint32(16)
		case taskPurchaseSkus:
			w = demonware.NewWriter(marketplaceHex(t, realPurchase))
		}
		marketplaceOutcome(t, sharedTask(t, l, wire, w.Bytes()), errUnhandled)
		marketplaceNoFiles(t, s)
	}
}

func TestPS4MarketplaceRejectsUnsupportedTasksWithoutWrite(t *testing.T) {
	s := marketplaceTestStore(t)
	l, wire := sharedPS4Lobby()
	// Exercise every unknown selector, including 146, 42 and 81, plus the
	// Switch-only 246. Use purchase-shaped arguments to catch misrouting.
	payload := marketplaceHex(t, ps4PurchaseRequest)
	check := func() {
		for task := 0; task <= 255; task++ {
			if task == taskGetBalancesV3 || task == taskGetInventoryPaginated || task == taskPurchaseSkus {
				continue
			}
			payload[2] = byte(task)
			marketplaceOutcome(t, sharedTask(t, l, wire, payload), errUnhandled)
		}
		payload[1] = demonware.TagBool
		marketplaceOutcome(t, sharedTask(t, l, wire, payload), errUnhandled)
		payload[1] = demonware.TagUint8
	}
	check()
	marketplaceNoFiles(t, s)
	if _, err := s.purchase(l.pid(), []uint32{200085}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.accountPath(l.pid()))
	if err != nil {
		t.Fatal(err)
	}
	check()
	after, err := os.ReadFile(s.accountPath(l.pid()))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("unsupported task changed an existing ledger")
	}
	if l.afterReply != nil {
		t.Fatal("unsupported task scheduled a push")
	}
}

func TestPS4MarketplaceRejectsNondefaultPurchaseOptions(t *testing.T) {
	s := marketplaceTestStore(t)
	l, wire := sharedPS4Lobby()
	payload := marketplaceHex(t, ps4PurchaseRequest)
	// The first 35 bytes contain context and the two one-element arrays.
	// Mutate every trailing byte, including tags, counts, recipient, options
	// and terminator; also reject every truncation and appended data.
	for i := 35; i < len(payload); i++ {
		bad := bytes.Clone(payload)
		bad[i] ^= 1
		marketplaceOutcome(t, sharedTask(t, l, wire, bad), errUnhandled)
	}
	for n := 3; n < len(payload); n++ {
		marketplaceOutcome(t, sharedTask(t, l, wire, payload[:n]), errUnhandled)
	}
	for _, bad := range [][]byte{append(bytes.Clone(payload), 0), append(bytes.Clone(payload), 1)} {
		marketplaceOutcome(t, sharedTask(t, l, wire, bad), errUnhandled)
	}
	marketplaceNoFiles(t, s)
}

func TestPS4MarketplaceRejectsMalformedBalances(t *testing.T) {
	s := marketplaceTestStore(t)
	l, wire := sharedPS4Lobby()
	payload := marketplaceHex(t, ps4BalancesRequest)
	for _, max := range []byte{0, 17, 255} {
		bad := bytes.Clone(payload)
		bad[len(bad)-2] = max
		marketplaceOutcome(t, sharedTask(t, l, wire, bad), errUnhandled)
	}
	for n := 3; n < len(payload); n++ {
		marketplaceOutcome(t, sharedTask(t, l, wire, payload[:n]), errUnhandled)
	}
	marketplaceOutcome(t, sharedTask(t, l, wire, append(payload, 0)), errUnhandled)
	badLength := demonware.NewWriter([]byte{svcMarketplace, demonware.TagUint8, taskGetBalancesV3})
	badLength.Struct(demonware.AppendVarint([]byte{0x0a}, ^uint64(0)))
	marketplaceOutcome(t, sharedTask(t, l, wire, badLength.Bytes()), errUnhandled)
	marketplaceNoFiles(t, s)
}

// Decode separate encrypted frames so the purchase reply and push order are
// checked through the real dispatcher without opening a listener.
func marketplaceFrames(t *testing.T, l *lobbyConn, wire *platformWire) [][]byte {
	t.Helper()
	var frames [][]byte
	var previous uint32
	for raw := wire.Bytes(); len(raw) != 0; {
		if len(raw) < 4 {
			t.Fatal("truncated frame")
		}
		size := int(binary.LittleEndian.Uint32(raw)) + 4
		if size < 50 || size > len(raw) {
			t.Fatal("bad frame length")
		}
		frame, err := demonware.ParseEncryptedFrame(raw[:size])
		if err != nil || !frame.Verify(l.keys.S2CMAC) {
			t.Fatal("invalid frame or server MAC")
		}
		if len(frames) != 0 && frame.Sequence != previous+1 {
			t.Fatal("nonconsecutive server sequence")
		}
		previous = frame.Sequence
		message, err := frame.Decrypt(l.keys.S2CAES)
		if err != nil {
			t.Fatal("invalid encrypted reply")
		}
		frames = append(frames, append([]byte{message.Type}, message.Payload...))
		raw = raw[size:]
	}
	if len(frames) != 0 && previous != l.s2cSeq {
		t.Fatal("last server sequence not recorded")
	}
	return frames
}

func TestPlatformMarketplacePurchaseInventoryAndBalancePush(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		title                   uint32
		request, context, owner string
	}{
		{"PS4", 5760, ps4PurchaseRequest, "octane_psn", "psn"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := marketplaceTestStore(t)
			l, wire := sharedPS4Lobby()
			l.player.Title = tc.title
			l.onTask(marketplaceHex(t, tc.request))
			frames := marketplaceFrames(t, l, wire)
			if len(frames) != 2 || frames[0][0] != demonware.InnerTaskReply || frames[1][0] != demonware.InnerPush {
				t.Fatal("purchase must reply before balance push")
			}
			marketplaceOutcome(t, frames[0][1:], 0)
			wantBalance := int64(10000) - economyCatalog[200597].Price
			// Check the entire push, including nested owner identity and demonware.ZigZag amount.
			owner := demonware.AppendVarintField(nil, 1, l.pid())
			owner = demonware.AppendBytesField(owner, 2, []byte(tc.owner))
			entry := demonware.AppendBytesField(nil, 1, owner)
			entry = demonware.AppendVarintField(entry, 2, 10)
			entry = demonware.AppendVarintField(entry, 3, 0xffffffff)
			entry = demonware.AppendVarintField(entry, 4, 0)
			entry = demonware.AppendVarintField(entry, 5, demonware.ZigZag(wantBalance))
			push := &demonware.Writer{}
			push.Uint32(76)
			push.Struct(demonware.AppendBytesField(nil, 1, entry))
			if !bytes.Equal(frames[1][1:], push.Bytes()) {
				t.Fatal("wrong balance push owner or amount")
			}
			// Reload through a fresh store to prove the shared purchase was persisted.
			a, err := (&economyStore{dir: s.dir}).snapshot(l.pid())
			if err != nil || a.Balances[10] != wantBalance || !ownsSKU(a, economyCatalog[200597]) {
				t.Fatal("purchase debit/grant not persisted")
			}
			w := demonware.NewWriter([]byte{svcMarketplace, demonware.TagUint8, taskGetInventoryPaginated})
			w.String(tc.context)
			w.Uint32(1)
			w.Uint32(16)
			w.Raw([]byte{0})
			reply := sharedTask(t, l, wire, w.Bytes())
			marketplaceOutcome(t, reply, 0)
			r := demonware.NewReader(reply[14:])
			task, e1 := r.Uint8()
			count, e2 := r.Uint32()
			total, e3 := r.Uint32()
			pid, e4 := r.Uint64()
			kind, e5 := r.String()
			item, e6 := r.Uint32()
			if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || e6 != nil || task != taskGetInventoryPaginated || count != 1 || total != 1 || pid != l.pid() || kind != tc.owner || len(kind) > 10 || item != economyCatalog[200597].Items[0] {
				t.Fatal("wrong inventory wire identity or catalog item")
			}
		})
	}
}
