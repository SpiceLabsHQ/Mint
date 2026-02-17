package tags

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestTagConstants(t *testing.T) {
	// Verify all 8 tag keys exist with correct values.
	tests := []struct {
		name     string
		got      string
		expected string
	}{
		{"TagMint", TagMint, "mint"},
		{"TagComponent", TagComponent, "mint:component"},
		{"TagVM", TagVM, "mint:vm"},
		{"TagOwner", TagOwner, "mint:owner"},
		{"TagOwnerARN", TagOwnerARN, "mint:owner-arn"},
		{"TagBootstrap", TagBootstrap, "mint:bootstrap"},
		{"TagHealth", TagHealth, "mint:health"},
		{"TagName", TagName, "Name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("got %q, want %q", tt.got, tt.expected)
			}
		})
	}
}

func TestComponentConstants(t *testing.T) {
	tests := []struct {
		name     string
		got      string
		expected string
	}{
		{"ComponentInstance", ComponentInstance, "instance"},
		{"ComponentVolume", ComponentVolume, "volume"},
		{"ComponentSecurityGroup", ComponentSecurityGroup, "security-group"},
		{"ComponentElasticIP", ComponentElasticIP, "elastic-ip"},
		{"ComponentProjectVolume", ComponentProjectVolume, "project-volume"},
		{"ComponentEFSAccessPoint", ComponentEFSAccessPoint, "efs-access-point"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("got %q, want %q", tt.got, tt.expected)
			}
		})
	}
}

func TestBootstrapStatusConstants(t *testing.T) {
	tests := []struct {
		name     string
		got      string
		expected string
	}{
		{"BootstrapPending", BootstrapPending, "pending"},
		{"BootstrapComplete", BootstrapComplete, "complete"},
		{"BootstrapFailed", BootstrapFailed, "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("got %q, want %q", tt.got, tt.expected)
			}
		})
	}
}

func TestTagBuilderBaseTags(t *testing.T) {
	// Builder without optional fields still includes all base tags.
	b := NewTagBuilder("alice", "arn:aws:iam::123456789012:user/alice", "default")
	tags := b.Build()

	expected := map[string]string{
		TagMint:     "true",
		TagOwner:    "alice",
		TagOwnerARN: "arn:aws:iam::123456789012:user/alice",
		TagVM:       "default",
		TagName:     "mint/alice/default",
	}

	tagMap := tagsToMap(tags)

	for key, want := range expected {
		got, ok := tagMap[key]
		if !ok {
			t.Errorf("missing tag %q", key)
			continue
		}
		if got != want {
			t.Errorf("tag %q = %q, want %q", key, got, want)
		}
	}

	// Base tags should not include component or bootstrap.
	if _, ok := tagMap[TagComponent]; ok {
		t.Error("base tags should not include component")
	}
	if _, ok := tagMap[TagBootstrap]; ok {
		t.Error("base tags should not include bootstrap")
	}
}

func TestTagBuilderNameFormat(t *testing.T) {
	tests := []struct {
		name     string
		owner    string
		vmName   string
		wantName string
	}{
		{"simple", "alice", "default", "mint/alice/default"},
		{"custom vm", "bob", "dev-box", "mint/bob/dev-box"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := NewTagBuilder(tt.owner, "arn:fake", tt.vmName).Build()
			tagMap := tagsToMap(tags)
			if got := tagMap[TagName]; got != tt.wantName {
				t.Errorf("Name tag = %q, want %q", got, tt.wantName)
			}
		})
	}
}

func TestTagBuilderWithComponent(t *testing.T) {
	components := []string{
		ComponentInstance,
		ComponentVolume,
		ComponentSecurityGroup,
		ComponentElasticIP,
		ComponentProjectVolume,
		ComponentEFSAccessPoint,
	}

	for _, comp := range components {
		t.Run(comp, func(t *testing.T) {
			tags := NewTagBuilder("alice", "arn:fake", "default").
				WithComponent(comp).
				Build()

			tagMap := tagsToMap(tags)

			// Component tag should be set.
			if got := tagMap[TagComponent]; got != comp {
				t.Errorf("component tag = %q, want %q", got, comp)
			}

			// Base tags still present.
			if got := tagMap[TagMint]; got != "true" {
				t.Errorf("mint tag = %q, want %q", got, "true")
			}
		})
	}
}

func TestTagBuilderWithBootstrap(t *testing.T) {
	statuses := []string{BootstrapPending, BootstrapComplete, BootstrapFailed}

	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			tags := NewTagBuilder("alice", "arn:fake", "default").
				WithBootstrap(status).
				Build()

			tagMap := tagsToMap(tags)
			if got := tagMap[TagBootstrap]; got != status {
				t.Errorf("bootstrap tag = %q, want %q", got, status)
			}
		})
	}
}

func TestTagBuilderFluent(t *testing.T) {
	// Builder supports chaining all optional methods.
	tags := NewTagBuilder("alice", "arn:fake", "default").
		WithComponent(ComponentInstance).
		WithBootstrap(BootstrapPending).
		Build()

	tagMap := tagsToMap(tags)

	if tagMap[TagComponent] != ComponentInstance {
		t.Errorf("component = %q, want %q", tagMap[TagComponent], ComponentInstance)
	}
	if tagMap[TagBootstrap] != BootstrapPending {
		t.Errorf("bootstrap = %q, want %q", tagMap[TagBootstrap], BootstrapPending)
	}
	if tagMap[TagMint] != "true" {
		t.Error("mint tag missing from chained build")
	}
}

func TestFilterByOwner(t *testing.T) {
	filters := FilterByOwner("alice")

	filterMap := filtersToMap(filters)

	// Must filter on mint=true and mint:owner=alice.
	assertFilterValue(t, filterMap, TagMint, "true")
	assertFilterValue(t, filterMap, TagOwner, "alice")

	// Should not filter on VM name.
	if _, ok := filterMap[TagVM]; ok {
		t.Error("FilterByOwner should not filter on VM name")
	}
}

func TestFilterByOwnerAndVM(t *testing.T) {
	filters := FilterByOwnerAndVM("alice", "dev-box")

	filterMap := filtersToMap(filters)

	assertFilterValue(t, filterMap, TagMint, "true")
	assertFilterValue(t, filterMap, TagOwner, "alice")
	assertFilterValue(t, filterMap, TagVM, "dev-box")
}

// --- helpers ---

func tagsToMap(tags []ec2types.Tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, tag := range tags {
		m[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return m
}

func filtersToMap(filters []ec2types.Filter) map[string][]string {
	m := make(map[string][]string, len(filters))
	for _, f := range filters {
		// Strip "tag:" prefix for easier assertion.
		key := aws.ToString(f.Name)
		if len(key) > 4 && key[:4] == "tag:" {
			key = key[4:]
		}
		m[key] = f.Values
	}
	return m
}

func assertFilterValue(t *testing.T, filterMap map[string][]string, key, wantValue string) {
	t.Helper()
	values, ok := filterMap[key]
	if !ok {
		t.Errorf("missing filter for %q", key)
		return
	}
	if len(values) != 1 || values[0] != wantValue {
		t.Errorf("filter %q values = %v, want [%q]", key, values, wantValue)
	}
}
