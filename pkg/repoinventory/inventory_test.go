package repoinventory

import (
	"encoding/json"
	"testing"
)

func TestClassifyDetectsConnectionStates(t *testing.T) {
	repos := []Repo{
		{FullName: "hendra/apotik-pintar", Name: "apotik-pintar"},
		{FullName: "hendra/kasaku", Name: "kasaku"},
		{FullName: "hendra/Dev_Control", Name: "Dev_Control"},
	}
	raw := []vercelProject{
		{ID: "1", Name: "apotik-pintar", Link: &vercelLink{Type: "github", Org: "Hendra", Repo: "Apotik-Pintar"}},
		{ID: "2", Name: "kasaku"},
		{ID: "3", Name: "other", Link: &vercelLink{Type: "github", Org: "someone", Repo: "other"}},
		{ID: "4", Name: "gl", Link: &vercelLink{Type: "gitlab", ProjectNamespace: "team", ProjectName: "gl"}},
		{ID: "5", Name: "dev-control"},
		{ID: "6", Name: "orphan"},
	}
	gotRepos, projects := classify(repos, raw, true, nil, "hendra")
	want := []string{StatusConnected, StatusUnconnected, StatusOutside, StatusOtherGit, StatusUnconnected, StatusUnconnected}
	for i, status := range want {
		if projects[i].Status != status {
			t.Fatalf("project %s: status %q, want %q", projects[i].Name, projects[i].Status, status)
		}
	}
	if projects[1].SuggestedRepo != "hendra/kasaku" {
		t.Fatalf("suggested repo = %q", projects[1].SuggestedRepo)
	}
	if projects[4].SuggestedRepo != "hendra/Dev_Control" {
		t.Fatalf("normalized suggestion = %q", projects[4].SuggestedRepo)
	}
	if projects[5].SuggestedRepo != "" {
		t.Fatalf("orphan should have no suggestion, got %q", projects[5].SuggestedRepo)
	}
	if projects[3].Repo != "team/gl" {
		t.Fatalf("gitlab repo = %q", projects[3].Repo)
	}
	if len(gotRepos[0].Projects) != 1 || gotRepos[0].Projects[0] != "apotik-pintar" {
		t.Fatalf("repo projects = %v", gotRepos[0].Projects)
	}
	if len(gotRepos[1].Projects) != 0 {
		t.Fatalf("unlinked repo should have no projects, got %v", gotRepos[1].Projects)
	}
	if projects[0].URL != "https://vercel.com/hendra/apotik-pintar" {
		t.Fatalf("url = %q", projects[0].URL)
	}
}

func TestClassifyWithoutGithubKeepsLinkedProjectsConnected(t *testing.T) {
	raw := []vercelProject{{ID: "1", Name: "x", Link: &vercelLink{Type: "github", Org: "a", Repo: "x"}}}
	_, projects := classify([]Repo{}, raw, false, nil, "")
	if projects[0].Status != StatusConnected || projects[0].URL != "" {
		t.Fatalf("got %+v", projects[0])
	}
}

func TestSuggestUsesAppNameAndRejectsAmbiguity(t *testing.T) {
	appName := func(owner, repo string) string { return "devcontrol-" + owner + "-" + repo }
	repos := []Repo{{FullName: "a/shop"}, {FullName: "b/shop"}}
	if got := suggestRepo("devcontrol-a-shop", repos, appName); got != "a/shop" {
		t.Fatalf("app name suggestion = %q", got)
	}
	if got := suggestRepo("shop", repos, appName); got != "" {
		t.Fatalf("ambiguous suggestion = %q", got)
	}
}

func TestPaginationNext(t *testing.T) {
	cases := map[string]string{`null`: "", `1690000000000`: "1690000000000", `"abc"`: "abc", ``: ""}
	for raw, want := range cases {
		if got := paginationNext(json.RawMessage(raw)); got != want {
			t.Fatalf("paginationNext(%s) = %q, want %q", raw, got, want)
		}
	}
}
