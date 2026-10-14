package demonware

const (
	InnerTaskReply   = 0x01
	InnerPush        = 0x02
	InnerTaskRequest = 0x86
)

// TaskReply encodes an ordinary result list. Empty lists omit the total count;
// errors stop after the error code. Transaction allocation belongs to the caller.
func TaskReply(transaction uint64, task byte, errCode uint32, results func(w *Writer) uint32) []byte {
	body := &Writer{}
	n := uint32(0)
	if errCode == 0 && results != nil {
		n = results(body)
	}
	return ListReply(transaction, task, errCode, n, n, body.Bytes())
}

// ListReply supports paginated results whose total exceeds the current count.
// The caller serializes each result according to the service's own schema.
func ListReply(transaction uint64, task byte, errCode, count, total uint32, body []byte) []byte {
	w := &Writer{}
	w.Uint64(transaction)
	w.Uint32(errCode)
	if errCode != 0 {
		return w.b
	}
	w.Uint8(task)
	// 0xC005B0 only reads the total if there is at least one result.
	w.Uint32(count)
	if count > 0 {
		w.Uint32(total)
		w.Raw(body)
	}
	return w.b
}

// HTTPProxyReply includes status fields 1 and 2 for both SDK deserializers:
// bdHTTPProxyResponse reads field 2; the bdABTesting REST path reads field 1.
// This describes two readers, not independent validation with two games.
func HTTPProxyReply(transaction uint64, task byte, status uint32, body []byte) []byte {
	sb := AppendVarint([]byte{0x08}, uint64(status))
	sb = AppendVarint(append(sb, 0x10), uint64(status))
	sb = append(sb, 0x1A)
	sb = AppendVarint(sb, uint64(len(body)))
	sb = append(sb, body...)
	// bdRemoteTask::handleTaskReply consumes transaction, error and task.
	// bdStructBufferTask::deserializeTaskReply then reads StructStart directly;
	// ordinary result-count fields make it fail with BD_HANDLE_TASK_FAILED (4).
	return StructReply(transaction, task, sb)
}

// StructReply omits ordinary list counts before StructStart.
func StructReply(transaction uint64, task byte, sb []byte) []byte {
	w := &Writer{}
	w.Uint64(transaction)
	w.Uint32(0)
	w.Uint8(task)
	w.Struct(sb)
	return w.b
}
