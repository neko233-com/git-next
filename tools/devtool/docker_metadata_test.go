// Copyright 2026 git-next contributors. MIT license.
package main

import "testing"

func TestMissingDockerResourceRequiresExactResource(t *testing.T) {
	for _, tt := range []struct {
		kind, message string
		absent        bool
	}{
		{"network", "Error response from daemon: network owned-fixture not found", true},
		{"network", "Error: No such network: owned-fixture", true},
		{"volume", "Error response from daemon: get owned-fixture: no such volume", true},
		{"container", "Error: No such object: owned-fixture", true},
		{"image", "Error: No such image: owned-fixture", true},
		{"network", "Error response from daemon: network other-fixture not found", false},
		{"network", "Cannot connect to the Docker daemon", false},
		{"image", "context owned-fixture not found", false},
	} {
		if got := missingDockerResource(tt.kind, "owned-fixture", tt.message); got != tt.absent {
			t.Errorf("%s: %q: absent=%v", tt.kind, tt.message, got)
		}
	}
}
