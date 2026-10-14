// JSON references recorded from main at 06912ba before code-quality renames.
package shadnet

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/testutil/jsonreference"
)

func TestJSONReferencePlatformShadnet(t *testing.T) {
	cases := []jsonreference.Case{
		{Name: "Policy", Type: reflect.TypeOf((*Policy)(nil)).Elem(), Shape: ""},
		{Name: "value_0", Type: reflect.TypeOf((*map[string]json.RawMessage)(nil)).Elem(), Shape: ""},
		{Name: "value_1", Type: reflect.TypeOf((*[]string)(nil)).Elem(), Shape: ""},
		{Name: "verifier_93", Type: reflect.TypeOf((*struct {
			Keys []json.RawMessage `json:"keys"`
		})(nil)).Elem(), Shape: "struct {\n\tKeys []json.RawMessage `json:\"keys\"`\n}"},
		{Name: "verifier_101", Type: reflect.TypeOf((*struct {
			N   string `json:"n"`
			E   string `json:"e"`
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Alg string `json:"alg"`
			Use string `json:"use"`
		})(nil)).Elem(), Shape: "struct {\n\tN   string `json:\"n\"`\n\tE   string `json:\"e\"`\n\tKty string `json:\"kty\"`\n\tKid string `json:\"kid\"`\n\tAlg string `json:\"alg\"`\n\tUse string `json:\"use\"`\n}"},
		{Name: "verifier_151", Type: reflect.TypeOf((*struct {
			Alg string `json:"alg"`
			Typ string `json:"typ"`
			Kid string `json:"kid"`
		})(nil)).Elem(), Shape: "struct {\n\tAlg string `json:\"alg\"`\n\tTyp string `json:\"typ\"`\n\tKid string `json:\"kid\"`\n}"},
		{Name: "verifier_171", Type: reflect.TypeOf((*struct {
			Issuer      string `json:"iss"`
			Subject     string `json:"sub"`
			Audience    string `json:"aud"`
			IssuedAt    int64  `json:"iat"`
			Expiry      int64  `json:"exp"`
			ID          string `json:"jti"`
			Environment int64  `json:"env_iss_id"`
			OnlineID    string `json:"online_id"`
			NPTitleID   string `json:"np_title_id"`
			Scope       string `json:"scope"`
			DUID        string `json:"duid"`
		})(nil)).Elem(), Shape: "struct {\n\tIssuer      string `json:\"iss\"`\n\tSubject     string `json:\"sub\"`\n\tAudience    string `json:\"aud\"`\n\tIssuedAt    int64  `json:\"iat\"`\n\tExpiry      int64  `json:\"exp\"`\n\tID          string `json:\"jti\"`\n\tEnvironment int64  `json:\"env_iss_id\"`\n\tOnlineID    string `json:\"online_id\"`\n\tNPTitleID   string `json:\"np_title_id\"`\n\tScope       string `json:\"scope\"`\n\tDUID        string `json:\"duid\"`\n}"},
	}
	jsonreference.Check(t, cases)
}
