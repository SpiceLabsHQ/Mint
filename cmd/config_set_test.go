package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// TestConfigSetInstanceTypeCredentialError verifies that when the AWS
// validator returns a credential-related error, config set instance_type
// surfaces a friendly message instead of the raw SDK error chain.
func TestConfigSetInstanceTypeCredentialError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	// Simulate a raw AWS SDK credential error like the one produced when
	// IMDS is unreachable and no credentials are configured.
	awsCredErr := errors.New(
		"operation error EC2: DescribeInstanceTypes, exceeded maximum number of attempts, 3, " +
			"get identity: get credentials: failed to refresh cached credentials, " +
			"no EC2 IMDS role found, operation error ec2imds: GetMetadata, " +
			"exceeded maximum number of attempts, 3, request send failed, " +
			`Get "http://169.254.169.254/latest/meta-data/iam/security-credentials/": ` +
			"dial tcp 169.254.169.254:80: connect: connection refused",
	)

	// Inject the mock validator so the real EC2 client is never called.
	orig := instanceTypeValidatorOverride
	instanceTypeValidatorOverride = func(_, _ string) error { return awsCredErr }
	defer func() { instanceTypeValidatorOverride = orig }()

	// Also set a region so the validator is actually invoked.
	setupDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", setupDir)

	// Pre-set a region so that config_set.go wires the validator.
	regionBuf := new(bytes.Buffer)
	regionCmd := NewRootCommand()
	regionCmd.SetOut(regionBuf)
	regionCmd.SetErr(regionBuf)
	regionCmd.SetArgs([]string{"config", "set", "region", "us-east-1"})
	if err := regionCmd.Execute(); err != nil {
		t.Fatalf("failed to set region: %v", err)
	}

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "set", "instance_type", "t3.micro"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when AWS credentials are unavailable, got nil")
	}

	errMsg := err.Error()

	// Must contain the friendly message.
	wantContains := `cannot validate instance type: AWS credentials unavailable`
	if !strings.Contains(errMsg, wantContains) {
		t.Errorf("error message = %q\nwant it to contain: %q", errMsg, wantContains)
	}

	// Must NOT expose raw SDK internals.
	for _, leak := range []string{
		"169.254.169.254",
		"exceeded maximum number of attempts",
		"ec2imds",
		"get credentials",
		"failed to refresh cached credentials",
	} {
		if strings.Contains(errMsg, leak) {
			t.Errorf("error message leaks raw SDK detail %q: %s", leak, errMsg)
		}
	}
}

// TestConfigSetInstanceTypeCredentialErrorKeywords verifies each individual
// credential keyword triggers the friendly error.
func TestConfigSetInstanceTypeCredentialErrorKeywords(t *testing.T) {
	credentialKeywords := []string{
		"get credentials",
		"NoCredentialProviders",
		"no EC2 IMDS role found",
		"failed to refresh cached credentials",
		"credential",
	}

	for _, kw := range credentialKeywords {
		kw := kw
		t.Run(kw, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("MINT_CONFIG_DIR", dir)

			// Pre-set region so validator is wired.
			regionCmd := NewRootCommand()
			regionBuf := new(bytes.Buffer)
			regionCmd.SetOut(regionBuf)
			regionCmd.SetErr(regionBuf)
			regionCmd.SetArgs([]string{"config", "set", "region", "us-east-1"})
			if err := regionCmd.Execute(); err != nil {
				t.Fatalf("failed to set region: %v", err)
			}

			orig := instanceTypeValidatorOverride
			instanceTypeValidatorOverride = func(_, _ string) error {
				return errors.New("some aws error: " + kw + " somewhere in chain")
			}
			defer func() { instanceTypeValidatorOverride = orig }()

			buf := new(bytes.Buffer)
			rootCmd := NewRootCommand()
			rootCmd.SetOut(buf)
			rootCmd.SetErr(buf)
			rootCmd.SetArgs([]string{"config", "set", "instance_type", "t3.micro"})

			err := rootCmd.Execute()
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			if !strings.Contains(err.Error(), "cannot validate instance type: AWS credentials unavailable") {
				t.Errorf("keyword %q: error = %q, want friendly message", kw, err.Error())
			}
		})
	}
}

// TestConfigSetInstanceTypeNonCredentialErrorPassesThrough verifies that
// non-credential validator errors (e.g. invalid instance type) are NOT
// swallowed by the credential check.
func TestConfigSetInstanceTypeNonCredentialErrorPassesThrough(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	// Pre-set region so validator is wired.
	regionCmd := NewRootCommand()
	regionBuf := new(bytes.Buffer)
	regionCmd.SetOut(regionBuf)
	regionCmd.SetErr(regionBuf)
	regionCmd.SetArgs([]string{"config", "set", "region", "us-east-1"})
	if err := regionCmd.Execute(); err != nil {
		t.Fatalf("failed to set region: %v", err)
	}

	orig := instanceTypeValidatorOverride
	instanceTypeValidatorOverride = func(_, _ string) error {
		return errors.New(`instance type "t3.nonexistent" is not available in us-east-1`)
	}
	defer func() { instanceTypeValidatorOverride = orig }()

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "set", "instance_type", "t3.nonexistent"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid instance type, got nil")
	}

	// Should NOT be the credential message.
	if strings.Contains(err.Error(), "AWS credentials unavailable") {
		t.Errorf("non-credential error was incorrectly mapped to credential error: %s", err.Error())
	}
}

// TestConfigSetOtherKeysUnaffectedByCredentialCheck verifies that other keys
// (region, volume_size_gb, etc.) are not processed through the credential
// check logic even if the error message happens to contain a keyword.
func TestConfigSetOtherKeysUnaffectedByCredentialCheck(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	// Invalid region value — error should be the normal validation error, not credential message.
	rootCmd.SetArgs([]string{"config", "set", "region", "not-a-region"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid region, got nil")
	}

	if strings.Contains(err.Error(), "AWS credentials unavailable") {
		t.Errorf("region error incorrectly mapped to credential error: %s", err.Error())
	}
}
