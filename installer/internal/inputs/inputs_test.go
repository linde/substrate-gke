// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package inputs

import (
	"regexp"
	"strings"
	"testing"

	"github.com/ai-on-gke/substrate-gke/installer/internal/snapshot"
	"github.com/ai-on-gke/substrate-gke/installer/internal/state"
)

// base is the least a valid set of values needs: the one input without a
// default.
var base = Values{"project_id": "my-project"}

func TestDefaultsAreValid(t *testing.T) {
	if p := Validate(All, base); p != nil {
		t.Fatalf("the defaults do not validate: %v", p)
	}
}

func TestNamesAreUniqueSnakeCase(t *testing.T) {
	ident := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	seen := map[string]bool{}
	for _, in := range All {
		if !ident.MatchString(in.Name) || seen[in.Name] {
			t.Errorf("input name %q is not a unique snake_case identifier", in.Name)
		}
		seen[in.Name] = true
		if in.Label == "" || in.Description == "" {
			t.Errorf("%s needs a label and a description", in.Name)
		}
		for _, r := range in.AllRules() {
			if r.When != nil && Lookup(r.When.Name) == nil {
				t.Errorf("%s: rule conditioned on unknown input %q", in.Name, r.When.Name)
			}
		}
	}
}

// TestImageTagRulesMatchCheckImageTag pins the image tag rules to the check
// the rest of the installer (the upgrade track) still uses.
func TestImageTagRulesMatchCheckImageTag(t *testing.T) {
	for _, tag := range []string{
		"v0.4.0-gke.0", "v1", "1", "a_b.c-d", "v1@sha256:abc", "v" + strings.Repeat("1", 62), "v" + strings.Repeat("1", 63),
		"", "@x", "-v1", "v1-", "v 1", "v1/2", "vé", ".", "_",
	} {
		ours := Validate([]*Input{ImageTag}, Values{"image_tag": tag}) == nil
		theirs := snapshot.CheckImageTag(tag) == nil
		if ours != theirs {
			t.Errorf("tag %q: inputs says valid=%t, CheckImageTag says valid=%t", tag, ours, theirs)
		}
	}
}

func TestRepositoryRuleMatchesState(t *testing.T) {
	for _, name := range []string{"", "a", "ate-images", "A", "1a", "a-", "a" + strings.Repeat("b", 62), "a" + strings.Repeat("b", 63)} {
		ours := Validate([]*Input{ArtifactRegistryRepository}, Values{"image_source": ImagesSource, "artifact_registry_repository": name}) == nil
		theirs := state.ValidateRepositoryName(name) == nil
		if ours != theirs {
			t.Errorf("repository %q: inputs says valid=%t, state says valid=%t", name, ours, theirs)
		}
	}
}

// TestEveryCustomRuleIsRegistered: a Custom rule naming a check nobody
// registered would panic in Validate.
func TestEveryCustomRuleIsRegistered(t *testing.T) {
	for _, in := range All {
		for _, r := range in.AllRules() {
			if r.Kind == Custom && CustomChecks[r.Check] == nil {
				t.Errorf("%s: custom check %q is not registered", in.Name, r.Check)
			}
		}
	}
}
