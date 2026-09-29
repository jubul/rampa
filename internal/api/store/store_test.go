package store

import (
	"testing"
	"time"
)

func TestMigrationVersions(t *testing.T) {
	vs := MigrationVersions()
	if len(vs) == 0 || vs[0] != "0001_init" {
		t.Fatalf("migraciones embebidas: %v", vs)
	}
	b, err := migrationsFS.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"organizations", "users", "memberships", "api_tokens", "cloud_accounts", "clusters", "applications", "scopes", "deployments", "parameters", "services", "approvals", "events", "agent_commands"} {
		if !contains(string(b), "CREATE TABLE "+table+" (") {
			t.Errorf("la migración no crea %s", table)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestEventFilterMatches(t *testing.T) {
	e := &Event{SubjectType: "deployment", SubjectID: "d1"}
	cases := []struct {
		f    EventFilter
		want bool
	}{
		{EventFilter{}, true},
		{EventFilter{SubjectType: "deployment"}, true},
		{EventFilter{SubjectType: "deployment", SubjectID: "d1"}, true},
		{EventFilter{SubjectType: "cluster"}, false},
		{EventFilter{SubjectID: "d2"}, false},
	}
	for _, c := range cases {
		if got := c.f.Matches(e); got != c.want {
			t.Errorf("%+v: %v", c.f, got)
		}
	}
}

func TestDeploymentTerminalAndTokenExpired(t *testing.T) {
	for st, want := range map[string]bool{DeploymentQueued: false, DeploymentProgressing: false, DeploymentHealthy: true, DeploymentFailed: true, DeploymentCancelled: true} {
		if (&Deployment{Status: st}).Terminal() != want {
			t.Errorf("Terminal(%s) != %v", st, want)
		}
	}
	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	if (&APIToken{}).Expired(now) || (&APIToken{ExpiresAt: &future}).Expired(now) || !(&APIToken{ExpiresAt: &past}).Expired(now) {
		t.Error("Expired incorrecto")
	}
}

func TestNormalizeEmail(t *testing.T) {
	if NormalizeEmail("  Ana@Acme.COM ") != "ana@acme.com" {
		t.Error("NormalizeEmail")
	}
}
