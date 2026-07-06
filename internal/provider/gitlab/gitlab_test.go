package gitlab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gl "gitlab.com/gitlab-org/api/client-go"

	"github.com/farzan-kh/wright/internal/provider"
	"github.com/farzan-kh/wright/internal/provider/providertest"
)

var testRepo = provider.Repo{FullPath: "acme/widgets"}

// base is the client-go API path prefix; the project path is URL-escaped, so
// "acme/widgets" appears as "acme%2Fwidgets".
const base = "/api/v4/projects/acme%2Fwidgets"

func mustWrite(w http.ResponseWriter, b []byte) { _, _ = w.Write(b) }

// router dispatches on "METHOD EscapedPath" so URL-encoded project paths (with
// %2F) match exactly, which net/http's ServeMux would otherwise decode.
func router(t *testing.T, routes map[string]http.HandlerFunc) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.EscapedPath()
		if fn, ok := routes[key]; ok {
			fn(w, r)
			return
		}
		t.Errorf("unexpected request: %s", key)
		http.Error(w, "unexpected request", http.StatusNotFound)
	})
}

func newTestClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := gl.NewClient("test-token", gl.WithBaseURL(srv.URL), gl.WithCustomRetryMax(0))
	if err != nil {
		t.Fatal(err)
	}
	return &Client{gl: c}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return m
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return b
}

func parseLabels(v any) []string {
	switch x := v.(type) {
	case string:
		if x == "" {
			return nil
		}
		parts := strings.Split(x, ",")
		labels := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				labels = append(labels, s)
			}
		}
		return labels
	case []any:
		labels := make([]string, 0, len(x))
		for _, item := range x {
			if s, _ := item.(string); s != "" {
				labels = append(labels, s)
			}
		}
		return labels
	default:
		return nil
	}
}

func TestListLabeledIssues(t *testing.T) {
	var gotLabels, gotState string
	h := router(t, map[string]http.HandlerFunc{
		"GET " + base + "/issues": func(w http.ResponseWriter, r *http.Request) {
			gotLabels = r.URL.Query().Get("labels")
			gotState = r.URL.Query().Get("state")
			if r.URL.Query().Get("page") == "2" {
				mustWrite(w, readFixture(t, "issues_page2.json"))
				return
			}
			w.Header().Set("X-Next-Page", "2")
			mustWrite(w, readFixture(t, "issues_page1.json"))
		},
		"GET " + base + "/issues/201/notes": func(w http.ResponseWriter, r *http.Request) {
			mustWrite(w, []byte(`[{"body":"changed the label","author":{"username":"dave"},"system":true,"created_at":"2026-06-05T11:00:00Z"},{"body":"can you also cover the retry path?","author":{"username":"carol"},"system":false,"created_at":"2026-06-05T12:00:00Z"}]`))
		},
		"GET " + base + "/issues/202/notes": func(w http.ResponseWriter, r *http.Request) {
			mustWrite(w, []byte(`[]`))
		},
	})

	c := newTestClient(t, h)
	issues, err := c.ListLabeledIssues(context.Background(), testRepo, "wright")
	if err != nil {
		t.Fatalf("ListLabeledIssues: %v", err)
	}
	if gotState != "opened" {
		t.Errorf("state param = %q, want opened", gotState)
	}
	if gotLabels != "wright" {
		t.Errorf("labels param = %q, want wright", gotLabels)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2 (both pages): %+v", len(issues), issues)
	}
	providertest.AssertIssuesPopulated(t, issues)
	providertest.AssertEveryIssueHasLabel(t, issues, "wright")
	if issues[0].Number != 201 || issues[0].Author != "dave" {
		t.Errorf("issue[0] = %+v", issues[0])
	}
	if len(issues[0].Comments) != 1 || issues[0].Comments[0].Author != "carol" || issues[0].Comments[0].Body != "can you also cover the retry path?" {
		t.Errorf("issue[0] comments = %+v, want system note excluded", issues[0].Comments)
	}
}

func TestCommentOnIssue(t *testing.T) {
	var gotBody string
	h := router(t, map[string]http.HandlerFunc{
		"POST " + base + "/issues/201/notes": func(w http.ResponseWriter, r *http.Request) {
			gotBody, _ = decodeBody(t, r)["body"].(string)
			w.WriteHeader(http.StatusCreated)
			mustWrite(w, []byte(`{"id":1}`))
		},
	})
	c := newTestClient(t, h)
	if err := c.CommentOnIssue(context.Background(), testRepo, 201, "please add repro steps"); err != nil {
		t.Fatalf("CommentOnIssue: %v", err)
	}
	if gotBody != "please add repro steps" {
		t.Errorf("note body = %q", gotBody)
	}
}

func TestCommentOnPullRequest(t *testing.T) {
	var gotBody string
	h := router(t, map[string]http.HandlerFunc{
		"POST " + base + "/merge_requests/7/notes": func(w http.ResponseWriter, r *http.Request) {
			gotBody, _ = decodeBody(t, r)["body"].(string)
			w.WriteHeader(http.StatusCreated)
			mustWrite(w, []byte(`{"id":2}`))
		},
	})
	c := newTestClient(t, h)
	if err := c.CommentOnPullRequest(context.Background(), testRepo, 7, "smoke test comment"); err != nil {
		t.Fatalf("CommentOnPullRequest: %v", err)
	}
	if gotBody != "smoke test comment" {
		t.Errorf("note body = %q", gotBody)
	}
}

func TestIssueLabelRoundTrip(t *testing.T) {
	labels := []string{"wright", "reliability"}
	contains := func(needle string) bool {
		for _, l := range labels {
			if l == needle {
				return true
			}
		}
		return false
	}

	h := router(t, map[string]http.HandlerFunc{
		"GET " + base + "/issues": func(w http.ResponseWriter, r *http.Request) {
			mustWrite(w, mustJSON(t, []map[string]any{{
				"id":          9201,
				"iid":         201,
				"title":       "Issue label round-trip",
				"description": "",
				"web_url":     "https://gitlab.com/acme/widgets/-/issues/201",
				"author":      map[string]any{"username": "dave"},
				"labels":      labels,
				"created_at":  "2026-06-05T10:00:00Z",
				"updated_at":  "2026-06-06T12:00:00Z",
			}}))
		},
		"PUT " + base + "/issues/201": func(w http.ResponseWriter, r *http.Request) {
			body := decodeBody(t, r)
			for _, add := range parseLabels(body["add_labels"]) {
				if !contains(add) {
					labels = append(labels, add)
				}
			}
			for _, remove := range parseLabels(body["remove_labels"]) {
				out := labels[:0]
				for _, l := range labels {
					if l != remove {
						out = append(out, l)
					}
				}
				labels = out
			}
			mustWrite(w, mustJSON(t, map[string]any{
				"id":          9201,
				"iid":         201,
				"title":       "Issue label round-trip",
				"description": "",
				"web_url":     "https://gitlab.com/acme/widgets/-/issues/201",
				"author":      map[string]any{"username": "dave"},
				"labels":      labels,
				"created_at":  "2026-06-05T10:00:00Z",
				"updated_at":  "2026-06-06T12:00:00Z",
			}))
		},
		"GET " + base + "/issues/201/notes": func(w http.ResponseWriter, r *http.Request) {
			mustWrite(w, []byte(`[]`))
		},
	})

	c := newTestClient(t, h)
	providertest.AssertIssueLabelRoundTrip(t, c, testRepo, 201, "wright", "needs-human")
}

func TestRemoveIssueLabelAlreadyAbsent(t *testing.T) {
	h := router(t, map[string]http.HandlerFunc{
		"PUT " + base + "/issues/101": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			mustWrite(w, []byte(`{"message":"404 Issue Not Found"}`))
		},
	})
	c := newTestClient(t, h)
	if err := c.RemoveIssueLabel(context.Background(), testRepo, 101, "needs-human"); err != nil {
		t.Fatalf("RemoveIssueLabel should ignore an already-absent label: %v", err)
	}
}

func TestDefaultBranch(t *testing.T) {
	h := router(t, map[string]http.HandlerFunc{
		"GET " + base: func(w http.ResponseWriter, r *http.Request) {
			mustWrite(w, []byte(`{"default_branch":"main"}`))
		},
	})
	c := newTestClient(t, h)
	got, err := c.DefaultBranch(context.Background(), testRepo)
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != "main" {
		t.Errorf("DefaultBranch = %q, want main", got)
	}
}

func TestCreateBranch(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		var gotBranch, gotRef string
		h := router(t, map[string]http.HandlerFunc{
			"POST " + base + "/repository/branches": func(w http.ResponseWriter, r *http.Request) {
				body := decodeBody(t, r)
				gotBranch, _ = body["branch"].(string)
				gotRef, _ = body["ref"].(string)
				w.WriteHeader(http.StatusCreated)
				mustWrite(w, []byte(`{"name":"wright/x"}`))
			},
		})
		c := newTestClient(t, h)
		if err := c.CreateBranch(context.Background(), testRepo, "wright/x", "main"); err != nil {
			t.Fatalf("CreateBranch: %v", err)
		}
		if gotBranch != "wright/x" || gotRef != "main" {
			t.Errorf("branch=%q ref=%q", gotBranch, gotRef)
		}
	})

	t.Run("already_exists", func(t *testing.T) {
		h := router(t, map[string]http.HandlerFunc{
			"POST " + base + "/repository/branches": func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				mustWrite(w, []byte(`{"message":"Branch already exists"}`))
			},
		})
		c := newTestClient(t, h)
		err := c.CreateBranch(context.Background(), testRepo, "wright/x", "main")
		providertest.AssertErrorIs(t, err, provider.ErrAlreadyExists)
	})
}

func TestDeleteBranch(t *testing.T) {
	called := false
	h := router(t, map[string]http.HandlerFunc{
		"DELETE " + base + "/repository/branches/wright%2Fx": func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusNoContent)
		},
	})
	c := newTestClient(t, h)
	if err := c.DeleteBranch(context.Background(), testRepo, "wright/x"); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	if !called {
		t.Error("delete endpoint not called")
	}
}

func TestPushCommits(t *testing.T) {
	var commitBody map[string]any
	h := router(t, map[string]http.HandlerFunc{
		// added.md exists -> update; obsolete.txt not probed (it's a delete).
		"HEAD " + base + "/repository/files/docs%2Fadded%2Emd": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
		"POST " + base + "/repository/commits": func(w http.ResponseWriter, r *http.Request) {
			commitBody = decodeBody(t, r)
			w.WriteHeader(http.StatusCreated)
			mustWrite(w, []byte(`{"id":"newsha"}`))
		},
	})
	c := newTestClient(t, h)
	head, err := c.PushCommits(context.Background(), testRepo, "wright/x", providertest.StandardCommits())
	if err != nil {
		t.Fatalf("PushCommits: %v", err)
	}
	if head != "newsha" {
		t.Errorf("head = %q, want newsha", head)
	}
	if commitBody["branch"] != "wright/x" {
		t.Errorf("branch = %v, want wright/x", commitBody["branch"])
	}

	actions, _ := commitBody["actions"].([]any)
	if len(actions) != 2 {
		t.Fatalf("actions = %d, want 2: %v", len(actions), actions)
	}
	byPath := map[string]map[string]any{}
	for _, a := range actions {
		m := a.(map[string]any)
		byPath[m["file_path"].(string)] = m
	}
	// added.md existed -> "update" with content.
	if a := byPath["docs/added.md"]; a["action"] != "update" || a["content"] != "hello from wright\n" {
		t.Errorf("added action = %v", a)
	}
	// obsolete.txt -> "delete".
	if a := byPath["obsolete.txt"]; a["action"] != "delete" {
		t.Errorf("delete action = %v", a)
	}
}

func TestPushCommitsCreatesMissingFile(t *testing.T) {
	var commitBody map[string]any
	h := router(t, map[string]http.HandlerFunc{
		"HEAD " + base + "/repository/files/docs%2Fadded%2Emd": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
		"POST " + base + "/repository/commits": func(w http.ResponseWriter, r *http.Request) {
			commitBody = decodeBody(t, r)
			w.WriteHeader(http.StatusCreated)
			mustWrite(w, []byte(`{"id":"newsha"}`))
		},
	})
	c := newTestClient(t, h)
	if _, err := c.PushCommits(context.Background(), testRepo, "wright/x", providertest.StandardCommits()); err != nil {
		t.Fatalf("PushCommits: %v", err)
	}
	actions, _ := commitBody["actions"].([]any)
	for _, a := range actions {
		m := a.(map[string]any)
		if m["file_path"] == "docs/added.md" && m["action"] != "create" {
			t.Errorf("missing file should map to create, got %v", m["action"])
		}
	}
}

func TestFindOpenPullRequestByHead(t *testing.T) {
	var gotSource, gotState string
	h := router(t, map[string]http.HandlerFunc{
		"GET " + base + "/merge_requests": func(w http.ResponseWriter, r *http.Request) {
			gotSource = r.URL.Query().Get("source_branch")
			gotState = r.URL.Query().Get("state")
			mustWrite(w, []byte(`[{"iid":9,"web_url":"https://gitlab.com/acme/widgets/-/merge_requests/9","source_branch":"wright/issue-201","target_branch":"main"}]`))
		},
	})
	c := newTestClient(t, h)
	pr, err := c.FindOpenPullRequestByHead(context.Background(), testRepo, "wright/issue-201")
	if err != nil {
		t.Fatalf("FindOpenPullRequestByHead: %v", err)
	}
	if gotSource != "wright/issue-201" || gotState != "opened" {
		t.Fatalf("query source/state = %q/%q", gotSource, gotState)
	}
	if pr == nil || pr.Number != 9 {
		t.Fatalf("pr = %+v, want !9", pr)
	}
}

func TestFindOpenPullRequestByHeadNone(t *testing.T) {
	h := router(t, map[string]http.HandlerFunc{
		"GET " + base + "/merge_requests": func(w http.ResponseWriter, r *http.Request) {
			mustWrite(w, []byte(`[]`))
		},
	})
	c := newTestClient(t, h)
	pr, err := c.FindOpenPullRequestByHead(context.Background(), testRepo, "wright/issue-201")
	if err != nil {
		t.Fatalf("FindOpenPullRequestByHead: %v", err)
	}
	if pr != nil {
		t.Fatalf("pr = %+v, want nil", pr)
	}
}

func TestOpenPullRequest(t *testing.T) {
	var body map[string]any
	h := router(t, map[string]http.HandlerFunc{
		"POST " + base + "/merge_requests": func(w http.ResponseWriter, r *http.Request) {
			body = decodeBody(t, r)
			w.WriteHeader(http.StatusCreated)
			mustWrite(w, []byte(`{"iid":7,"web_url":"https://gitlab.com/acme/widgets/-/merge_requests/7","source_branch":"wright/x","target_branch":"main"}`))
		},
	})
	c := newTestClient(t, h)
	pr, err := c.OpenPullRequest(context.Background(), testRepo, provider.PullRequestSpec{
		Title:      "Fix the thing",
		Body:       "Closes #201",
		HeadBranch: "wright/x",
		BaseBranch: "main",
		Draft:      true,
	})
	if err != nil {
		t.Fatalf("OpenPullRequest: %v", err)
	}
	if title, _ := body["title"].(string); title != "Draft: Fix the thing" {
		t.Errorf("title = %q, want draft-prefixed", title)
	}
	if body["source_branch"] != "wright/x" || body["target_branch"] != "main" {
		t.Errorf("branches in body = %v", body)
	}
	if pr.Number != 7 || pr.HeadBranch != "wright/x" || pr.BaseBranch != "main" {
		t.Errorf("pr = %+v", pr)
	}
}

// TestOpenPullRequestRecoversFromDuplicateOnRetry covers the case where a
// create actually reached GitLab (e.g. an earlier retry attempt) but the
// caller only sees the conflict on a later attempt: OpenPullRequest should
// look up and return the already-created MR instead of failing.
func TestOpenPullRequestRecoversFromDuplicateOnRetry(t *testing.T) {
	h := router(t, map[string]http.HandlerFunc{
		"POST " + base + "/merge_requests": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			mustWrite(w, []byte(`{"message":["Another open merge request already exists for this source branch: !7"]}`))
		},
		"GET " + base + "/merge_requests": func(w http.ResponseWriter, r *http.Request) {
			mustWrite(w, []byte(`[{"iid":7,"web_url":"https://gitlab.com/acme/widgets/-/merge_requests/7","source_branch":"wright/x","target_branch":"main"}]`))
		},
	})
	c := newTestClient(t, h)
	pr, err := c.OpenPullRequest(context.Background(), testRepo, provider.PullRequestSpec{
		Title:      "Fix the thing",
		Body:       "Closes #201",
		HeadBranch: "wright/x",
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("OpenPullRequest: %v", err)
	}
	if pr.Number != 7 {
		t.Fatalf("pr = %+v, want recovered !7", pr)
	}
}

func TestMergePullRequest(t *testing.T) {
	var body map[string]any
	h := router(t, map[string]http.HandlerFunc{
		"PUT " + base + "/merge_requests/7/merge": func(w http.ResponseWriter, r *http.Request) {
			body = decodeBody(t, r)
			mustWrite(w, []byte(`{"iid":7,"state":"merged"}`))
		},
	})
	c := newTestClient(t, h)
	err := c.MergePullRequest(context.Background(), testRepo, 7, provider.MergeOptions{
		Method:       provider.MergeSquash,
		DeleteBranch: true,
	})
	if err != nil {
		t.Fatalf("MergePullRequest: %v", err)
	}
	if body["squash"] != true {
		t.Errorf("squash = %v, want true", body["squash"])
	}
	if body["should_remove_source_branch"] != true {
		t.Errorf("should_remove_source_branch = %v, want true", body["should_remove_source_branch"])
	}
}

func TestClosePullRequest(t *testing.T) {
	var body map[string]any
	h := router(t, map[string]http.HandlerFunc{
		"PUT " + base + "/merge_requests/7": func(w http.ResponseWriter, r *http.Request) {
			body = decodeBody(t, r)
			mustWrite(w, []byte(`{"iid":7,"state":"closed"}`))
		},
	})
	c := newTestClient(t, h)
	if err := c.ClosePullRequest(context.Background(), testRepo, 7); err != nil {
		t.Fatalf("ClosePullRequest: %v", err)
	}
	if body["state_event"] != "close" {
		t.Errorf("state_event = %v, want close", body["state_event"])
	}
}

func TestGetIssue(t *testing.T) {
	h := router(t, map[string]http.HandlerFunc{
		"GET " + base + "/issues/201": func(w http.ResponseWriter, r *http.Request) {
			mustWrite(w, mustJSON(t, map[string]any{
				"id":          9301,
				"iid":         201,
				"title":       "Fix bug",
				"description": "steps",
				"state":       "closed",
				"author":      map[string]any{"username": "dave"},
			}))
		},
	})
	c := newTestClient(t, h)
	iss, err := c.GetIssue(context.Background(), testRepo, 201)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if iss.Number != 201 || iss.State != "closed" || iss.Title != "Fix bug" {
		t.Errorf("issue = %+v", iss)
	}
}

func TestGetIssueNotFound(t *testing.T) {
	h := router(t, map[string]http.HandlerFunc{
		"GET " + base + "/issues/999": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			mustWrite(w, []byte(`{"message":"404 Issue Not Found"}`))
		},
	})
	c := newTestClient(t, h)
	_, err := c.GetIssue(context.Background(), testRepo, 999)
	providertest.AssertErrorIs(t, err, provider.ErrNotFound)
}

func TestReadRepoFile(t *testing.T) {
	var gotRef string
	filePath := gl.PathEscape("docs/adr/007-x.md")
	h := router(t, map[string]http.HandlerFunc{
		"GET " + base + "/repository/files/" + filePath: func(w http.ResponseWriter, r *http.Request) {
			gotRef = r.URL.Query().Get("ref")
			mustWrite(w, mustJSON(t, map[string]any{
				"file_name": "007-x.md",
				"file_path": "docs/adr/007-x.md",
				"encoding":  "base64",
				"content":   base64.StdEncoding.EncodeToString([]byte("# ADR 007\n")),
			}))
		},
	})
	c := newTestClient(t, h)
	content, err := c.ReadRepoFile(context.Background(), testRepo, "main", "docs/adr/007-x.md")
	if err != nil {
		t.Fatalf("ReadRepoFile: %v", err)
	}
	if content != "# ADR 007\n" {
		t.Errorf("content = %q", content)
	}
	if gotRef != "main" {
		t.Errorf("ref param = %q, want main", gotRef)
	}
}

func TestReadRepoFileResolvesDefaultBranch(t *testing.T) {
	filePath := gl.PathEscape("missing.md")
	h := router(t, map[string]http.HandlerFunc{
		"GET " + base: func(w http.ResponseWriter, r *http.Request) {
			mustWrite(w, []byte(`{"default_branch":"main"}`))
		},
		"GET " + base + "/repository/files/" + filePath: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			mustWrite(w, []byte(`{"message":"404 File Not Found"}`))
		},
	})
	c := newTestClient(t, h)
	_, err := c.ReadRepoFile(context.Background(), testRepo, "", "missing.md")
	providertest.AssertErrorIs(t, err, provider.ErrNotFound)
}

func TestListRepoDir(t *testing.T) {
	h := router(t, map[string]http.HandlerFunc{
		"GET " + base + "/repository/tree": func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("ref"); got != "main" {
				t.Errorf("ref param = %q, want main", got)
			}
			mustWrite(w, mustJSON(t, []map[string]any{
				{"type": "blob", "name": "006-y.md", "path": "docs/adr/006-y.md"},
				{"type": "tree", "name": "archive", "path": "docs/adr/archive"},
			}))
		},
	})
	c := newTestClient(t, h)
	entries, err := c.ListRepoDir(context.Background(), testRepo, "main", "docs/adr")
	if err != nil {
		t.Fatalf("ListRepoDir: %v", err)
	}
	if len(entries) != 2 || entries[0] != "006-y.md" || entries[1] != "archive/" {
		t.Errorf("entries = %v", entries)
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"not_found", http.StatusNotFound, provider.ErrNotFound},
		{"unauthorized", http.StatusUnauthorized, provider.ErrAuth},
		{"forbidden", http.StatusForbidden, provider.ErrAuth},
		{"rate_limited", http.StatusTooManyRequests, provider.ErrRateLimited},
		{"bad_request", http.StatusBadRequest, provider.ErrInvalidRequest},
		{"conflict", http.StatusConflict, provider.ErrInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := router(t, map[string]http.HandlerFunc{
				"GET " + base: func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(tc.status)
					mustWrite(w, []byte(`{"message":"boom"}`))
				},
			})
			c := newTestClient(t, h)
			_, err := c.DefaultBranch(context.Background(), testRepo)
			providertest.AssertErrorIs(t, err, tc.want)
		})
	}
}
