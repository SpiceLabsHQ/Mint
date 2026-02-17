package identity

import "testing"

func TestNormalizeARN(t *testing.T) {
	tests := []struct {
		name     string
		arn      string
		wantName string
		wantErr  bool
	}{
		{
			name:     "IAM user",
			arn:      "arn:aws:iam::123456789012:user/ryan",
			wantName: "ryan",
		},
		{
			name:     "SSO assumed role with email",
			arn:      "arn:aws:sts::123456789012:assumed-role/AWSReservedSSO_PowerUserAccess_abc123/ryan@example.com",
			wantName: "ryan",
		},
		{
			name:     "generic assumed role with session name",
			arn:      "arn:aws:sts::123456789012:assumed-role/RoleName/session-name",
			wantName: "session-name",
		},
		{
			name:     "special characters replaced with hyphens",
			arn:      "arn:aws:iam::123456789012:user/Ryan.O'Brien",
			wantName: "ryan-o-brien",
		},
		{
			name:     "uppercase lowered",
			arn:      "arn:aws:iam::123456789012:user/RYAN",
			wantName: "ryan",
		},
		{
			name:     "email domain stripped from IAM user",
			arn:      "arn:aws:iam::123456789012:user/ryan@example.com",
			wantName: "ryan",
		},
		{
			name:     "consecutive special chars collapse to single hyphen",
			arn:      "arn:aws:iam::123456789012:user/ryan..smith",
			wantName: "ryan-smith",
		},
		{
			name:     "trailing hyphens stripped",
			arn:      "arn:aws:iam::123456789012:user/ryan.",
			wantName: "ryan",
		},
		{
			name:     "leading hyphens stripped",
			arn:      "arn:aws:iam::123456789012:user/.ryan",
			wantName: "ryan",
		},
		{
			name:     "root user ARN",
			arn:      "arn:aws:iam::123456789012:root",
			wantName: "root",
		},
		{
			name:    "empty ARN",
			arn:     "",
			wantErr: true,
		},
		{
			name:    "malformed ARN missing colon-separated fields",
			arn:     "not-an-arn",
			wantErr: true,
		},
		{
			name:     "federated user",
			arn:      "arn:aws:sts::123456789012:federated-user/developer",
			wantName: "developer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeARN(tt.arn)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeARN(%q) expected error, got %q", tt.arn, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeARN(%q) unexpected error: %v", tt.arn, err)
			}
			if got != tt.wantName {
				t.Errorf("NormalizeARN(%q) = %q, want %q", tt.arn, got, tt.wantName)
			}
		})
	}
}
