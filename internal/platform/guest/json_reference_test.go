// JSON references recorded from main at 06912ba before code-quality renames.
package guest

import (
	"reflect"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/testutil/jsonreference"
)

func TestJSONReferencePlatformGuest(t *testing.T) {
	cases := []jsonreference.Case{
		{Name: "value_0", Type: reflect.TypeOf((*map[string]string)(nil)).Elem(), Shape: ""},
	}
	jsonreference.Check(t, cases)
}
