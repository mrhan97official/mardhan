package repoinventory

import (
	"encoding/json"
	"testing"

	"devcontrol/pkg/vercelapp"
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

func TestSplitRepoRejectsUnsafeNames(t *testing.T) {
	if owner, name, ok := splitRepo("hendra/apotik-pintar"); !ok || owner != "hendra" || name != "apotik-pintar" {
		t.Fatalf("valid repo rejected: %q %q %v", owner, name, ok)
	}
	for _, bad := range []string{"", "hendra", "a/b/c", "../x", "a/..", "a b/c", "a/c?x=1"} {
		if _, _, ok := splitRepo(bad); ok {
			t.Fatalf("unsafe repo accepted: %q", bad)
		}
	}
}

func TestHasGitLink(t *testing.T) {
	cases := []struct {
		project vercelapp.Project
		want    bool
	}{
		{vercelapp.Project{}, false},
		{vercelapp.Project{LinkType: "github"}, false},
		{vercelapp.Project{LinkType: "github", LinkOrg: "a", LinkRepo: "b"}, true},
		{vercelapp.Project{LinkType: "gitlab"}, true},
	}
	for _, c := range cases {
		if got := hasGitLink(c.project); got != c.want {
			t.Fatalf("hasGitLink(%+v) = %v, want %v", c.project, got, c.want)
		}
	}
}

func TestManagedProjectsWithoutD1(t *testing.T) {
	t.Setenv("CF_D1_DATABASE_ID", "")
	managed, err := managedProjects(func(owner, repo string) string { return owner + "-" + repo })
	if err != nil || len(managed) != 0 {
		t.Fatalf("got %v, %v", managed, err)
	}
}

func TestProductionURLAndTemporary(t *testing.T) {
	cases := []struct {
		target *vercelTarget
		want   string
	}{
		{nil, ""},
		{&vercelTarget{URL: "app-abc123.vercel.app"}, "https://app-abc123.vercel.app"},
		{&vercelTarget{Alias: []string{"app-git-main.vercel.app", "app.vercel.app"}}, "https://app.vercel.app"},
		{&vercelTarget{Alias: []string{"app.vercel.app", "toko.example.com"}}, "https://toko.example.com"},
	}
	for _, c := range cases {
		if got := productionURL("app", c.target); got != c.want {
			t.Fatalf("productionURL = %q, want %q", got, c.want)
		}
	}
	raw := []vercelProject{{ID: "1", Name: "devcontrol-selfupdate-test-1700000000"}}
	raw[0].Targets.Production = &vercelTarget{ReadyState: "ready", URL: "x.vercel.app"}
	_, projects := classify([]Repo{}, raw, true, nil, "")
	if !projects[0].Temporary || projects[0].ProductionState != "READY" || projects[0].ProductionURL != "https://x.vercel.app" {
		t.Fatalf("got %+v", projects[0])
	}
}

func TestLookupManagedByNameOrLinkedRepo(t *testing.T) {
	managed := map[string]string{"devcontrol-a-shop": "a/shop", repoKey("A/Shop"): "a/shop"}
	if got := lookupManaged(managed, "DevControl-A-Shop", ""); got != "a/shop" {
		t.Fatalf("by name = %q", got)
	}
	if got := lookupManaged(managed, "manual-import", "a/SHOP"); got != "a/shop" {
		t.Fatalf("by linked repo = %q", got)
	}
	if got := lookupManaged(managed, "other", "b/other"); got != "" {
		t.Fatalf("unrelated = %q", got)
	}
	if got := lookupManaged(nil, "x", "a/shop"); got != "" {
		t.Fatalf("nil map = %q", got)
	}
}
