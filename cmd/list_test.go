package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// makeInstanceWithTime creates a DescribeInstancesOutput with a launch time.
func makeInstanceWithTime(id, vmName, owner, state, ip, instanceType, bootstrap string, launchTime time.Time) *ec2.DescribeInstancesOutput {
	inst := ec2types.Instance{
		InstanceId:   aws.String(id),
		InstanceType: ec2types.InstanceType(instanceType),
		LaunchTime:   aws.Time(launchTime),
		State: &ec2types.InstanceState{
			Name: ec2types.InstanceStateName(state),
		},
		Tags: []ec2types.Tag{
			{Key: aws.String("mint"), Value: aws.String("true")},
			{Key: aws.String("mint:vm"), Value: aws.String(vmName)},
			{Key: aws.String("mint:owner"), Value: aws.String(owner)},
		},
	}
	if ip != "" {
		inst.PublicIpAddress = aws.String(ip)
	}
	if bootstrap != "" {
		inst.Tags = append(inst.Tags, ec2types.Tag{
			Key: aws.String("mint:bootstrap"), Value: aws.String(bootstrap),
		})
	}
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{inst}}},
	}
}

// makeMultiInstanceOutput creates a DescribeInstancesOutput with multiple instances.
func makeMultiInstanceOutput(instances ...ec2types.Instance) *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: instances}},
	}
}

func makeTestInstance(id, vmName, owner, state, ip, instanceType, bootstrap string, launchTime time.Time) ec2types.Instance {
	inst := ec2types.Instance{
		InstanceId:   aws.String(id),
		InstanceType: ec2types.InstanceType(instanceType),
		LaunchTime:   aws.Time(launchTime),
		State: &ec2types.InstanceState{
			Name: ec2types.InstanceStateName(state),
		},
		Tags: []ec2types.Tag{
			{Key: aws.String("mint"), Value: aws.String("true")},
			{Key: aws.String("mint:vm"), Value: aws.String(vmName)},
			{Key: aws.String("mint:owner"), Value: aws.String(owner)},
		},
	}
	if ip != "" {
		inst.PublicIpAddress = aws.String(ip)
	}
	if bootstrap != "" {
		inst.Tags = append(inst.Tags, ec2types.Tag{
			Key: aws.String("mint:bootstrap"), Value: aws.String(bootstrap),
		})
	}
	return inst
}

func TestListCommand(t *testing.T) {
	recentLaunch := time.Now().Add(-30 * time.Minute)
	oldLaunch := time.Now().Add(-3 * time.Hour)

	tests := []struct {
		name           string
		describe       *mockDescribeInstances
		owner          string
		idleTimeout    int
		jsonOutput     bool
		wantErr        bool
		wantErrContain string
		wantOutput     []string
		wantAbsent     []string
	}{
		{
			name: "single running VM table output",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-abc123", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
			},
			owner:       "alice",
			idleTimeout: 60,
			wantOutput:  []string{"default", "running", "1.2.3.4", "m6i.xlarge", "complete"},
		},
		{
			name: "empty list shows no VMs message",
			describe: &mockDescribeInstances{
				output: &ec2.DescribeInstancesOutput{},
			},
			owner:       "alice",
			idleTimeout: 60,
			wantOutput:  []string{"No VMs found"},
		},
		{
			name: "multiple VMs listed",
			describe: &mockDescribeInstances{
				output: makeMultiInstanceOutput(
					makeTestInstance("i-one", "default", "alice", "running", "1.1.1.1", "t3.medium", "complete", recentLaunch),
					makeTestInstance("i-two", "dev", "alice", "stopped", "", "m6i.xlarge", "complete", oldLaunch),
				),
			},
			owner:       "alice",
			idleTimeout: 60,
			wantOutput:  []string{"default", "dev", "running", "stopped", "t3.medium", "m6i.xlarge"},
		},
		{
			name: "idle timeout warning",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-idle", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", oldLaunch),
			},
			owner:       "alice",
			idleTimeout: 60, // 60 min timeout, VM running 3 hours
			wantOutput:  []string{"idle"},
		},
		{
			name: "no idle warning when under threshold",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-recent", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
			},
			owner:       "alice",
			idleTimeout: 60, // 60 min timeout, VM running 30 min
			wantAbsent:  []string{"idle"},
		},
		{
			name: "no idle warning for stopped VMs",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-stopped", "default", "alice", "stopped", "", "m6i.xlarge", "complete", oldLaunch),
			},
			owner:       "alice",
			idleTimeout: 60,
			wantAbsent:  []string{"idle"},
		},
		{
			name: "bootstrap failed indicator",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-fail", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "failed", recentLaunch),
			},
			owner:       "alice",
			idleTimeout: 60,
			wantOutput:  []string{"FAILED"},
		},
		{
			name: "json output format",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-abc123", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
			},
			owner:       "alice",
			idleTimeout: 60,
			jsonOutput:  true,
			wantOutput:  []string{`"id"`, `"name"`, `"state"`, `"i-abc123"`},
		},
		{
			name: "json output is valid JSON array",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-abc123", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
			},
			owner:       "alice",
			idleTimeout: 60,
			jsonOutput:  true,
		},
		{
			name: "json empty list",
			describe: &mockDescribeInstances{
				output: &ec2.DescribeInstancesOutput{},
			},
			owner:       "alice",
			idleTimeout: 60,
			jsonOutput:  true,
			wantOutput:  []string{"[]"},
		},
		{
			name: "API error propagates",
			describe: &mockDescribeInstances{
				err: errThrottled,
			},
			owner:          "alice",
			idleTimeout:    60,
			wantErr:        true,
			wantErrContain: "throttled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := new(bytes.Buffer)

			deps := &listDeps{
				describe:    tt.describe,
				owner:       tt.owner,
				idleTimeout: time.Duration(tt.idleTimeout) * time.Minute,
			}

			cmd := newListCommandWithDeps(deps)
			root := newTestRoot()
			root.AddCommand(cmd)
			root.SetOut(buf)
			root.SetErr(buf)

			args := []string{"list"}
			if tt.jsonOutput {
				args = append(args, "--json")
			}
			root.SetArgs(args)

			err := root.Execute()

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantErrContain != "" && !strings.Contains(err.Error(), tt.wantErrContain) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErrContain)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			output := buf.String()
			for _, want := range tt.wantOutput {
				if !strings.Contains(output, want) {
					t.Errorf("output missing %q, got:\n%s", want, output)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(output, absent) {
					t.Errorf("output should not contain %q, got:\n%s", absent, output)
				}
			}

			// Validate JSON output is parseable.
			if tt.jsonOutput {
				var result []interface{}
				if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
					t.Errorf("JSON output is not a valid array: %v\nOutput: %s", err, output)
				}
			}
		})
	}
}

// errThrottled is a reusable test error.
var errThrottled = errForTest("throttled")

type errForTest string

func (e errForTest) Error() string { return string(e) }
