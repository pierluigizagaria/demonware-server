// JSON references recorded from main at 06912ba before code-quality renames.
package ctr

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/testutil/jsonreference"
)

func TestJSONReferenceCtr(t *testing.T) {
	cases := []jsonreference.Case{
		{Name: "CtrSearchDoc", Type: reflect.TypeOf((*CtrSearchDoc)(nil)).Elem(), Shape: ""},
		{Name: "CtrPlayerInfo", Type: reflect.TypeOf((*CtrPlayerInfo)(nil)).Elem(), Shape: ""},
		{Name: "economySKU", Type: reflect.TypeOf((*economySKU)(nil)).Elem(), Shape: ""},
		{Name: "EconomyAccount", Type: reflect.TypeOf((*EconomyAccount)(nil)).Elem(), Shape: ""},
		{Name: "ChallengeCompletion", Type: reflect.TypeOf((*ChallengeCompletion)(nil)).Elem(), Shape: ""},
		{Name: "PitStopRefreshRecord", Type: reflect.TypeOf((*PitStopRefreshRecord)(nil)).Elem(), Shape: ""},
		{Name: "challengePeriod", Type: reflect.TypeOf((*challengePeriod)(nil)).Elem(), Shape: ""},
		{Name: "ChallengeRule", Type: reflect.TypeOf((*ChallengeRule)(nil)).Elem(), Shape: ""},
		{Name: "challengeSchedule", Type: reflect.TypeOf((*challengeSchedule)(nil)).Elem(), Shape: ""},
		{Name: "windowShoppingRule", Type: reflect.TypeOf((*windowShoppingRule)(nil)).Elem(), Shape: ""},
		{Name: "raceRate", Type: reflect.TypeOf((*raceRate)(nil)).Elem(), Shape: ""},
		{Name: "AchievementEvent", Type: reflect.TypeOf((*AchievementEvent)(nil)).Elem(), Shape: ""},
		{Name: "CtrStat", Type: reflect.TypeOf((*CtrStat)(nil)).Elem(), Shape: ""},
		{Name: "lobbyHostDoc", Type: reflect.TypeOf((*lobbyHostDoc)(nil)).Elem(), Shape: ""},
		{Name: "lobbyListenServer", Type: reflect.TypeOf((*lobbyListenServer)(nil)).Elem(), Shape: ""},
		{Name: "lobbyTeamBalance", Type: reflect.TypeOf((*lobbyTeamBalance)(nil)).Elem(), Shape: ""},
		{Name: "value_0", Type: reflect.TypeOf((*map[uint32]economySKU)(nil)).Elem(), Shape: ""},
		{Name: "value_1", Type: reflect.TypeOf((*map[uint64]raceRate)(nil)).Elem(), Shape: ""},
		{Name: "value_2", Type: reflect.TypeOf((*map[uint32]map[uint64]CtrStat)(nil)).Elem(), Shape: ""},
		{Name: "value_3", Type: reflect.TypeOf((*map[string]json.RawMessage)(nil)).Elem(), Shape: ""},
		{Name: "value_4", Type: reflect.TypeOf((*map[string]any)(nil)).Elem(), Shape: ""},
		{Name: "value_5", Type: reflect.TypeOf((*map[string]string)(nil)).Elem(), Shape: ""},
		{Name: "value_6", Type: reflect.TypeOf((*map[string]bool)(nil)).Elem(), Shape: ""},
		{Name: "value_7", Type: reflect.TypeOf((*map[uint64]int64)(nil)).Elem(), Shape: ""},
		{Name: "value_8", Type: reflect.TypeOf((*map[uint32]bool)(nil)).Elem(), Shape: ""},
		{Name: "value_9", Type: reflect.TypeOf((*map[string]uint64)(nil)).Elem(), Shape: ""},
		{Name: "value_10", Type: reflect.TypeOf((*jsonUint32)(nil)).Elem(), Shape: ""},
		{Name: "value_11", Type: reflect.TypeOf((*[]uint32)(nil)).Elem(), Shape: ""},
		{Name: "ctr_umbrella_52", Type: reflect.TypeOf((*struct {
			Ticket  string     `json:"ticket"`
			TitleID jsonUint32 `json:"titleID"`
		})(nil)).Elem(), Shape: "struct {\n\tTicket  string     `json:\"ticket\"`\n\tTitleID jsonUint32 `json:\"titleID\"`\n}"},
		{Name: "ctr_umbrella_82", Type: reflect.TypeOf((*struct {
			ID       uint64 `json:"umbrellaID"`
			Token    string `json:"accessToken"`
			Expires  int64  `json:"expires"`
			Accounts []any  `json:"accounts"`
		})(nil)).Elem(), Shape: "struct {\n\tID       uint64 `json:\"umbrellaID\"`\n\tToken    string `json:\"accessToken\"`\n\tExpires  int64  `json:\"expires\"`\n\tAccounts []any  `json:\"accounts\"`\n}"},
		{Name: "lobbydoc_47", Type: reflect.TypeOf((*struct {
			ListenServer struct {
				LocalAddress string `json:"local_address"`
			} `json:"listen_server"`
		})(nil)).Elem(), Shape: "struct {\n\tListenServer struct {\n\t\tLocalAddress string `json:\"local_address\"`\n\t} `json:\"listen_server\"`\n}"},
		{Name: "lobbydoc_48", Type: reflect.TypeOf((*struct {
			LocalAddress string `json:"local_address"`
		})(nil)).Elem(), Shape: "struct {\n\tLocalAddress string `json:\"local_address\"`\n}"},
	}
	jsonreference.Check(t, cases)
}
