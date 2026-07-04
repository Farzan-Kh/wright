package gitops

import "testing"

func TestBranchName(t *testing.T) {
	if got := BranchName(42); got != "patchr/issue-42" {
		t.Fatalf("BranchName = %q, want patchr/issue-42", got)
	}
}

func TestInjectTokenIntoRemoteURL(t *testing.T) {
	got, err := InjectTokenIntoRemoteURL("https://github.com/acme/widgets.git", "tok")
	if err != nil {
		t.Fatalf("InjectTokenIntoRemoteURL: %v", err)
	}
	want := "https://x-access-token:tok@github.com/acme/widgets.git"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestInjectCredentialIntoRemoteURL(t *testing.T) {
	got, err := InjectCredentialIntoRemoteURL("https://gitlab.com/group/app.git", "oauth2", "tok")
	if err != nil {
		t.Fatalf("InjectCredentialIntoRemoteURL: %v", err)
	}
	want := "https://oauth2:tok@gitlab.com/group/app.git"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestInjectTokenIntoRemoteURLRejectsNonHTTPS(t *testing.T) {
	if _, err := InjectTokenIntoRemoteURL("ssh://git@github.com/acme/widgets.git", "tok"); err == nil {
		t.Fatal("expected error for non-https remote")
	}
}
