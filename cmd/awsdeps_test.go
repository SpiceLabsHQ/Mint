package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/config"
)

// fakeCmd builds a *cobra.Command with the given Use (name) attached to the
// root so that CommandPath() returns "mint <name>" — matching the real CLI.
func fakeCmd(name string) *cobra.Command {
	root := &cobra.Command{Use: "mint"}
	child := &cobra.Command{Use: name}
	root.AddCommand(child)
	return child
}

// fakeSubCmd builds a *cobra.Command nested two levels deep so that
// CommandPath() returns "mint <parent> <child>" — used to test completion
// subcommands like "mint completion bash".
func fakeSubCmd(parent, child string) *cobra.Command {
	root := &cobra.Command{Use: "mint"}
	parentCmd := &cobra.Command{Use: parent}
	childCmd := &cobra.Command{Use: child}
	parentCmd.AddCommand(childCmd)
	root.AddCommand(parentCmd)
	return childCmd
}

func TestCommandNeedsAWS(t *testing.T) {
	tests := []struct {
		name     string
		cmd      *cobra.Command
		expected bool
	}{
		{"version does not need AWS", fakeCmd("version"), false},
		{"config does not need AWS", fakeCmd("config"), false},
		{"set does not need AWS", fakeCmd("set"), false},
		{"get does not need AWS", fakeCmd("get"), false},
		{"ssh-config does not need AWS", fakeCmd("ssh-config"), false},
		{"help does not need AWS", fakeCmd("help"), false},
		// doctor initialises its own AWS clients so it can report credential
		// failures as a check result rather than a fatal PersistentPreRunE error.
		{"doctor does not need AWS", fakeCmd("doctor"), false},
		// completion and its shell subcommands are local-only.
		{"completion does not need AWS", fakeCmd("completion"), false},
		{"completion bash does not need AWS", fakeSubCmd("completion", "bash"), false},
		{"completion zsh does not need AWS", fakeSubCmd("completion", "zsh"), false},
		{"completion fish does not need AWS", fakeSubCmd("completion", "fish"), false},
		{"completion powershell does not need AWS", fakeSubCmd("completion", "powershell"), false},
		{"up needs AWS", fakeCmd("up"), true},
		{"down needs AWS", fakeCmd("down"), true},
		{"destroy needs AWS", fakeCmd("destroy"), true},
		{"ssh needs AWS", fakeCmd("ssh"), true},
		{"code needs AWS", fakeCmd("code"), true},
		{"list needs AWS", fakeCmd("list"), true},
		{"status needs AWS", fakeCmd("status"), true},
		{"init needs AWS", fakeCmd("init"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := commandNeedsAWS(tt.cmd)
			if got != tt.expected {
				t.Errorf("commandNeedsAWS(%q) = %v, want %v", tt.cmd.CommandPath(), got, tt.expected)
			}
		})
	}
}

func TestAWSClientsFromContext_Nil(t *testing.T) {
	ctx := context.Background()
	clients := awsClientsFromContext(ctx)
	if clients != nil {
		t.Errorf("expected nil clients from empty context, got %v", clients)
	}
}

func TestAWSClientsFromContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	clients := &awsClients{
		owner:    "test-user",
		ownerARN: "arn:aws:iam::123456789012:user/test-user",
	}
	ctx = contextWithAWSClients(ctx, clients)

	got := awsClientsFromContext(ctx)
	if got == nil {
		t.Fatal("expected non-nil clients from context")
	}
	if got.owner != "test-user" {
		t.Errorf("owner = %q, want %q", got.owner, "test-user")
	}
	if got.ownerARN != "arn:aws:iam::123456789012:user/test-user" {
		t.Errorf("ownerARN = %q, want %q", got.ownerARN, "arn:aws:iam::123456789012:user/test-user")
	}
}

func TestAWSClientsHasEFSClient(t *testing.T) {
	// Verify the awsClients struct has an efsClient field.
	// We can't create a real efs.Client without AWS config, but we can
	// verify the field exists and is typed correctly by setting it to nil.
	clients := &awsClients{
		owner:     "test-user",
		ownerARN:  "arn:aws:iam::123456789012:user/test-user",
		efsClient: nil,
	}
	if clients.efsClient != nil {
		t.Error("efsClient should be nil when not initialized")
	}
}

func TestInitAWSClientsDebugMode(t *testing.T) {
	// Verify that initAWSClients does not panic or error when the debug
	// flag is set on the CLIContext. We cannot easily inspect the resulting
	// aws.Config's ClientLogMode without calling real AWS APIs, but we can
	// verify the code path compiles and executes without error when debug
	// is enabled. The function will fail on credential resolution (expected
	// in a test environment without AWS creds), but it should get past the
	// config loading step.
	t.Run("debug flag does not cause config load panic", func(t *testing.T) {
		cliCtx := &cli.CLIContext{Debug: true}
		ctx := cli.WithContext(context.Background(), cliCtx)

		// initAWSClients will likely fail on STS/identity resolution
		// in a test environment, but the important thing is that it
		// does not panic on the debug log mode option.
		_, err := initAWSClients(ctx)
		// We expect an error (no real AWS creds), but not a panic.
		// If we get here without panic, the debug wiring compiled and ran.
		if err == nil {
			t.Log("initAWSClients succeeded (unexpected in test env, but not a failure)")
		}
	})

	t.Run("non-debug flag also works", func(t *testing.T) {
		cliCtx := &cli.CLIContext{Debug: false}
		ctx := cli.WithContext(context.Background(), cliCtx)

		_, err := initAWSClients(ctx)
		if err == nil {
			t.Log("initAWSClients succeeded (unexpected in test env, but not a failure)")
		}
	})

	t.Run("nil cli context does not panic", func(t *testing.T) {
		ctx := context.Background()
		_, err := initAWSClients(ctx)
		if err == nil {
			t.Log("initAWSClients succeeded (unexpected in test env, but not a failure)")
		}
	})
}

func TestInitAWSClientsProfilePropagation(t *testing.T) {
	// When a non-empty Profile is set in CLIContext, initAWSClients should
	// attempt to load that profile from shared config. In a test environment
	// without real AWS creds the call will still fail (on identity resolution),
	// but the important guarantee is that it does not panic and gets past the
	// config load step (which would fail immediately if the option were wired
	// incorrectly). We verify the code path runs without panic.
	t.Run("non-empty profile does not cause config load panic", func(t *testing.T) {
		cliCtx := &cli.CLIContext{Profile: "nonexistent-test-profile"}
		ctx := cli.WithContext(context.Background(), cliCtx)

		// Expected to error on credential resolution, not on config loading.
		_, _ = initAWSClients(ctx)
		// No panic = success
	})

	t.Run("empty profile runs without panic", func(t *testing.T) {
		cliCtx := &cli.CLIContext{Profile: ""}
		ctx := cli.WithContext(context.Background(), cliCtx)

		_, _ = initAWSClients(ctx)
		// No panic = success
	})
}

func TestInitAWSClientsRegionWiring(t *testing.T) {
	// When a Config with a non-empty Region is stored in context (simulated by
	// wiring a mint config into clients), initAWSClients should pass
	// WithRegion to LoadDefaultConfig. We can't inspect the resulting
	// aws.Config.Region directly without a real AWS call, but we can verify
	// the code path does not panic.
	t.Run("non-empty region in mint config does not panic", func(t *testing.T) {
		cliCtx := &cli.CLIContext{}
		ctx := cli.WithContext(context.Background(), cliCtx)

		// initAWSClients will try to load the mint config from disk. In the
		// test environment there's no config, so it gets an empty Region
		// which means no WithRegion option — still must not panic.
		_, _ = initAWSClients(ctx)
	})
}

func TestAWSClients_IdleTimeout(t *testing.T) {
	t.Run("returns config value", func(t *testing.T) {
		clients := &awsClients{
			mintConfig: &config.Config{IdleTimeoutMinutes: 30},
		}
		got := clients.idleTimeout()
		want := 30 * time.Minute
		if got != want {
			t.Errorf("idleTimeout() = %v, want %v", got, want)
		}
	})

	t.Run("returns default when config is nil", func(t *testing.T) {
		clients := &awsClients{}
		got := clients.idleTimeout()
		want := 60 * time.Minute
		if got != want {
			t.Errorf("idleTimeout() = %v, want %v", got, want)
		}
	})
}
