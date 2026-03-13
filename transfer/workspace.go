package main

import (
	"fmt"
	"regexp"
	"strings"
)

// workspaceRE enforces the same format as scripts/transfer.py and .github/workflows/terraform.yml.
// If the allowed format changes, update all three locations.
var workspaceRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,47}[a-z0-9]$`)

func validateWorkspace(name string) error {
	if !workspaceRE.MatchString(name) {
		return fmt.Errorf("invalid workspace name %q: must be 3-49 chars, lowercase letters, numbers, and hyphens only; cannot start or end with a hyphen", name)
	}
	if strings.Contains(name, "--") {
		return fmt.Errorf("invalid workspace name %q: cannot contain consecutive hyphens", name)
	}
	return nil
}

func resolve(workspace, project string) (bucketName, signingServiceAccount string) {
	return "secure-transfer-" + workspace,
		"st-signer-" + workspace + "@" + project + ".iam.gserviceaccount.com"
}
