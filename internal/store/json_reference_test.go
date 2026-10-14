// JSON references recorded from main at 06912ba before code-quality renames.
package store

import (
	"reflect"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/testutil/jsonreference"
)

func TestJSONReferenceStore(t *testing.T) {
	cases := []jsonreference.Case{
		{Name: "value_0", Type: reflect.TypeOf((*map[string]uint64)(nil)).Elem(), Shape: ""},
		{Name: "value_1", Type: reflect.TypeOf((*map[string]bool)(nil)).Elem(), Shape: ""},
		{Name: "value_2", Type: reflect.TypeOf((*[]string)(nil)).Elem(), Shape: ""},
		{Name: "value_3", Type: reflect.TypeOf((*[]uint32)(nil)).Elem(), Shape: ""},
	}
	jsonreference.Check(t, cases)
}
