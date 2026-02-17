package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestPhase2CommandsRegistered(t *testing.T) {
	root := NewRootCommand()

	phase2Commands := []string{
		"mosh", "connect", "sessions", "key", "project", "extend",
	}

	registered := make(map[string]bool)
	for _, cmd := range root.Commands() {
		registered[cmd.Name()] = true
	}

	for _, name := range phase2Commands {
		if !registered[name] {
			t.Errorf("expected Phase 2 command %q to be registered on root", name)
		}
	}
}

func TestExistingCommandsStillRegistered(t *testing.T) {
	root := NewRootCommand()

	existingCommands := []string{
		"up", "destroy", "ssh", "code", "list", "status",
		"config", "init", "version", "down", "ssh-config",
	}

	registered := make(map[string]bool)
	for _, cmd := range root.Commands() {
		registered[cmd.Name()] = true
	}

	for _, name := range existingCommands {
		if !registered[name] {
			t.Errorf("expected existing command %q to still be registered on root", name)
		}
	}
}

func TestKeyHasAddSubcommand(t *testing.T) {
	root := NewRootCommand()

	var found bool
	for _, cmd := range root.Commands() {
		if cmd.Name() == "key" {
			for _, sub := range cmd.Commands() {
				if sub.Name() == "add" {
					found = true
				}
			}
		}
	}

	if !found {
		t.Error("expected 'key' command to have 'add' subcommand")
	}
}

func TestPhase3CommandsRegistered(t *testing.T) {
	root := NewRootCommand()

	phase3Commands := []string{
		"resize",
		"recreate",
	}

	registered := make(map[string]bool)
	for _, cmd := range root.Commands() {
		registered[cmd.Name()] = true
	}

	for _, name := range phase3Commands {
		if !registered[name] {
			t.Errorf("expected Phase 3 command %q to be registered on root", name)
		}
	}
}

func TestProjectHasSubcommands(t *testing.T) {
	root := NewRootCommand()

	expectedSubs := []string{"add", "list", "rebuild"}

	var projectCmd *cobra.Command
	for _, cmd := range root.Commands() {
		if cmd.Name() == "project" {
			projectCmd = cmd
			break
		}
	}

	if projectCmd == nil {
		t.Fatal("expected 'project' command to be registered on root")
	}

	subNames := make(map[string]bool)
	for _, sub := range projectCmd.Commands() {
		subNames[sub.Name()] = true
	}

	for _, name := range expectedSubs {
		if !subNames[name] {
			t.Errorf("expected 'project' command to have %q subcommand", name)
		}
	}
}
