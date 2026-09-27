package main

import "testing"

func TestReleaseAllowsBreak(t *testing.T) {
	tests := []struct {
		name        string
		base        string
		release     string
		wantAllowed bool
		wantReason  string
		wantErr     bool
	}{
		{
			name:        "v0 minor increase permits a break",
			base:        "v0.4.0",
			release:     "v0.5.0",
			wantAllowed: true,
			wantReason:  "the v0 minor version increased",
		},
		{
			name:    "v0 patch increase rejects a break",
			base:    "v0.4.0",
			release: "v0.4.1",
		},
		{
			name:        "major increase permits a break",
			base:        "v0.4.0",
			release:     "v1.0.0",
			wantAllowed: true,
			wantReason:  "the major version increased",
		},
		{
			name:    "stable minor increase rejects a break",
			base:    "v1.2.0",
			release: "v1.3.0",
		},
		{
			name:        "v0 prerelease minor increase permits a break",
			base:        "v0.4.0",
			release:     "v0.5.0-rc.1",
			wantAllowed: true,
			wantReason:  "the v0 minor version increased",
		},
		{
			name:    "invalid release tag is rejected",
			base:    "v0.4.0",
			release: "next",
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotAllowed, gotReason, err := releaseAllowsBreak(test.base, test.release)
			if (err != nil) != test.wantErr {
				t.Fatalf("releaseAllowsBreak() error = %v, want error %t", err, test.wantErr)
			}
			if err != nil {
				return
			}
			if gotAllowed != test.wantAllowed {
				t.Errorf("releaseAllowsBreak() allowed = %t, want %t", gotAllowed, test.wantAllowed)
			}
			if gotReason != test.wantReason {
				t.Errorf("releaseAllowsBreak() reason = %q, want %q", gotReason, test.wantReason)
			}
		})
	}
}

func TestValidateReleaseOrder(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		release string
		wantErr bool
	}{
		{name: "newer patch is ordered", base: "v0.4.0", release: "v0.4.1"},
		{name: "newer prerelease is ordered", base: "v0.4.0", release: "v0.5.0-rc.1"},
		{name: "same version is rejected", base: "v0.4.0", release: "v0.4.0", wantErr: true},
		{name: "older version is rejected", base: "v0.4.0", release: "v0.3.9", wantErr: true},
		{name: "prerelease at the baseline version is rejected", base: "v0.4.0", release: "v0.4.0-rc.1", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateReleaseOrder(test.base, test.release)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateReleaseOrder() error = %v, want error %t", err, test.wantErr)
			}
		})
	}
}
