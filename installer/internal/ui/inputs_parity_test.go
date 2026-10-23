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

package ui

// The tests in this file keep internal/inputs, which the Terraform module is
// generated from, in step with the screens: the fields each screen shows, and
// which values each accepts. They drive the real screens and compare them with
// inputs.Validate, so a change to a screen's field or rule fails here until
// internal/inputs (and with it the module, via `make generate`) follows.

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/ai-on-gke/substrate-gke/installer/internal/inputs"
	"github.com/ai-on-gke/substrate-gke/installer/internal/snapshot"
	"github.com/ai-on-gke/substrate-gke/installer/internal/state"
)

// advancedSourceApp is a test app on the advanced track building from
// source, which is when the project screen shows every field it has.
func advancedSourceApp(t *testing.T) *App {
	t.Helper()
	app := testApp(t)
	app.deps.Setup.Track = state.TrackAdvanced
	app.deps.Setup.ImageRepo, app.deps.Setup.ImageTag = "", ""
	return app
}

func TestInputsMatchProjectScreenFields(t *testing.T) {
	scr := newProjectScreen(advancedSourceApp(t).deps)
	want := inputs.OnScreen(state.Project)
	if len(scr.fields) != len(want) {
		var got []string
		for _, f := range scr.fields {
			got = append(got, f.label)
		}
		t.Fatalf("project screen fields %q; inputs declares %d", got, len(want))
	}
	for i, f := range scr.fields {
		in := want[i]
		if f.label != in.Label || f.input.Placeholder != in.Placeholder || f.input.CharLimit != in.MaxLen {
			t.Errorf("field %d: screen (%q, placeholder %q, limit %d); inputs.%s (%q, placeholder %q, limit %d)",
				i, f.label, f.input.Placeholder, f.input.CharLimit, in.Name, in.Label, in.Placeholder, in.MaxLen)
		}
		if def, ok := in.Default.(string); ok && f.input.Value() != def {
			t.Errorf("%s: screen starts at %q; inputs default is %q", in.Name, f.input.Value(), def)
		}
	}
}

func TestInputsMatchProjectScreenValidation(t *testing.T) {
	cases := []struct{ name, value string }{
		{"project_id", ""}, {"project_id", "  "}, {"project_id", "acme"},
		{"cluster_version", ""}, {"cluster_version", "1.30"}, {"cluster_version", "1.36.2-gke.1"},
		{"cluster_version", "latest"}, {"cluster_version", "2.0"},
		{"release_channel", ""}, {"release_channel", "regular"}, {"release_channel", "STABLE"},
		{"release_channel", "extended"}, {"release_channel", "none"},
		{"artifact_registry_repository", ""}, {"artifact_registry_repository", "my-images"},
		{"artifact_registry_repository", "My_Images"}, {"artifact_registry_repository", "images-"},
	}
	for _, c := range cases {
		t.Run(c.name+"="+strconv.Quote(c.value), func(t *testing.T) {
			app := advancedSourceApp(t)
			app.deps.Setup.ProjectID = "acme"
			scr := newProjectScreen(app.deps)
			set := make([]*inputs.Input, len(scr.fields))
			vals := inputs.Values{inputs.ImageSource.Name: inputs.ImagesSource}
			found := false
			for i, f := range scr.fields {
				set[i] = inputs.Lookup(nameOf(t, f.label))
				if set[i].Name == c.name {
					scr.fields[i].input.SetValue(c.value)
					found = true
				}
				vals[set[i].Name] = trimmed(scr.fields[i].input.Value())
			}
			if !found {
				t.Fatalf("no %s field on the project screen", c.name)
			}

			scr.submit()
			p := inputs.Validate(set, vals)
			switch {
			case p == nil && !scr.validating:
				t.Errorf("inputs accepts it; the screen refuses: %q", scr.errText)
			case p != nil && scr.validating:
				t.Errorf("the screen accepts it; inputs refuses: %s", p)
			case p != nil && scr.fields[scr.focus].label != p.Input.Label:
				t.Errorf("the screen blames %q; inputs blames %s", scr.fields[scr.focus].label, p.Input.Name)
			}
		})
	}
}

func TestInputsMatchImagesScreen(t *testing.T) {
	release := []*inputs.Input{inputs.ImageRepo, inputs.ImageTag, inputs.SubstrateRevision}
	scr := newImagesScreen(testApp(t).deps)
	scr.enterRelease()
	for i, in := range release {
		f := scr.fields[i]
		if scr.labels[i] != in.Label || f.Placeholder != in.Placeholder || f.CharLimit != in.MaxLen || f.Value() != in.Default {
			t.Errorf("release field %d: screen (%q, placeholder %q, limit %d, value %q); inputs.%s (%q, %q, %d, %q)",
				i, scr.labels[i], f.Placeholder, f.CharLimit, f.Value(), in.Name, in.Label, in.Placeholder, in.MaxLen, in.Default)
		}
	}

	ok := [3]string{snapshot.ReleaseRepo, snapshot.ReleaseVersion, snapshot.Commit}
	for _, c := range []struct {
		field int
		value string
	}{
		{0, ""}, {0, " "}, {1, ""}, {1, "bad tag"}, {1, "-v1"}, {1, "v" + strings.Repeat("1", 63)},
		{1, "v0.4.0_rc.1"}, {1, "v1@sha256:abc"}, {2, ""}, {2, snapshot.Commit},
	} {
		t.Run(fmt.Sprintf("%s=%q", release[c.field].Name, c.value), func(t *testing.T) {
			scr := newImagesScreen(testApp(t).deps)
			scr.enterRelease()
			vals := inputs.Values{inputs.ImageSource.Name: inputs.ImagesPrebuilt}
			for i, in := range release {
				v := ok[i]
				if i == c.field {
					v = c.value
				}
				scr.fields[i].SetValue(v)
				vals[in.Name] = trimmed(v)
			}
			scr.submitRelease()
			p := inputs.Validate(release, vals)
			switch {
			case p == nil && scr.errText != "":
				t.Errorf("inputs accepts it; the screen refuses: %q", scr.errText)
			case p != nil && scr.errText == "":
				t.Errorf("the screen accepts it; inputs refuses: %s", p)
			case p != nil && release[scr.focus] != p.Input:
				t.Errorf("the screen blames %s; inputs blames %s", release[scr.focus].Name, p.Input.Name)
			}
		})
	}
}

func TestInputsMatchClusterNameField(t *testing.T) {
	scr := newClusterScreen(testApp(t).deps)
	if scr.nameInput.CharLimit != inputs.ClusterName.MaxLen || scr.nameInput.Value() != inputs.ClusterName.Default {
		t.Errorf("cluster name: screen (limit %d, value %q); inputs (limit %d, default %q)",
			scr.nameInput.CharLimit, scr.nameInput.Value(), inputs.ClusterName.MaxLen, inputs.ClusterName.Default)
	}
	for _, name := range []string{"", "   ", "c", "substrate-test"} {
		t.Run(strconv.Quote(name), func(t *testing.T) {
			scr := newClusterScreen(testApp(t).deps)
			scr.loading, scr.mode = false, "name"
			scr.nameInput.SetValue(name)
			accepted := scr.Update(key("enter")) != nil
			valid := inputs.Validate([]*inputs.Input{inputs.ClusterName}, inputs.Values{inputs.ClusterName.Name: trimmed(name)}) == nil
			if accepted != valid {
				t.Errorf("screen accepts=%t, inputs accepts=%t", accepted, valid)
			}
		})
	}
}

func TestInputsMatchAutoscalingBounds(t *testing.T) {
	scr := newAutoscalingScreen(testApp(t).deps)
	for _, f := range []struct {
		limit int
		value string
		in    *inputs.Input
	}{{scr.minIn.CharLimit, scr.minIn.Value(), inputs.AutoscalingMinNodes}, {scr.maxIn.CharLimit, scr.maxIn.Value(), inputs.AutoscalingMaxNodes}} {
		if f.limit != f.in.MaxLen || f.value != fmt.Sprint(f.in.Default) {
			t.Errorf("%s: screen (limit %d, value %q); inputs (limit %d, default %v)", f.in.Name, f.limit, f.value, f.in.MaxLen, f.in.Default)
		}
	}
	bounds := []*inputs.Input{inputs.AutoscalingMinNodes, inputs.AutoscalingMaxNodes}
	for _, c := range [][2]int{{0, 1}, {1, 5}, {3, 3}, {0, 0}, {4, 3}, {-1, 5}, {9999, 9999}, {1, 9999}} {
		t.Run(fmt.Sprint(c), func(t *testing.T) {
			scr := newAutoscalingScreen(testApp(t).deps)
			scr.mode = "bounds"
			scr.minIn.SetValue(strconv.Itoa(c[0]))
			scr.maxIn.SetValue(strconv.Itoa(c[1]))
			scr.Update(key("enter"))
			accepted := scr.mode == "exec"
			valid := inputs.Validate(bounds, inputs.Values{
				inputs.AutoscalingEnabled.Name:  true,
				inputs.AutoscalingMinNodes.Name: c[0],
				inputs.AutoscalingMaxNodes.Name: c[1],
			}) == nil
			if accepted != valid {
				t.Errorf("screen accepts=%t, inputs accepts=%t", accepted, valid)
			}
		})
	}
}

// nameOf is the input a project screen field is, by its label.
func nameOf(t *testing.T, label string) string {
	t.Helper()
	for _, in := range inputs.OnScreen(state.Project) {
		if in.Label == label {
			return in.Name
		}
	}
	t.Fatalf("inputs declares no project field labelled %q", label)
	return ""
}

// trimmed is what the screens validate: the value as typed, trimmed.
func trimmed(s string) string { return strings.TrimSpace(s) }
