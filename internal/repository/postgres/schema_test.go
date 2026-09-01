package postgres

import (
	"os"
	"strings"
	"testing"
)

func TestInitialMigrationContainsRequiredTables(t *testing.T) {
	content, err := os.ReadFile("../../../migrations/000001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(content)
	for _, table := range []string{
		"accounts",
		"proxy_profiles",
		"channels",
		"channel_memberships",
		"keyword_rules",
		"outgoing_message_jobs",
		"outgoing_message_events",
	} {
		if !strings.Contains(sql, "CREATE TABLE "+table) {
			t.Fatalf("migration missing table %s", table)
		}
	}
}
