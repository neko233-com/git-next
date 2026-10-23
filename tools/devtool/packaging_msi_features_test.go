// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func msiFeatureMembership(t *testing.T, fragment *wixNode) map[string]string {
	t.Helper()
	membership := map[string]string{}
	var featureGroup *wixNode
	for _, node := range fragment.children {
		if node.name == "FeatureGroup" {
			if featureGroup != nil || node.attrs["Id"] != "PayloadFeatures" {
				t.Fatal("payload must expose exactly the referenced feature group")
			}
			featureGroup = node
		}
	}
	if featureGroup == nil {
		t.Fatal("missing payload features")
	}
	for _, feature := range featureGroup.children {
		if feature.name != "Feature" || !strings.HasPrefix(feature.attrs["Id"], "PayloadFeature_") || len(feature.attrs["Id"]) >= 38 || feature.attrs["Level"] != "1" || feature.attrs["Display"] != "hidden" || feature.attrs["InstallDefault"] != "followParent" {
			t.Fatalf("unexpected feature contract: %v", feature.attrs)
		}
		if len(feature.children) == 0 || len(feature.children) > 1600 {
			t.Fatalf("invalid component count %d", len(feature.children))
		}
		for _, ref := range feature.children {
			id := ref.attrs["Id"]
			if ref.name != "ComponentRef" || id == "" || membership[id] != "" {
				t.Fatalf("invalid or repeated component reference: %v", ref)
			}
			membership[id] = feature.attrs["Id"]
		}
	}
	return membership
}

func msiNodeBytes(t *testing.T, node *wixNode) []byte {
	t.Helper()
	var data bytes.Buffer
	encoder := xml.NewEncoder(&data)
	if e := node.encode(encoder); e != nil {
		t.Fatal(e)
	}
	if e := encoder.Flush(); e != nil {
		t.Fatal(e)
	}
	return data.Bytes()
}

func TestMSIPayloadFeaturesKeepComponentDefinitionsAndMembership(t *testing.T) {
	build := func(reverse, extra bool) (*wixNode, *wixNode) {
		fragment := wn("Fragment", nil)
		group := fragment.add("ComponentGroup", map[string]string{"Id": "Payload"})
		for i := 0; i < 4000; i++ {
			n := i
			if reverse {
				n = 3999 - i
			}
			id := fmt.Sprintf("Payload_%06d", n)
			component := group.add("Component", map[string]string{"Id": id, "Guid": uuid5MSI(id), "Directory": "BINDIR"})
			component.add("RegistryValue", map[string]string{"Name": id, "KeyPath": "yes"})
		}
		if extra {
			group.add("Component", map[string]string{"Id": "Payload_added_later"})
		}
		return fragment, group
	}
	fragment, group := build(false, false)
	before := msiNodeBytes(t, group)
	if e := payloadMSIFeatures(fragment, group); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(before, msiNodeBytes(t, group)) {
		t.Fatal("feature splitting changed component definitions")
	}
	membership := msiFeatureMembership(t, fragment)
	if len(membership) != 4000 {
		t.Fatalf("lost components: %d", len(membership))
	}
	for _, scenario := range []struct{ reverse, extra bool }{{true, false}, {false, true}, {true, true}} {
		other, otherGroup := build(scenario.reverse, scenario.extra)
		if e := payloadMSIFeatures(other, otherGroup); e != nil {
			t.Fatal(e)
		}
		got := msiFeatureMembership(t, other)
		for id, feature := range membership {
			if got[id] != feature {
				t.Fatalf("component %s moved feature under reorder/addition", id)
			}
		}
		if !scenario.extra {
			if !reflect.DeepEqual(got, membership) || !bytes.Equal(msiNodeBytes(t, fragment.children[1]), msiNodeBytes(t, other.children[1])) {
				t.Fatal("feature references must be deterministic under component reordering")
			}
		}
	}
}

func TestMSIPayloadFeatureLimitFailsClosed(t *testing.T) {
	fragment := wn("Fragment", nil)
	group := fragment.add("ComponentGroup", map[string]string{"Id": "Payload"})
	var last string
	for i := 0; len(group.children) < 1601; i++ {
		id := fmt.Sprintf("Payload_collision_%d", i)
		hash := sha256.Sum256([]byte(id))
		if hash[0]&63 == 0 {
			group.add("Component", map[string]string{"Id": id})
			last = id
		}
	}
	group.children = group.children[:1600]
	if e := payloadMSIFeatures(fragment, group); e != nil {
		t.Fatalf("documented limit should be supported: %v", e)
	}
	if len(msiFeatureMembership(t, fragment)) != 1600 {
		t.Fatal("boundary lost component references")
	}
	fragment = wn("Fragment", nil)
	group.add("Component", map[string]string{"Id": last})
	if e := payloadMSIFeatures(fragment, group); e == nil || !strings.Contains(e.Error(), "exceeds 1600 components") {
		t.Fatalf("feature overflow did not fail closed: %v", e)
	}
}

func TestMSIPayloadFeaturesCoverNativeAndLegacyScopes(t *testing.T) {
	source := filepath.Join(t.TempDir(), "payload.exe")
	if e := write(source, []byte("MZfixture"), 0644); e != nil {
		t.Fatal(e)
	}
	files := map[string]string{"bin/git.exe": source}
	for i := 0; i < 1700; i++ {
		files[fmt.Sprintf("runtime/share/tool-%04d.dat", i)] = source
	}
	for _, legacy := range []bool{false, true} {
		for _, scope := range []string{"user", "machine"} {
			t.Run(fmt.Sprintf("legacy=%t/%s", legacy, scope), func(t *testing.T) {
				node, e := payloadWix(files, record{}, "windows-x64", "1.0.3", scope, "/fixture", legacy)
				if e != nil {
					t.Fatal(e)
				}
				fragment := node.children[0]
				membership := msiFeatureMembership(t, fragment)
				group := fragment.children[1]
				if len(group.children) != len(membership) || len(membership) < len(files) {
					t.Fatalf("missing generated component references: %d definitions, %d references", len(group.children), len(membership))
				}
				for _, component := range group.children {
					if membership[component.attrs["Id"]] == "" {
						t.Fatal("unreferenced generated component")
					}
				}
			})
		}
	}
}

func TestMSIPayloadTableReadbackRejectsLinkerLossAndInvalidLayout(t *testing.T) {
	files := [][]string{{"File_A"}, {"File_B"}}
	components := [][]string{{"Integration"}, {"Component_A"}, {"Component_B"}}
	features := [][]string{{"ProductFeature", ""}, {"PayloadFeature_00", "ProductFeature"}}
	membership := [][]string{{"ProductFeature", "Integration"}, {"PayloadFeature_00", "Component_A"}, {"PayloadFeature_00", "Component_B"}}
	sequence := [][]string{{"InstallInitialize", "1500"}, {"InstallExecute", "6500"}, {"RemoveExistingProducts", "6501"}, {"InstallFinalize", "6600"}}
	if e := checkMSIPayloadTables(files, components, features, membership, sequence, 2); e != nil {
		t.Fatal(e)
	}
	for name, verify := range map[string]func() error{
		"linker discarded payload": func() error {
			return checkMSIPayloadTables(nil, components[:1], features[:1], membership[:1], sequence, 2)
		},
		"unassigned component": func() error { return checkMSIPayloadTables(files, components, features, membership[:2], sequence, 2) },
		"component shared across features": func() error {
			return checkMSIPayloadTables(files, components, features, append(membership, []string{"ProductFeature", "Component_A"}), sequence, 2)
		},
		"payload feature orphan": func() error {
			return checkMSIPayloadTables(files, components, [][]string{{"ProductFeature", ""}, {"PayloadFeature_00", "UnknownParent"}}, membership, sequence, 2)
		},
		"old product removed before execution": func() error {
			return checkMSIPayloadTables(files, components, features, membership, [][]string{{"InstallInitialize", "1500"}, {"InstallExecute", "6500"}, {"RemoveExistingProducts", "1501"}, {"InstallFinalize", "6600"}}, 2)
		},
		"feature capacity exceeded": func() error {
			var manyComponents, manyMembership [][]string
			for i := 0; i < 1601; i++ {
				id := fmt.Sprintf("Component_%d", i)
				manyComponents = append(manyComponents, []string{id})
				manyMembership = append(manyMembership, []string{"PayloadFeature_00", id})
			}
			return checkMSIPayloadTables(files, manyComponents, features, manyMembership, sequence, 2)
		},
	} {
		t.Run(name, func(t *testing.T) {
			if e := verify(); e == nil {
				t.Fatal("invalid compiled MSI accepted")
			}
		})
	}
}
