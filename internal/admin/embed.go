// Package admin provides the Deployer type for managing the Mint admin
// CloudFormation stack (mint-admin). It handles VPC auto-discovery, stack
// create/update routing, idempotent no-ops, and stack event streaming.
package admin

import _ "embed"

// adminTemplate is the CloudFormation template for the Mint admin stack.
// Embedded at compile time so the binary carries the template without
// requiring file-system access at runtime.
//
//go:embed templates/admin-setup.yaml
var adminTemplate string
