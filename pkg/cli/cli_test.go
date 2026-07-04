package cli_test

import (
	"errors"
	"os"
	"os/exec"
	"testing"

	"github.com/sbordeyne/vlbackup/pkg/cli"
)

func TestVersion(t *testing.T) {
	expectedVersion := "vlbackup 0.0.0-dev"
	if cli.Version != "0.0.0-dev" {
		t.Errorf("Expected version %s, got %s", expectedVersion, cli.Version)
	}
}

func TestArgsVersion(t *testing.T) {
	want := "vlbackup " + cli.Version
	if got := (cli.Args{}).Version(); got != want {
		t.Errorf("Args.Version() = %q, want %q", got, want)
	}
}

// TestGetCliArgs covers the happy path (no --help/--version/parse error, which
// call os.Exit and cannot run in-process). Env vars supply values.
func TestGetCliArgs(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"vlbackup"}

	t.Setenv("HOST", ":9999")
	t.Setenv("DATAPATH", "/custom/data")
	t.Setenv("TRANSFERAUTHKEY", "sekret")

	args := cli.GetCliArgs()
	if args.Host != ":9999" {
		t.Errorf("Host = %q, want :9999", args.Host)
	}
	if args.DataPath != "/custom/data" {
		t.Errorf("DataPath = %q, want /custom/data", args.DataPath)
	}
	if args.TransferAuthKey != "sekret" {
		t.Errorf("TransferAuthKey = %q, want sekret", args.TransferAuthKey)
	}
}

// TestGetCliArgsExitPaths covers the --help/--version/parse-error branches,
// each of which calls os.Exit. The test re-execs itself as a child that
// invokes GetCliArgs with the given flag, then asserts the child's exit code.
func TestGetCliArgsExitPaths(t *testing.T) {
	if os.Getenv("CLI_EXIT_MODE") == "1" {
		os.Args = []string{"vlbackup"}
		if arg := os.Getenv("CLI_EXIT_ARG"); arg != "" {
			os.Args = append(os.Args, arg)
		}
		cli.GetCliArgs()
		return
	}

	tests := []struct {
		name     string
		arg      string
		wantExit int
	}{
		{name: "help", arg: "--help", wantExit: 0},
		{name: "version", arg: "--version", wantExit: 0},
		{name: "parse error", arg: "--nonexistent-flag", wantExit: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestGetCliArgsExitPaths$")
			cmd.Env = append(os.Environ(), "CLI_EXIT_MODE=1", "CLI_EXIT_ARG="+tt.arg)
			err := cmd.Run()
			if tt.wantExit == 0 {
				if err != nil {
					t.Errorf("child exit = %v, want 0", err)
				}
				return
			}
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != tt.wantExit {
				t.Errorf("child exit = %v, want code %d", err, tt.wantExit)
			}
		})
	}
}
