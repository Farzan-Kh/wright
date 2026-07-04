package verifier

import (
	"context"
	"errors"
	"testing"

	"github.com/farzan-kh/patchr/internal/sandbox"
)

func TestDetectCommand(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		files map[string]string
		want  string
		err   error
	}{
		{name: "go", files: map[string]string{"go.mod": "module x"}, want: "go test ./..."},
		{name: "npm", files: map[string]string{"package.json": `{"scripts":{"test":"vitest"}}`}, want: "npm test"},
		{name: "pytest_ini", files: map[string]string{"pytest.ini": "[pytest]"}, want: "pytest"},
		{name: "pytest_pyproject", files: map[string]string{"pyproject.toml": `[tool.pytest.ini_options]`}, want: "pytest"},
		{name: "make_test", files: map[string]string{"Makefile": "build:\n\ntest:\n\techo ok\n"}, want: "make test"},
		{name: "none", files: map[string]string{"README.md": "x"}, err: ErrNoCommand},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := &Verifier{}
			exec := &sandbox.FakeExec{Files: tc.files}
			got, err := v.DetectCommand(ctx, exec)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("err = %v, want %v", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("DetectCommand: %v", err)
			}
			if got != tc.want {
				t.Fatalf("command = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVerifyUsesOverride(t *testing.T) {
	ctx := context.Background()
	called := ""
	exec := &sandbox.FakeExec{BashFn: func(command string) (string, error) {
		called = command
		return "ok", nil
	}}
	v := &Verifier{OverrideCommand: "make ci-test"}
	out, err := v.Verify(ctx, exec)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if called != "make ci-test" {
		t.Fatalf("called %q, want make ci-test", called)
	}
	if out != "ok" {
		t.Fatalf("out = %q, want ok", out)
	}
}
