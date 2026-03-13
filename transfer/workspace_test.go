package main

import (
	"testing"
)

func TestValidateWorkspace(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid hyphenated name", "acme-q1-report", false},
		{"valid alphanumeric", "abc123", false},
		{"valid three chars", "abc", false},
		{"too short two chars", "ab", true},
		{"leading hyphen", "-invalid", true},
		{"trailing hyphen", "invalid-", true},
		{"uppercase letters", "Invalid-Name", true},
		{"consecutive hyphens", "double--hyphen", true},
		{"underscore", "invalid_name", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWorkspace(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateWorkspace(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	t.Run("bucket name", func(t *testing.T) {
		bucket, _ := resolve("acme-q1-report", "my-project")
		want := "secure-transfer-acme-q1-report"
		if bucket != want {
			t.Errorf("got bucket %q, want %q", bucket, want)
		}
	})

	t.Run("signing SA email", func(t *testing.T) {
		_, sa := resolve("acme-q1-report", "my-project")
		want := "st-signer-acme-q1-report@my-project.iam.gserviceaccount.com"
		if sa != want {
			t.Errorf("got SA %q, want %q", sa, want)
		}
	})

	t.Run("different workspaces give different buckets", func(t *testing.T) {
		bucketA, _ := resolve("client-a", "proj")
		bucketB, _ := resolve("client-b", "proj")
		if bucketA == bucketB {
			t.Errorf("expected different buckets, got %q for both", bucketA)
		}
	})

	t.Run("same workspace different projects give different SAs", func(t *testing.T) {
		_, saA := resolve("ws", "project-a")
		_, saB := resolve("ws", "project-b")
		if saA == saB {
			t.Errorf("expected different SAs, got %q for both", saA)
		}
	})
}
