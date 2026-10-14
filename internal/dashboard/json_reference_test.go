// JSON references recorded from main at 06912ba before code-quality renames.
package dashboard

import (
	"reflect"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/testutil/jsonreference"
)

func TestJSONReferenceDashboard(t *testing.T) {
	cases := []jsonreference.Case{
		{Name: "apiPlayer", Type: reflect.TypeOf((*apiPlayer)(nil)).Elem(), Shape: ""},
		{Name: "ApiLobbyP", Type: reflect.TypeOf((*ApiLobbyP)(nil)).Elem(), Shape: ""},
		{Name: "ApiGathering", Type: reflect.TypeOf((*ApiGathering)(nil)).Elem(), Shape: ""},
		{Name: "apiEvent", Type: reflect.TypeOf((*apiEvent)(nil)).Elem(), Shape: ""},
		{Name: "apiMethod", Type: reflect.TypeOf((*apiMethod)(nil)).Elem(), Shape: ""},
		{Name: "apiServer", Type: reflect.TypeOf((*apiServer)(nil)).Elem(), Shape: ""},
		{Name: "ApiStats", Type: reflect.TypeOf((*ApiStats)(nil)).Elem(), Shape: ""},
	}
	jsonreference.Check(t, cases)
}
