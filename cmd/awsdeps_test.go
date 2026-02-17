package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/nicholasgasior/mint/internal/config"
)

func TestCommandNeedsAWS(t *testing.T) {
	tests := []struct {
		name     string
		cmdName  string
		expected bool
	}{
		{"version does not need AWS", "version", false},
		{"config does not need AWS", "config", false},
		{"set does not need AWS", "set", false},
		{"get does not need AWS", "get", false},
		{"ssh-config does not need AWS", "ssh-config", false},
		{"help does not need AWS", "help", false},
		{"up needs AWS", "up", true},
		{"down needs AWS", "down", true},
		{"destroy needs AWS", "destroy", true},
		{"ssh needs AWS", "ssh", true},
		{"code needs AWS", "code", true},
		{"list needs AWS", "list", true},
		{"status needs AWS", "status", true},
		{"init needs AWS", "init", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := commandNeedsAWS(tt.cmdName)
			if got != tt.expected {
				t.Errorf("commandNeedsAWS(%q) = %v, want %v", tt.cmdName, got, tt.expected)
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
