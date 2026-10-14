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
// looks the ID up in the currency table the game loaded from its own data. An ID the
// game does not know is dropped. The catalog defines currency IDs 10 through 12.

package ctr

import (
	"bytes"
	"os"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
)

const (
	SvcMarketplace            = 80
	TaskGetInventoryPaginated = 165
	TaskPurchaseSKUs          = 154
	TaskGetBalancesV3         = 245
	taskSwitchReconciliation  = 246
)

// Wumpa Coins, Nitro Boost, Free Nitro Boost, in the original catalog order.
func currencyIDs() []uint64 { return []uint64{10, 11, 12} }

func (s *EconomyStore) LoadWallets(dir string) {
	s.Dir = dir
	if err := os.MkdirAll(s.Dir, 0755); err != nil {
		panic(err)
	}
}

// GetPlayerBalancesResponse builds the reply for task 245.
func GetPlayerBalancesResponse(w map[uint64]int64, max int) []byte {
	ids := currencyIDs()
	if max <= 0 || max > len(ids) {
		max = len(ids)
	}
	out := []byte{}
	for _, id := range ids[:max] {
		e := demonware.AppendVarintField(nil, 1, id)
		e = demonware.AppendVarintField(e, 2, demonware.ZigZag(w[id]))
		e = demonware.AppendVarintField(e, 3, 0)
		out = demonware.AppendBytesField(out, 1, e)
	}
	return out
}

// OnMarketplace handles Pit Stop requests; nil means an unsupported task.
func (h *EconomyHandler) OnMarketplace(peer lobby.TaskSession, task byte, payload []byte) (result lobby.TaskResult) {
	switch task {
	case TaskGetBalancesV3:
		ctx, max := ParseBalancesRequest(payload)
		if ctx != ps4.MarketplaceContext || !validPS4BalancesRequest(payload, ctx, max) {
			result.Reply = h.TaskReply(task, lobby.ErrUnhandled, nil)
			return result
		}
		a, err := h.Economy.Snapshot(peer.PID())
		if err != nil {
			peer.Logf("[ctr] economy read failed: %v", err)
			result.Reply = h.TaskReply(task, lobby.ErrUnhandled, nil)
			return result
		}
		w := a.Balances
		sb := GetPlayerBalancesResponse(w, max)
		peer.Logf("[ctr] pitstop balances ctx=%q max=%d -> %d currencies", ctx, max, len(currencyIDs()))
		result.Reply = h.StructTaskReply(task, sb)
		return result

	case taskSwitchReconciliation:
		// Switch only (an eShop receipt): the PS4 build never sends it.
		result.Reply = h.TaskReply(task, lobby.ErrUnhandled, nil)
		return result

	case TaskGetInventoryPaginated:
		if len(payload) < 3 {
			result.Reply = h.TaskReply(task, lobby.ErrUnhandled, nil)
			return result
		}
		r := demonware.NewReader(payload[3:])
		ctx, err := r.String()
		if err != nil || ctx != ps4.MarketplaceContext {
			result.Reply = h.TaskReply(task, lobby.ErrUnhandled, nil)
			return result
		}
		page, err := r.Uint32()
		if err != nil {
			result.Reply = h.TaskReply(task, lobby.ErrUnhandled, nil)
			return result
		}
		size, err := r.Uint32()
		if err != nil || size == 0 || size > 4096 {
			result.Reply = h.TaskReply(task, lobby.ErrUnhandled, nil)
			return result
		}
		a, err := h.Economy.Inventory(peer.PID())
		if err != nil {
			peer.Logf("[ctr] economy inventory failed: %v", err)
			result.Reply = h.TaskReply(task, lobby.ErrUnhandled, nil)
			return result
		}
		items := InventoryPage(a, page, size)
		peer.Logf("[ctr] pitstop inventory page=%d size=%d -> %d items", page, size, len(items))
		result.Reply = h.TaskReply(task, 0, func(w *demonware.Writer) uint32 {
			for _, item := range items {
				WriteInventoryItem(w, peer.PID(), item)
			}
			return uint32(len(items))
		})
		return result
	case TaskPurchaseSKUs:
		skus, ok := parsePurchaseSkus(payload)
		if !ok {
			result.Reply = h.TaskReply(task, lobby.ErrUnhandled, nil)
			return result
		}
		debit, err := h.Economy.Purchase(peer.PID(), skus)
		if err != nil {
			peer.Logf("[ctr] pitstop purchase rejected: %v", err)
			result.Reply = h.TaskReply(task, lobby.ErrUnhandled, nil)
			return result
		}
		peer.Logf("[ctr] pitstop purchase skus=%v debit=%d currency=10", skus, debit)
		result.AfterReply = func(_ error) { h.PushCurrentBalance(peer) }
		result.Reply = h.TaskReply(task, 0, nil)
		return result

	}
	result.Reply = nil
	return result
}

// CUSA13795 01.21: game 0xac10f0 -> SDK 0x920d0 (80/245), request
// vtable 0x240ca88 -> serializer 0xd3b70: field 1 context, field 2 max.
// The captured PS4 request is 24 bytes including the task terminator, max 1..16.
func validPS4BalancesRequest(payload []byte, ctx string, max int) bool {
	if max < 1 || max > 16 {
		return false
	}
	w := demonware.NewWriter([]byte{SvcMarketplace, demonware.TagUint8, TaskGetBalancesV3})
	sb := demonware.AppendBytesField(nil, 1, []byte(ctx))
	w.Struct(demonware.AppendVarintField(sb, 2, uint64(max)))
	return bytes.Equal(payload, append(w.Bytes(), 0))
}

// ParseBalancesRequest reads the bdGetPlayerBalancesRequest struct buffer that follows the
// service and task bytes: field 1 context string, field 2 maxNumResults.
func ParseBalancesRequest(payload []byte) (string, int) {
	sb := demonware.TaskStructPayload(payload)
	ctx, max := "", 0
	for i := 0; i < len(sb); {
		field := sb[i] >> 3
		wire := sb[i] & 7
		i++
		switch wire {
		case 0:
			v, next, err := demonware.ReadVarint(sb, i)
			if err != nil {
				return ctx, max
			}
			i = next
			if field == 2 {
				max = int(v)
			}
		case 2:
			ln, next, err := demonware.ReadVarint(sb, i)
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

// WriteInventoryItem writes one bdMarketplaceInventory. Field order and the struct offsets
// each value comes from were read from bdMarketplaceInventory::serialize; field 3 (+0x48)
// is the item ID CMarketplaceInventoryItem::populateItem matches against the game's own
// catalog, so it carries the catalog ITEM ID granted by the SKU.
func WriteInventoryItem(w *demonware.Writer, owner uint64, item uint32) {
	w.Uint64(owner)                      // bdUserAccountID owner
	w.String(ps4.MarketplaceAccountType) // bdUserAccountID account type
	w.Uint32(item)                       // catalog item ID, not SKU
	w.Uint32(1)                          // +0x4c quantity
	w.Uint32(0)                          // +0x50
	w.Blob(nil)                          // +0x54 item data
	w.Uint32(0)                          // +0x98
	w.Int64(0)                           // +0xa0 expiry, 0 = never
	w.Uint16(0)                          // +0xa8
	w.Uint32(0)                          // +0xac
}

// purchaseSkus encodes arrays as count followed by typed elements.
// CTR cosmetics are non-consumable: quantities other than one are rejected.
func parsePurchaseSkus(payload []byte) ([]uint32, bool) {
	if len(payload) < 3 {
		return nil, false
	}
	r := demonware.NewReader(payload[3:])
	ctx, err := r.String()
	if err != nil || ctx != ps4.MarketplaceContext {
		return nil, false
	}
	n, err := r.Uint32()
	if err != nil || n == 0 || n > 255 {
		return nil, false
	}
	skus := make([]uint32, n)
	for i := range skus {
		skus[i], err = r.Uint32()
		if err != nil {
			return nil, false
		}
	}
	qn, err := r.Uint32()
	if err != nil || qn != n {
		return nil, false
	}
	for range skus {
		q, e := r.Uint32()
		if e != nil || q != 1 {
			return nil, false
		}
	}
	if !defaultPS4PurchaseOptions(r.Remaining(), n) {
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
	w := &demonware.Writer{}
	w.Bool(false)
	w.Uint32(count)
	for i := uint32(0); i < count; i++ {
		w.Uint64(0)
		w.String("")
	}
	w.Uint32(0)
	w.Uint32(0)
	w.Uint8(0)
	w.Uint32(0)
	return bytes.Equal(tail, append(w.Bytes(), 0))
}

func ParsePurchaseSKU(payload []byte) (uint32, bool) {
	ids, ok := parsePurchaseSkus(payload)
	if !ok || len(ids) != 1 {
		return 0, false
	}
	return ids[0], true
}
