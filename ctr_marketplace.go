package main

// Pit Stop, the in-game shop, is bdMarketplace (service 80).
//
// CTR reads its catalog locally; the server validates purchases against the same
// catalog and stores balances and granted inventory items.
//
//	245 getBalancesV3        struct: bdGetPlayerBalancesRequest -> bdGetPlayerBalancesResponse
//	246 switchReconciliation struct: carries the Nintendo eShop receipt (a JWT)
//	165 getInventoryPaginated classic task, an array of bdMarketplaceInventory
//
// Request seen on the wire (task 245):
//
//	50 03 f5 17 08 11 00 00 00 | 0a 0d "octane_switch" 10 03
//	service task   struct len   | 1: context           2: maxNumResults
//
// bdGetPlayerBalancesResponse::deserialize loops readObject(1, ...) into a
// 0x30-byte element capped at 16 entries, so the reply is:
//
//	bdGetPlayerBalancesResponse { repeated bdMarketplaceCurrencyAmount balances = 1 }
//	bdMarketplaceCurrencyAmount { uint64 currency_id = 1; int64 amount = 2; uint64 kind = 3 }
//
// CMarketplaceGetBalancesTask::finish reads getCurrencyID/getAmount off each entry and
// looks the id up in the currency table the game loaded from its own data. An id the
// game does not know is dropped. The catalog defines currency IDs 10 through 12.

import (
	"bytes"
	"encoding/binary"
	"os"
)

const (
	svcMarketplace            = 80
	taskGetInventoryPaginated = 165
	taskPurchaseSkus          = 154
	taskGetBalancesV3         = 245
	taskSwitchReconciliation  = 246
)

// Wumpa Coins, Nitro Boost, Free Nitro Boost, in the original catalog order.
func currencyIDs() []uint64 { return []uint64{10, 11, 12} }

func startingBalance() int64 { return startBalance }

func loadWallets() {
	economy.dir = dataDir
	if err := os.MkdirAll(economy.dir, 0755); err != nil {
		panic(err)
	}
}

// ---- protobuf -------------------------------------------------------------

func pbVarintField(b []byte, field byte, v uint64) []byte {
	b = append(b, field<<3)
	return appendVarint(b, v)
}

// zigzag encodes a signed amount the way bdStructBufferSerializer::writeInt64 does.
// bdMarketplaceCurrencyAmount's amount is an int64, and a plain varint is read back
// halved: 10000 sent raw arrives as 10000>>1 = 5000, which is what the game displayed.
func zigzag(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

func pbBytesField(b []byte, field byte, p []byte) []byte {
	b = append(b, field<<3|2)
	b = appendVarint(b, uint64(len(p)))
	return append(b, p...)
}

// getPlayerBalancesResponse builds the reply for task 245.
func getPlayerBalancesResponse(w map[uint64]int64, max int) []byte {
	ids := currencyIDs()
	if max <= 0 || max > len(ids) {
		max = len(ids)
	}
	out := []byte{}
	for _, id := range ids[:max] {
		e := pbVarintField(nil, 1, id)
		e = pbVarintField(e, 2, zigzag(w[id]))
		e = pbVarintField(e, 3, 0)
		out = pbBytesField(out, 1, e)
	}
	return out
}

// structTaskReply mirrors httpProxyReply: bdStructBufferTask::deserializeTaskReply reads
// the struct immediately after the task byte, so no result-count fields may precede it.
func structTaskReply(task byte, sb []byte) []byte {
	w := &bdWriter{}
	w.u64(transactions.Add(1))
	w.u32(0)
	w.u8(task)
	w.structv(sb)
	return w.b
}

// ---- dispatch -------------------------------------------------------------

// onMarketplace handles Pit Stop requests; nil means an unsupported task.
func (l *lobbyConn) onMarketplace(task byte, payload []byte) []byte {
	switch task {
	case taskGetBalancesV3:
		ctx, max := parseBalancesRequest(payload)
		if ctx != marketplaceContext || !validPS4BalancesRequest(payload, ctx, max) {
			return taskReply(task, errUnhandled, nil)
		}
		a, err := economy.snapshot(l.pid())
		if err != nil {
			l.logf("CTR economy read failed: %v", err)
			return taskReply(task, errUnhandled, nil)
		}
		w := a.Balances
		sb := getPlayerBalancesResponse(w, max)
		l.logf("CTR pitstop balances ctx=%q max=%d -> %d currencies", ctx, max, len(currencyIDs()))
		return structTaskReply(task, sb)

	case taskSwitchReconciliation:
		// Switch only (an eShop receipt): the PS4 build never sends it.
		return taskReply(task, errUnhandled, nil)

	case taskGetInventoryPaginated:
		r := &bdReader{b: payload, off: 3}
		ctx, err := r.str()
		if err != nil || ctx != marketplaceContext {
			return taskReply(task, errUnhandled, nil)
		}
		page, err := r.u32()
		if err != nil {
			return taskReply(task, errUnhandled, nil)
		}
		size, err := r.u32()
		if err != nil || size == 0 || size > 4096 {
			return taskReply(task, errUnhandled, nil)
		}
		a, err := economy.inventory(l.pid())
		if err != nil {
			l.logf("CTR economy inventory failed: %v", err)
			return taskReply(task, errUnhandled, nil)
		}
		items := inventoryPage(a, page, size)
		l.logf("CTR pitstop inventory page=%d size=%d -> %d items", page, size, len(items))
		return taskReply(task, 0, func(w *bdWriter) uint32 {
			for _, item := range items {
				writeInventoryItem(w, l.pid(), item)
			}
			return uint32(len(items))
		})
	case taskPurchaseSkus:
		skus, ok := parsePurchaseSkus(payload)
		if !ok {
			return taskReply(task, errUnhandled, nil)
		}
		debit, err := economy.purchase(l.pid(), skus)
		if err != nil {
			l.logf("CTR pitstop purchase rejected: %v", err)
			return taskReply(task, errUnhandled, nil)
		}
		l.logf("CTR pitstop purchase skus=%v debit=%d currency=10", skus, debit)
		l.afterReply = l.pushCurrentBalance
		return taskReply(task, 0, nil)

	}
	return nil
}

// CUSA13795 01.21: game 0xac10f0 -> SDK 0x920d0 (80/245), request
// vtable 0x240ca88 -> serializer 0xd3b70: field 1 context, field 2 max.
// The captured PS4 request is 24 bytes including the task terminator, max 1..16.
func validPS4BalancesRequest(payload []byte, ctx string, max int) bool {
	if max < 1 || max > 16 {
		return false
	}
	w := &bdWriter{b: []byte{svcMarketplace, tagU8, taskGetBalancesV3}}
	sb := pbBytesField(nil, 1, []byte(ctx))
	w.structv(pbVarintField(sb, 2, uint64(max)))
	return bytes.Equal(payload, append(w.b, 0))
}

// parseBalancesRequest reads the bdGetPlayerBalancesRequest struct buffer that follows the
// service and task bytes: field 1 context string, field 2 maxNumResults.
func parseBalancesRequest(payload []byte) (string, int) {
	sb := structPayload(payload)
	ctx, max := "", 0
	for i := 0; i < len(sb); {
		field := sb[i] >> 3
		wire := sb[i] & 7
		i++
		switch wire {
		case 0:
			v, next, err := readVarint(sb, i)
			if err != nil {
				return ctx, max
			}
			i = next
			if field == 2 {
				max = int(v)
			}
		case 2:
			ln, next, err := readVarint(sb, i)
			if err != nil || ln > uint64(len(sb)-next) {
				return ctx, max
			}
			i = next
			if field == 1 {
				ctx = string(sb[i : i+int(ln)])
			}
			i += int(ln)
		default:
			return ctx, max
		}
	}
	return ctx, max
}

// structPayload returns the struct buffer inside a task argument buffer. onTask passes the
// WHOLE typed buffer, so the first three bytes are the service, the u8 tag and the task:
//
//	50 | 03 f5 | 17 | 08 11 00 00 00 | 0a 0d "octane_switch" 10 03
//	svc  tag/task  struct  u32 length   the struct itself
func structPayload(payload []byte) []byte {
	const hdr = 3 // service + tagU8 + task
	if len(payload) < hdr+6 || payload[hdr] != tagStruct || payload[hdr+1] != tagU32 {
		return nil
	}
	n := int(payload[hdr+2]) | int(payload[hdr+3])<<8 | int(payload[hdr+4])<<16 | int(payload[hdr+5])<<24
	if n < 0 || hdr+6+n > len(payload) {
		return nil
	}
	return payload[hdr+6 : hdr+6+n]
}

// ---- purchases and inventory ----------------------------------------------

// readUInt16 expects 0x06; readInt64 expects 0x09. 0x14 is a special
// INT64_MAX sentinel in writeInt64, not the tag for an eight-byte integer.
const (
	tagU16 = 0x06
	tagI64 = 0x09
)

func (w *bdWriter) u16(v uint16) {
	w.b = append(w.b, tagU16)
	w.b = binary.LittleEndian.AppendUint16(w.b, v)
}

func (w *bdWriter) i64(v int64) {
	w.b = append(w.b, tagI64)
	w.b = binary.LittleEndian.AppendUint64(w.b, uint64(v))
}

// writeInventoryItem writes one bdMarketplaceInventory. Field order and the struct offsets
// each value comes from were read from bdMarketplaceInventory::serialize; field 3 (+0x48)
// is the item id CMarketplaceInventoryItem::populateItem matches against the game's own
// catalog, so it carries the catalog ITEM id granted by the SKU.
func writeInventoryItem(w *bdWriter, owner uint64, item uint32) {
	w.u64(owner)                   // bdUserAccountID owner
	w.strv(marketplaceAccountType) // bdUserAccountID account type
	w.u32(item)                    // catalog item ID, not SKU
	w.u32(1)                       // +0x4c quantity
	w.u32(0)                       // +0x50
	w.blobv(nil)                   // +0x54 item data
	w.u32(0)                       // +0x98
	w.i64(0)                       // +0xa0 expiry, 0 = never
	w.u16(0)                       // +0xa8
	w.u32(0)                       // +0xac
}

// purchaseSkus encodes arrays as count followed by typed elements.
// CTR cosmetics are non-consumable: quantities other than one are rejected.
func parsePurchaseSkus(payload []byte) ([]uint32, bool) {
	r := &bdReader{b: payload, off: 3}
	ctx, err := r.str()
	if err != nil || ctx != marketplaceContext {
		return nil, false
	}
	n, err := r.u32()
	if err != nil || n == 0 || n > 255 {
		return nil, false
	}
	skus := make([]uint32, n)
	for i := range skus {
		skus[i], err = r.u32()
		if err != nil {
			return nil, false
		}
	}
	qn, err := r.u32()
	if err != nil || qn != n {
		return nil, false
	}
	for range skus {
		q, e := r.u32()
		if e != nil || q != 1 {
			return nil, false
		}
	}
	if !defaultPS4PurchaseOptions(r.b[r.off:], n) {
		return nil, false
	}
	return skus, true
}

// CUSA13795 01.21 original eboot: game 0xac1e70 selects octane_psn at
// 0xac1f68 and calls 0x97160 at 0xac1ff1 with SKU/quantity arrays (each qty=1).
// 0x97160 zeros all optional arguments; 0x973c0 is the sibling overload that
// preserves the boolean argument. Both call 0x971b0, selecting service 80/task
// 154 at 0x971e5/0x971ea, then serializer 0x97410 at 0x972b0.
// Wire order: context string; u32 SKU count + u32 SKUs (0x975f4..0x9769a);
// u32 quantity count + u32 quantities (0x9769c..0x9773b); then these defaults:
// bool(false) (0x9777a), u32 count + null account IDs (0x977bd/0x97836;
// 0x897d0 writes u64(0), string("")), u32(0) (0x97874), u32(0) (0x978f5),
// u8(0) (0x979d0), u32(0) (0x97a47). No buyer account precedes the arrays:
// 0x975e5 skips it for the null account constructed by 0x971b0.
// Task finalization 0x123570 -> 0x17ca00 appends a raw zero terminator.
// Only this caller's exact defaults are supported; never ignore gifting,
// coupon or other optional data.
func defaultPS4PurchaseOptions(tail []byte, count uint32) bool {
	w := &bdWriter{}
	w.boolv(false)
	w.u32(count)
	for i := uint32(0); i < count; i++ {
		w.u64(0)
		w.strv("")
	}
	w.u32(0)
	w.u32(0)
	w.u8(0)
	w.u32(0)
	return bytes.Equal(tail, append(w.b, 0))
}
func parsePurchaseSku(payload []byte) (uint32, bool) {
	ids, ok := parsePurchaseSkus(payload)
	if !ok || len(ids) != 1 {
		return 0, false
	}
	return ids[0], true
}
