package lobby

import (
	"testing"
)

func TestServiceTableCopiesUnreadablePolicy(t *testing.T) {
	policy := []byte{80, 104}
	table := NewServiceTable(&ReplyCounter{}, nil, policy)
	policy[0], policy[1] = 4, 255
	for service := 0; service < 256; service++ {
		if table.rejectsUnreadableTask(byte(service)) != (service == 80 || service == 104) {
			t.Fatalf("policy changed for service %d", service)
		}
	}
}
