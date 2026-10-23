// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"testing"
)

func TestLifecycleRefusesEveryOccupiedUpgradeFamily(t *testing.T) {
	for _, tt := range []struct {
		name, upgrade string
		products      []string
		queryError    error
		pass          bool
	}{
		{"vacant", "family", nil, nil, true},
		{"missing identity", "", nil, nil, false},
		{"other installed version", "family", []string{"unrelated-version-same-upgrade-code"}, nil, false},
		{"unknown registration state", "family", nil, fmt.Errorf("query refused"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := unoccupiedMSIFamily(tt.upgrade, func(string) ([]string, error) { return tt.products, tt.queryError })
			if (e == nil) != tt.pass {
				t.Fatalf("isolation result: %v", e)
			}
		})
	}
}
