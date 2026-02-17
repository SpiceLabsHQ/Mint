// Package cmd provides CLI commands for mint.
// This file defines the shared AWS client infrastructure used by
// PersistentPreRunE to initialize SDK clients once and share them
// across subcommands via context.
package cmd

import (
	"context"
	"fmt"
	"time"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/nicholasgasior/mint/internal/config"
	"github.com/nicholasgasior/mint/internal/identity"
)

// awsClients holds pre-initialized AWS SDK clients and resolved identity.
// Created once in PersistentPreRunE and stored on the command context.
type awsClients struct {
	ec2Client *ec2.Client
	ssmClient *ssm.Client
	icClient  *ec2instanceconnect.Client
	owner     string // resolved owner name (mint:owner tag value)
	ownerARN  string // resolved owner ARN (mint:owner-arn tag value)

	// mintConfig holds the loaded user preferences for instance type,
	// volume size, idle timeout, etc.
	mintConfig *config.Config
}

// awsClientsKey is the context key for storing awsClients.
type awsClientsKey struct{}

// awsClientsFromContext retrieves the awsClients from the context.
// Returns nil if no clients have been stored.
func awsClientsFromContext(ctx context.Context) *awsClients {
	v, _ := ctx.Value(awsClientsKey{}).(*awsClients)
	return v
}

// contextWithAWSClients returns a new context carrying the given awsClients.
func contextWithAWSClients(ctx context.Context, clients *awsClients) context.Context {
	return context.WithValue(ctx, awsClientsKey{}, clients)
}

// commandNeedsAWS returns true if the command requires AWS client
// initialization. Commands that operate locally (version, config, ssh-config,
// help) return false.
func commandNeedsAWS(cmdName string) bool {
	switch cmdName {
	case "version", "config", "set", "get", "ssh-config", "help":
		return false
	default:
		return true
	}
}

// initAWSClients loads the AWS SDK config, creates all SDK clients,
// resolves the caller identity, and loads the mint config. Returns
// an awsClients struct ready to be stored on the command context.
func initAWSClients(ctx context.Context) (*awsClients, error) {
	cfg, err := awscfg.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	// Resolve owner identity (ADR-0013).
	stsClient := sts.NewFromConfig(cfg)
	resolver := identity.NewResolver(stsClient)
	owner, err := resolver.Resolve(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve identity: %w", err)
	}

	// Load mint user preferences.
	mintCfg, err := config.Load(config.DefaultConfigDir())
	if err != nil {
		return nil, fmt.Errorf("load mint config: %w", err)
	}

	return &awsClients{
		ec2Client:  ec2.NewFromConfig(cfg),
		ssmClient:  ssm.NewFromConfig(cfg),
		icClient:   ec2instanceconnect.NewFromConfig(cfg),
		owner:      owner.Name,
		ownerARN:   owner.ARN,
		mintConfig: mintCfg,
	}, nil
}

// idleTimeout returns the configured idle timeout as a time.Duration.
func (c *awsClients) idleTimeout() time.Duration {
	if c.mintConfig == nil {
		return 60 * time.Minute
	}
	return time.Duration(c.mintConfig.IdleTimeoutMinutes) * time.Minute
}
