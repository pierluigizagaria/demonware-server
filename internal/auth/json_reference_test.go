// JSON references recorded from main at 06912ba before code-quality renames.
package auth

import (
	"reflect"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/testutil/jsonreference"
)

func TestJSONReferenceAuth(t *testing.T) {
	cases := []jsonreference.Case{
		{Name: "value_0", Type: reflect.TypeOf((*map[string]string)(nil)).Elem(), Shape: ""},
		{Name: "auth_108", Type: reflect.TypeOf((*struct {
			AuthTask string `json:"auth_task"`
			IVSeed   string `json:"iv_seed"`
			TitleID  string `json:"title_id"`
			Identity string `json:"identity"`
		})(nil)).Elem(), Shape: "struct {\n\tAuthTask string `json:\"auth_task\"`\n\tIVSeed   string `json:\"iv_seed\"`\n\tTitleID  string `json:\"title_id\"`\n\tIdentity string `json:\"identity\"`\n}"},
		{Name: "auth_218", Type: reflect.TypeOf((*struct {
			AuthTask     string `json:"auth_task"`
			Code         string `json:"code"`
			IVSeed       string `json:"iv_seed"`
			ClientTicket string `json:"client_ticket"`
			ServerTicket string `json:"server_ticket"`
			ExtraData    string `json:"extra_data"`
		})(nil)).Elem(), Shape: "struct {\n\tAuthTask     string `json:\"auth_task\"`\n\tCode         string `json:\"code\"`\n\tIVSeed       string `json:\"iv_seed\"`\n\tClientTicket string `json:\"client_ticket\"`\n\tServerTicket string `json:\"server_ticket\"`\n\tExtraData    string `json:\"extra_data\"`\n}"},
	}
	jsonreference.Check(t, cases)
}
