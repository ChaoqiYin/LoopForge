package application

import "testing"

func TestPublicOperationsOmitsLocalIssueWrites(t *testing.T) {
	for _, op := range PublicOperations() {
		if op.Name == "issues_save" {
			t.Fatal("local issue creation tool must not be exposed")
		}
	}
}

func TestPublicOperationsKeepsCompatibilitySync(t *testing.T) {
	found := false
	for _, op := range PublicOperations() {
		if op.Name == "issues_import_tapd" {
			found = true
		}
	}
	if !found {
		t.Fatal("TAPD compatibility import must remain available")
	}
}
