package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"os"
	"testing"
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
	t.Setenv("CTR_START_BALANCE", "10000")
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
	r := &bdReader{b: reply}
	if _, err := r.u64(); err != nil {
		t.Fatal(err)
	}
	if code, err := r.u32(); err != nil || code != want {
		t.Fatalf("task error = %d, %v; want %d", code, err, want)
	}
	if want != 0 && r.off != len(reply) {
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
		w := &bdWriter{b: []byte{svcMarketplace, tagU8, task}}
		switch task {
		case taskGetBalancesV3:
			w.structv(pbVarintField(pbBytesField(nil, 1, []byte("octane_switch")), 2, 3))
			w.b = append(w.b, 0)
		case taskGetInventoryPaginated:
			w.strv("octane_switch")
			w.u32(1)
			w.u32(16)
		case taskPurchaseSkus:
			w.b = marketplaceHex(t, realPurchase)
		}
		marketplaceOutcome(t, sharedTask(t, l, wire, w.b), errUnhandled)
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
		payload[1] = tagBool
		marketplaceOutcome(t, sharedTask(t, l, wire, payload), errUnhandled)
		payload[1] = tagU8
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
	badLength := &bdWriter{b: []byte{svcMarketplace, tagU8, taskGetBalancesV3}}
	badLength.structv(appendVarint([]byte{0x0a}, ^uint64(0)))
	marketplaceOutcome(t, sharedTask(t, l, wire, badLength.b), errUnhandled)
	marketplaceNoFiles(t, s)
}

// Decode separate encrypted frames so the purchase reply and push order are
// checked through the real dispatcher without opening a listener.
func marketplaceFrames(t *testing.T, l *lobbyConn, wire *platformWire) [][]byte {
	t.Helper()
	var frames [][]byte
	for raw := wire.Bytes(); len(raw) != 0; {
		if len(raw) < 4 {
			t.Fatal("truncated frame")
		}
		size := int(binary.LittleEndian.Uint32(raw)) + 4
		if size < 50 || size > len(raw) {
			t.Fatal("bad frame length")
		}
		plain, err := aesCBCDecrypt(l.keys.s2cAES, raw[10:26], raw[26:size-8])
		if err != nil || len(plain) < 5 {
			t.Fatalf("frame decrypt: %v", err)
		}
		n := int(binary.LittleEndian.Uint32(plain))
		if n > len(plain)-5 {
			t.Fatal("bad inner length")
		}
		frames = append(frames, plain[4:5+n])
		raw = raw[size:]
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
			if len(frames) != 2 || frames[0][0] != innerTaskReply || frames[1][0] != innerPush {
				t.Fatal("purchase must reply before balance push")
			}
			marketplaceOutcome(t, frames[0][1:], 0)
			wantBalance := int64(10000) - economyCatalog[200597].Price
			// Check the entire push, including nested owner identity and zigzag amount.
			owner := pbVarintField(nil, 1, l.pid())
			owner = pbBytesField(owner, 2, []byte(tc.owner))
			entry := pbBytesField(nil, 1, owner)
			entry = pbVarintField(entry, 2, 10)
			entry = pbVarintField(entry, 3, 0xffffffff)
			entry = pbVarintField(entry, 4, 0)
			entry = pbVarintField(entry, 5, zigzag(wantBalance))
			push := &bdWriter{}
			push.u32(76)
			push.structv(pbBytesField(nil, 1, entry))
			if !bytes.Equal(frames[1][1:], push.b) {
				t.Fatal("wrong balance push owner or amount")
			}
			// Reload through a fresh store to prove the shared purchase was persisted.
			a, err := (&economyStore{dir: s.dir}).snapshot(l.pid())
			if err != nil || a.Balances[10] != wantBalance || !ownsSKU(a, economyCatalog[200597]) {
				t.Fatal("purchase debit/grant not persisted")
			}
			w := &bdWriter{b: []byte{svcMarketplace, tagU8, taskGetInventoryPaginated}}
			w.strv(tc.context)
			w.u32(1)
			w.u32(16)
			w.b = append(w.b, 0)
			reply := sharedTask(t, l, wire, w.b)
			marketplaceOutcome(t, reply, 0)
			r := &bdReader{b: reply, off: 14}
			task, e1 := r.u8()
			count, e2 := r.u32()
			total, e3 := r.u32()
			pid, e4 := r.u64()
			kind, e5 := r.str()
			item, e6 := r.u32()
			if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || e6 != nil || task != taskGetInventoryPaginated || count != 1 || total != 1 || pid != l.pid() || kind != tc.owner || len(kind) > 10 || item != economyCatalog[200597].Items[0] {
				t.Fatal("wrong inventory wire identity or catalog item")
			}
		})
	}
}
