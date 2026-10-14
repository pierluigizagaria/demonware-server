// JSON references recorded from main at 06912ba before code-quality renames.
package ps4

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/testutil/jsonreference"
)

func TestJSONReferencePlatformPs4(t *testing.T) {
	cases := []jsonreference.Case{
		{Name: "value_0", Type: reflect.TypeOf((*map[string]json.RawMessage)(nil)).Elem(), Shape: ""},
		{Name: "value_1", Type: reflect.TypeOf((*map[string]string)(nil)).Elem(), Shape: ""},
		{Name: "platform_psn_53", Type: reflect.TypeOf((*struct {
			TitleID   string `json:"title_id"`
			AuthTask  string `json:"auth_task"`
			IVSeed    string `json:"iv_seed"`
			ExtraData string `json:"extra_data"`
		})(nil)).Elem(), Shape: "struct {\n\tTitleID   string `json:\"title_id\"`\n\tAuthTask  string `json:\"auth_task\"`\n\tIVSeed    string `json:\"iv_seed\"`\n\tExtraData string `json:\"extra_data\"`\n}"},
		{Name: "platform_psn_65", Type: reflect.TypeOf((*struct {
			Token string `json:"token"`
		})(nil)).Elem(), Shape: "struct {\n\tToken string `json:\"token\"`\n}"},
		{Name: "platform_psn_68", Type: reflect.TypeOf((*struct {
			IDToken string `json:"id_token"`
		})(nil)).Elem(), Shape: "struct {\n\tIDToken string `json:\"id_token\"`\n}"},
	}
	jsonreference.Check(t, cases)
}
