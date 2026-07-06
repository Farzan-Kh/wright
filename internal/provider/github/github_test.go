package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	gh "github.com/google/go-github/v78/github"

	"github.com/farzan-kh/wright/internal/provider"
	"github.com/farzan-kh/wright/internal/provider/providertest"
)

var testRepo = provider.Repo{FullPath: "acme/widgets"}

// newTestClient stands up an httptest server for h and returns a Client whose
// underlying go-github client is pointed at it.
func newTestClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	c := gh.NewClient(nil)
	c.BaseURL = u
	return &Client{gh: c}
}

// mustWrite writes a canned response body, ignoring the error (irrelevant to an
// in-process httptest handler).
func mustWrite(w http.ResponseWriter, b []byte) { _, _ = w.Write(b) }

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

func TestListLabeledIssues(t *testing.T) {
	var gotLabels, gotState string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		gotLabels = r.URL.Query().Get("labels")
		gotState = r.URL.Query().Get("state")
		if r.URL.Query().Get("page") == "2" {
			mustWrite(w, readFixture(t, "issues_page2.json"))
			return
		}
		w.Header().Set("Link", `<http://`+r.Host+`/repos/acme/widgets/issues?page=2>; rel="next"`)
		mustWrite(w, readFixture(t, "issues_page1.json"))
	})
	mux.HandleFunc("GET /repos/acme/widgets/issues/101/comments", func(w http.ResponseWriter, r *http.Request) {
		mustWrite(w, []byte(`[{"body":"can you also cover the retry path?","user":{"login":"carol"},"created_at":"2026-06-01T12:00:00Z"}]`))
	})
	mux.HandleFunc("GET /repos/acme/widgets/issues/103/comments", func(w http.ResponseWriter, r *http.Request) {
		mustWrite(w, []byte(`[]`))
	})

	c := newTestClient(t, mux)
	issues, err := c.ListLabeledIssues(context.Background(), testRepo, "wright")
	if err != nil {
		t.Fatalf("ListLabeledIssues: %v", err)
	}

	if gotState != "open" {
		t.Errorf("state param = %q, want open", gotState)
	}
	if gotLabels != "wright" {
		t.Errorf("labels param = %q, want wright", gotLabels)
	}
	// page1 has issue 101 + PR 102 (filtered); page2 has issue 103.
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2: %+v", len(issues), issues)
	}
	providertest.AssertIssuesPopulated(t, issues)
	providertest.AssertNoIssueNumbers(t, issues, 102)
	providertest.AssertEveryIssueHasLabel(t, issues, "wright")

	if issues[0].Number != 101 || issues[0].Author != "alice" {
		t.Errorf("issue[0] = %+v", issues[0])
	}
	if issues[0].Labels[0] != "wright" || issues[0].Labels[1] != "bug" {
		t.Errorf("issue[0] labels = %v", issues[0].Labels)
	}
	if len(issues[0].Comments) != 1 || issues[0].Comments[0].Author != "carol" || issues[0].Comments[0].Body != "can you also cover the retry path?" {
		t.Errorf("issue[0] comments = %+v", issues[0].Comments)
	}
	if len(issues[1].Comments) != 0 {
		t.Errorf("issue[1] comments = %+v, want none", issues[1].Comments)
	}
}

func TestCommentOnIssue(t *testing.T) {
	var gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /repos/acme/widgets/issues/101/comments", func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = decodeBody(t, r)["body"].(string)
		w.WriteHeader(http.StatusCreated)
		mustWrite(w, []byte(`{"id":1}`))
	})

	c := newTestClient(t, mux)
	if err := c.CommentOnIssue(context.Background(), testRepo, 101, "please add repro steps"); err != nil {
		t.Fatalf("CommentOnIssue: %v", err)
	}
	if gotBody != "please add repro steps" {
		t.Errorf("comment body = %q", gotBody)
	}
}

func TestCommentOnPullRequest(t *testing.T) {
	var gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /repos/acme/widgets/issues/7/comments", func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = decodeBody(t, r)["body"].(string)
		w.WriteHeader(http.StatusCreated)
		mustWrite(w, []byte(`{"id":2}`))
	})
	c := newTestClient(t, mux)
	if err := c.CommentOnPullRequest(context.Background(), testRepo, 7, "smoke test comment"); err != nil {
		t.Fatalf("CommentOnPullRequest: %v", err)
	}
	if gotBody != "smoke test comment" {
		t.Errorf("comment body = %q", gotBody)
	}
}

func TestIssueLabelRoundTrip(t *testing.T) {
	labels := []string{"wright", "bug"}
	contains := func(needle string) bool {
		for _, l := range labels {
			if l == needle {
				return true
			}
		}
		return false
	}

	m := http.NewServeMux()
	m.HandleFunc("GET /repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		apiLabels := make([]map[string]string, 0, len(labels))
		for _, l := range labels {
			apiLabels = append(apiLabels, map[string]string{"name": l})
		}
		mustWrite(w, mustJSON(t, []map[string]any{{
			"number":     101,
			"title":      "Issue label round-trip",
			"body":       "",
			"html_url":   "https://github.com/acme/widgets/issues/101",
			"user":       map[string]any{"login": "alice"},
			"labels":     apiLabels,
			"created_at": "2026-06-01T10:00:00Z",
			"updated_at": "2026-06-02T11:30:00Z",
		}}))
	})
	m.HandleFunc("POST /repos/acme/widgets/issues/101/labels", func(w http.ResponseWriter, r *http.Request) {
		var req []string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode add-label request: %v", err)
		}
		for _, label := range req {
			if label != "" && !contains(label) {
				labels = append(labels, label)
			}
		}
		mustWrite(w, []byte(`[]`))
	})
	m.HandleFunc("DELETE /repos/acme/widgets/issues/101/labels/needs-human", func(w http.ResponseWriter, r *http.Request) {
		out := labels[:0]
		for _, l := range labels {
			if l != "needs-human" {
				out = append(out, l)
			}
		}
		labels = out
		w.WriteHeader(http.StatusOK)
	})
	m.HandleFunc("GET /repos/acme/widgets/issues/101/comments", func(w http.ResponseWriter, r *http.Request) {
		mustWrite(w, []byte(`[]`))
	})

	c := newTestClient(t, m)
	providertest.AssertIssueLabelRoundTrip(t, c, testRepo, 101, "wright", "needs-human")
}

func TestRemoveIssueLabelAlreadyAbsent(t *testing.T) {
	m := http.NewServeMux()
	m.HandleFunc("DELETE /repos/acme/widgets/issues/101/labels/needs-human", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		mustWrite(w, []byte(`{"message":"Label does not exist"}`))
	})
	c := newTestClient(t, m)
	if err := c.RemoveIssueLabel(context.Background(), testRepo, 101, "needs-human"); err != nil {
		t.Fatalf("RemoveIssueLabel should ignore missing label: %v", err)
	}
}

func TestDefaultBranch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/acme/widgets", func(w http.ResponseWriter, r *http.Request) {
		mustWrite(w, []byte(`{"default_branch":"main"}`))
	})
	c := newTestClient(t, mux)
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
		var gotRef, gotSHA string
		mux := http.NewServeMux()
		mux.HandleFunc("GET /repos/acme/widgets/git/ref/heads/main", func(w http.ResponseWriter, r *http.Request) {
			mustWrite(w, []byte(`{"ref":"refs/heads/main","object":{"sha":"basesha","type":"commit"}}`))
		})
		mux.HandleFunc("POST /repos/acme/widgets/git/refs", func(w http.ResponseWriter, r *http.Request) {
			body := decodeBody(t, r)
			gotRef, _ = body["ref"].(string)
			gotSHA, _ = body["sha"].(string)
			w.WriteHeader(http.StatusCreated)
			mustWrite(w, []byte(`{"ref":"refs/heads/wright/x"}`))
		})
		c := newTestClient(t, mux)
		if err := c.CreateBranch(context.Background(), testRepo, "wright/x", "main"); err != nil {
			t.Fatalf("CreateBranch: %v", err)
		}
		if gotRef != "refs/heads/wright/x" {
			t.Errorf("ref = %q", gotRef)
		}
		if gotSHA != "basesha" {
			t.Errorf("sha = %q, want basesha (resolved from base ref)", gotSHA)
		}
	})

	t.Run("already_exists", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /repos/acme/widgets/git/ref/heads/main", func(w http.ResponseWriter, r *http.Request) {
			mustWrite(w, []byte(`{"object":{"sha":"basesha"}}`))
		})
		mux.HandleFunc("POST /repos/acme/widgets/git/refs", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			mustWrite(w, []byte(`{"message":"Reference already exists"}`))
		})
		c := newTestClient(t, mux)
		err := c.CreateBranch(context.Background(), testRepo, "wright/x", "main")
		providertest.AssertErrorIs(t, err, provider.ErrAlreadyExists)
	})
}

func TestDeleteBranch(t *testing.T) {
	var called bool
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /repos/acme/widgets/git/refs/heads/wright/x", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	c := newTestClient(t, mux)
	if err := c.DeleteBranch(context.Background(), testRepo, "wright/x"); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	if !called {
		t.Error("delete endpoint not called")
	}
}

func TestPushCommits(t *testing.T) {
	var treeBody map[string]any
	var commitBody, updateBody map[string]any
	blobCount := 0

	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/acme/widgets/git/ref/heads/wright/x", func(w http.ResponseWriter, r *http.Request) {
		mustWrite(w, []byte(`{"object":{"sha":"parentsha"}}`))
	})
	mux.HandleFunc("GET /repos/acme/widgets/git/commits/parentsha", func(w http.ResponseWriter, r *http.Request) {
		mustWrite(w, []byte(`{"sha":"parentsha","tree":{"sha":"basetree"}}`))
	})
	mux.HandleFunc("POST /repos/acme/widgets/git/blobs", func(w http.ResponseWriter, r *http.Request) {
		blobCount++
		w.WriteHeader(http.StatusCreated)
		mustWrite(w, []byte(`{"sha":"blobsha"}`))
	})
	mux.HandleFunc("POST /repos/acme/widgets/git/trees", func(w http.ResponseWriter, r *http.Request) {
		treeBody = decodeBody(t, r)
		w.WriteHeader(http.StatusCreated)
		mustWrite(w, []byte(`{"sha":"newtree"}`))
	})
	mux.HandleFunc("POST /repos/acme/widgets/git/commits", func(w http.ResponseWriter, r *http.Request) {
		commitBody = decodeBody(t, r)
		w.WriteHeader(http.StatusCreated)
		mustWrite(w, []byte(`{"sha":"newcommit"}`))
	})
	mux.HandleFunc("PATCH /repos/acme/widgets/git/refs/heads/wright/x", func(w http.ResponseWriter, r *http.Request) {
		updateBody = decodeBody(t, r)
		mustWrite(w, []byte(`{"object":{"sha":"newcommit"}}`))
	})

	c := newTestClient(t, mux)
	head, err := c.PushCommits(context.Background(), testRepo, "wright/x", providertest.StandardCommits())
	if err != nil {
		t.Fatalf("PushCommits: %v", err)
	}
	if head != "newcommit" {
		t.Errorf("head SHA = %q, want newcommit", head)
	}
	if blobCount != 1 {
		t.Errorf("created %d blobs, want 1 (only the non-deleted file)", blobCount)
	}
	if treeBody["base_tree"] != "basetree" {
		t.Errorf("base_tree = %v, want basetree", treeBody["base_tree"])
	}

	// The tree must carry both entries: the added file (with a blob sha) and the
	// deletion (path present, sha explicitly null).
	entries, _ := treeBody["tree"].([]any)
	if len(entries) != 2 {
		t.Fatalf("tree entries = %d, want 2: %v", len(entries), entries)
	}
	byPath := map[string]map[string]any{}
	for _, e := range entries {
		m := e.(map[string]any)
		byPath[m["path"].(string)] = m
	}
	added := byPath["docs/added.md"]
	if added["sha"] != "blobsha" {
		t.Errorf("added entry sha = %v, want blobsha", added["sha"])
	}
	del, ok := byPath["obsolete.txt"]
	if !ok {
		t.Fatal("deletion entry missing from tree")
	}
	if sha, present := del["sha"]; !present || sha != nil {
		t.Errorf("deletion entry sha = %v (present=%v), want explicit null", sha, present)
	}

	// Parent chaining and ref update. The Git create-commit API serializes
	// parents as an array of SHA strings.
	parents, _ := commitBody["parents"].([]any)
	if len(parents) != 1 || parents[0] != "parentsha" {
		t.Errorf("commit parents = %v, want [parentsha]", parents)
	}
	if commitBody["tree"] != "newtree" {
		t.Errorf("commit tree = %v, want newtree", commitBody["tree"])
	}
	if updateBody["sha"] != "newcommit" {
		t.Errorf("update ref sha = %v, want newcommit", updateBody["sha"])
	}
}

func TestFindOpenPullRequestByHead(t *testing.T) {
	var gotHead, gotState string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/acme/widgets/pulls", func(w http.ResponseWriter, r *http.Request) {
		gotHead = r.URL.Query().Get("head")
		gotState = r.URL.Query().Get("state")
		mustWrite(w, []byte(`[{"number":17,"html_url":"https://github.com/acme/widgets/pull/17","head":{"ref":"wright/issue-101"},"base":{"ref":"main"}}]`))
	})
	c := newTestClient(t, mux)
	pr, err := c.FindOpenPullRequestByHead(context.Background(), testRepo, "wright/issue-101")
	if err != nil {
		t.Fatalf("FindOpenPullRequestByHead: %v", err)
	}
	if gotHead != "acme:wright/issue-101" || gotState != "open" {
		t.Fatalf("query head/state = %q/%q", gotHead, gotState)
	}
	if pr == nil || pr.Number != 17 {
		t.Fatalf("pr = %+v, want #17", pr)
	}
}

func TestFindOpenPullRequestByHeadNone(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/acme/widgets/pulls", func(w http.ResponseWriter, r *http.Request) {
		mustWrite(w, []byte(`[]`))
	})
	c := newTestClient(t, mux)
	pr, err := c.FindOpenPullRequestByHead(context.Background(), testRepo, "wright/issue-101")
	if err != nil {
		t.Fatalf("FindOpenPullRequestByHead: %v", err)
	}
	if pr != nil {
		t.Fatalf("pr = %+v, want nil", pr)
	}
}

func TestOpenPullRequest(t *testing.T) {
	var body map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("POST /repos/acme/widgets/pulls", func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		w.WriteHeader(http.StatusCreated)
		mustWrite(w, []byte(`{"number":7,"html_url":"https://github.com/acme/widgets/pull/7","head":{"ref":"wright/x"},"base":{"ref":"main"}}`))
	})
	c := newTestClient(t, mux)
	pr, err := c.OpenPullRequest(context.Background(), testRepo, provider.PullRequestSpec{
		Title:      "Fix the thing",
		Body:       "Resolves #101",
		HeadBranch: "wright/x",
		BaseBranch: "main",
		Draft:      true,
	})
	if err != nil {
		t.Fatalf("OpenPullRequest: %v", err)
	}
	if body["draft"] != true || body["head"] != "wright/x" || body["base"] != "main" {
		t.Errorf("request body = %v", body)
	}
	if pr.Number != 7 || pr.HeadBranch != "wright/x" || pr.BaseBranch != "main" {
		t.Errorf("pr = %+v", pr)
	}
}

func TestMergePullRequest(t *testing.T) {
	var mergeBody map[string]any
	deleteCalled := false
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /repos/acme/widgets/pulls/7/merge", func(w http.ResponseWriter, r *http.Request) {
		mergeBody = decodeBody(t, r)
		mustWrite(w, []byte(`{"merged":true,"sha":"mergedsha"}`))
	})
	mux.HandleFunc("GET /repos/acme/widgets/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		mustWrite(w, []byte(`{"number":7,"head":{"ref":"wright/x"}}`))
	})
	mux.HandleFunc("DELETE /repos/acme/widgets/git/refs/heads/wright/x", func(w http.ResponseWriter, r *http.Request) {
		deleteCalled = true
		w.WriteHeader(http.StatusNoContent)
	})
	c := newTestClient(t, mux)
	err := c.MergePullRequest(context.Background(), testRepo, 7, provider.MergeOptions{
		Method:       provider.MergeSquash,
		DeleteBranch: true,
	})
	if err != nil {
		t.Fatalf("MergePullRequest: %v", err)
	}
	if mergeBody["merge_method"] != "squash" {
		t.Errorf("merge_method = %v, want squash", mergeBody["merge_method"])
	}
	if !deleteCalled {
		t.Error("head branch was not deleted")
	}
}

func TestClosePullRequest(t *testing.T) {
	var body map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /repos/acme/widgets/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		mustWrite(w, []byte(`{"number":7,"state":"closed"}`))
	})
	c := newTestClient(t, mux)
	if err := c.ClosePullRequest(context.Background(), testRepo, 7); err != nil {
		t.Fatalf("ClosePullRequest: %v", err)
	}
	if body["state"] != "closed" {
		t.Errorf("state = %v, want closed", body["state"])
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
			mux := http.NewServeMux()
			mux.HandleFunc("GET /repos/acme/widgets", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				mustWrite(w, []byte(`{"message":"boom"}`))
			})
			c := newTestClient(t, mux)
			_, err := c.DefaultBranch(context.Background(), testRepo)
			providertest.AssertErrorIs(t, err, tc.want)
		})
	}
}

func TestGetIssue(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/acme/widgets/issues/101", func(w http.ResponseWriter, r *http.Request) {
		mustWrite(w, []byte(`{"number":101,"title":"Fix bug","body":"steps","state":"closed","user":{"login":"alice"}}`))
	})
	c := newTestClient(t, mux)
	iss, err := c.GetIssue(context.Background(), testRepo, 101)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if iss.Number != 101 || iss.State != "closed" || iss.Title != "Fix bug" {
		t.Errorf("issue = %+v", iss)
	}
}

func TestGetIssueNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/acme/widgets/issues/999", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		mustWrite(w, []byte(`{"message":"Not Found"}`))
	})
	c := newTestClient(t, mux)
	_, err := c.GetIssue(context.Background(), testRepo, 999)
	providertest.AssertErrorIs(t, err, provider.ErrNotFound)
}

func TestReadRepoFile(t *testing.T) {
	var gotRef string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/acme/widgets/contents/docs/adr/007-x.md", func(w http.ResponseWriter, r *http.Request) {
		gotRef = r.URL.Query().Get("ref")
		mustWrite(w, mustJSON(t, map[string]any{
			"type":     "file",
			"name":     "007-x.md",
			"path":     "docs/adr/007-x.md",
			"content":  base64.StdEncoding.EncodeToString([]byte("# ADR 007\n")),
			"encoding": "base64",
		}))
	})
	c := newTestClient(t, mux)
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

func TestReadRepoFileNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/acme/widgets/contents/missing.md", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		mustWrite(w, []byte(`{"message":"Not Found"}`))
	})
	c := newTestClient(t, mux)
	_, err := c.ReadRepoFile(context.Background(), testRepo, "", "missing.md")
	providertest.AssertErrorIs(t, err, provider.ErrNotFound)
}

func TestListRepoDir(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/acme/widgets/contents/docs/adr", func(w http.ResponseWriter, r *http.Request) {
		mustWrite(w, mustJSON(t, []map[string]any{
			{"type": "file", "name": "006-y.md", "path": "docs/adr/006-y.md"},
			{"type": "dir", "name": "archive", "path": "docs/adr/archive"},
		}))
	})
	c := newTestClient(t, mux)
	entries, err := c.ListRepoDir(context.Background(), testRepo, "", "docs/adr")
	if err != nil {
		t.Fatalf("ListRepoDir: %v", err)
	}
	if len(entries) != 2 || entries[0] != "006-y.md" || entries[1] != "archive/" {
		t.Errorf("entries = %v", entries)
	}
}

func TestSplitRepoInvalid(t *testing.T) {
	c := &Client{}
	_, err := c.DefaultBranch(context.Background(), provider.Repo{FullPath: "no-slash"})
	if err == nil {
		t.Fatal("expected error for malformed repo path")
	}
}
