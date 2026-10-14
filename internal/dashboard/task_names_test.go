package dashboard

import (
	"testing"
)

func TestCTRNewsDashboardTaskNames(t *testing.T) {
	if TaskName(104, 4) != "bdMarketingComms::reportMessagesViewed" ||
		TaskName(104, 5) != "bdMarketingComms::getMessagesForLocations" {
		t.Fatal("marketing task names missing")
	}
}
