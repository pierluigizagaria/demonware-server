package ctr

import (
	demonwarepkg "github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
	lobbypkg "github.com/NextendoNetwork/nextendo-demonware/internal/lobby"
	ps4pkg "github.com/NextendoNetwork/nextendo-demonware/internal/platform/ps4"
)

const (
	svcMarketingComms           = 104
	taskReportMessagesViewed    = 4
	taskGetMessagesForLocations = 5
	maxNewsEntries              = 8
)

// CUSA13795 01.21: 0x90e90 builds 104/5; 0x90170 and 0x91700 read
// locations and messages. 0xcda540 consumes only content_long from the JSON.
func (h *NewsHandler) OnMarketingComms(peer lobbypkg.TaskSession, task byte, r *demonwarepkg.Reader) []byte {
	if peer.Identity() == nil || peer.Identity().PID == 0 || !ps4pkg.IsCTRTitle(peer.Identity().Title) {
		return h.TaskReply(task, lobbypkg.ErrUnhandled, nil)
	}
	key, err := r.String()
	if err != nil || len(key) > 15 {
		return h.TaskReply(task, lobbypkg.ErrUnhandled, nil)
	}
	switch task {
	case taskGetMessagesForLocations:
		language, err := r.String()
		if err != nil || len(language) > 19 {
			return h.TaskReply(task, lobbypkg.ErrUnhandled, nil)
		}
		count, err := r.Uint32()
		if err != nil || count == 0 || count > maxNewsEntries {
			return h.TaskReply(task, lobbypkg.ErrUnhandled, nil)
		}
		locations := make([]struct{ location, capacity uint32 }, count)
		for i := range locations {
			location, e1 := r.Uint32()
			capacity, e2 := r.Uint32()
			if e1 != nil || e2 != nil {
				return h.TaskReply(task, lobbypkg.ErrUnhandled, nil)
			}
			locations[i].location, locations[i].capacity = location, capacity
		}
		if len(r.Remaining()) != 1 || r.Remaining()[0] != 0 {
			return h.TaskReply(task, lobbypkg.ErrUnhandled, nil)
		}
		msg := h.News.Message(language)
		return h.TaskReply(task, 0, func(w *demonwarepkg.Writer) uint32 {
			for _, loc := range locations {
				w.Uint32(loc.location)
				if loc.location != 1 || loc.capacity == 0 || len(msg.Content) == 0 {
					w.Uint32(0)
					continue
				}
				w.Uint32(1)
				w.Uint64(msg.Id)
				w.String(language)
				w.Blob(msg.Content)
				w.Blob([]byte("{}"))
			}
			return count
		})
	case taskReportMessagesViewed:
		count, err := r.Uint32()
		if err != nil || count > maxNewsEntries {
			return h.TaskReply(task, lobbypkg.ErrUnhandled, nil)
		}
		ids := make([]uint64, count)
		for i := range ids {
			language, e1 := r.String()
			id, e2 := r.Uint64()
			_, e3 := r.Uint32()
			if e1 != nil || len(language) > 19 || e2 != nil || e3 != nil {
				return h.TaskReply(task, lobbypkg.ErrUnhandled, nil)
			}
			ids[i] = id
		}
		if len(r.Remaining()) != 1 || r.Remaining()[0] != 0 {
			return h.TaskReply(task, lobbypkg.ErrUnhandled, nil)
		}
		peer.Logf("CTR news viewed count=%d", count)
		for _, id := range ids {
			peer.Logf("CTR news viewed id=%d", id)
		}
		return h.TaskReply(task, 0, nil)
	}
	return h.TaskReply(task, lobbypkg.ErrUnhandled, nil)
}
