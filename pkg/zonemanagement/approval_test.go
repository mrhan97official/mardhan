package zonemanagement

import "testing"

func TestDomainMatchesDoesNotAcceptLookalikeDomain(t *testing.T) {
	tests := []struct {
		host, zone string
		want bool
	}{
		{"app.example.com", "example.com", true},
		{"example.com", "example.com", true},
		{"EXAMPLE.COM.", "example.com", true},
		{"example.com.evil.test", "example.com", false},
		{"anotherexample.com", "example.com", false},
		{"app.vercel.app", "example.com", false},
	}
	for _, test := range tests {
		if got := domainMatches(test.host, test.zone); got != test.want {
			t.Errorf("domainMatches(%q, %q) = %t, want %t", test.host, test.zone, got, test.want)
		}
	}
}

func TestVercelEndpointKeepsUpsertWhenAddingTeam(t *testing.T) {
	t.Setenv("VERCEL_TEAM_ID", "team 123")
	got := vercelEndpoint("/v10/projects/prj_123/env?upsert=true")
	want := "https://api.vercel.com/v10/projects/prj_123/env?upsert=true&teamId=team+123"
	if got != want { t.Fatalf("endpoint = %q, want %q", got, want) }
}
