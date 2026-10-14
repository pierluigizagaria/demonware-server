// JSON references recorded from main at 06912ba before code-quality renames.
package diablo

import (
	"reflect"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/testutil/jsonreference"
)

func TestJSONReferenceDiablo(t *testing.T) {
	cases := []jsonreference.Case{
		{Name: "pubConfig", Type: reflect.TypeOf((*pubConfig)(nil)).Elem(), Shape: ""},
		{Name: "riftSettings", Type: reflect.TypeOf((*riftSettings)(nil)).Elem(), Shape: ""},
		{Name: "seasonRotation", Type: reflect.TypeOf((*seasonRotation)(nil)).Elem(), Shape: ""},
		{Name: "value_0", Type: reflect.TypeOf((*seasonEvents)(nil)).Elem(), Shape: ""},
		{Name: "value_1", Type: reflect.TypeOf((*[]string)(nil)).Elem(), Shape: ""},
		{Name: "value_2", Type: reflect.TypeOf((*map[string]bool)(nil)).Elem(), Shape: ""},
		{Name: "value_3", Type: reflect.TypeOf((*map[string]seasonEvents)(nil)).Elem(), Shape: ""},
	}
	jsonreference.Check(t, cases)
}
