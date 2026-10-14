// JSON references recorded from main at 06912ba before code-quality renames.
package strictjson

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/testutil/jsonreference"
)

func TestJSONReferenceStrictjson(t *testing.T) {
	cases := []jsonreference.Case{
		{Name: "value_0", Type: reflect.TypeOf((*map[string]json.RawMessage)(nil)).Elem(), Shape: ""},
		{Name: "value_1", Type: reflect.TypeOf((*map[string]any)(nil)).Elem(), Shape: ""},
	}
	jsonreference.Check(t, cases)
}
