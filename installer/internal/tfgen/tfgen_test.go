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

package tfgen

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ai-on-gke/substrate-gke/installer/internal/chartgen"
	"github.com/ai-on-gke/substrate-gke/installer/internal/inputs"
	"github.com/ai-on-gke/substrate-gke/installer/internal/snapshot"
	"github.com/ai-on-gke/substrate-gke/installer/internal/state"
)

// base is the least a valid configuration needs: the one input without a
// default.
var base = inputs.Values{"project_id": "my-project"}

// conformanceCase is one configuration, run through inputs.Validate and through
// `terraform plan`. fail names the input inputs.Validate should report, with the
// index of the rule (into AllRules) it should fail; "" means valid.
type conformanceCase struct {
	name string
	vals inputs.Values
	fail string
	rule int
}

func valid(name string, vals inputs.Values) conformanceCase {
	return conformanceCase{name: name, vals: vals}
}
func invalid(name string, vals inputs.Values, input string, rule int) conformanceCase {
	return conformanceCase{name, vals, input, rule}
}

// Rule indexes count the implicit rules first: a String with a MaxLen has the
// length rule at 0, and every Int has the whole-number rule at 0.
var cases = []conformanceCase{
	valid("defaults", nil),

	// image_source
	valid("source build", inputs.Values{"image_source": "source"}),
	invalid("unknown image source", inputs.Values{"image_source": "docker"}, "image_source", 0),

	// image_repo
	invalid("image repo too long", inputs.Values{"image_repo": strings.Repeat("r", 201)}, "image_repo", 0),
	valid("image repo at the limit", inputs.Values{"image_repo": strings.Repeat("r", 200)}),
	invalid("prebuilt without a registry", inputs.Values{"image_repo": ""}, "image_repo", 1),
	invalid("prebuilt with a blank registry", inputs.Values{"image_repo": "   "}, "image_repo", 1),
	valid("source ignores the registry", inputs.Values{"image_source": "source", "image_repo": ""}),

	// image_tag
	invalid("image tag too long", inputs.Values{"image_tag": strings.Repeat("v", 201)}, "image_tag", 0),
	invalid("prebuilt without a tag", inputs.Values{"image_tag": ""}, "image_tag", 1),
	invalid("tag too long for a label", inputs.Values{"image_tag": "v" + strings.Repeat("1", 63)}, "image_tag", 2),
	valid("tag at the label limit", inputs.Values{"image_tag": "v" + strings.Repeat("1", 62)}),
	valid("digest is not part of the version", inputs.Values{"image_tag": "v" + strings.Repeat("1", 62) + "@sha256:" + strings.Repeat("a", 64)}),
	invalid("tag with a space", inputs.Values{"image_tag": "bad tag"}, "image_tag", 3),
	invalid("tag starting with a dot", inputs.Values{"image_tag": ".v1"}, "image_tag", 3),
	invalid("tag ending with a dash", inputs.Values{"image_tag": "v1-"}, "image_tag", 3),
	invalid("digest only", inputs.Values{"image_tag": "@sha256:abc"}, "image_tag", 3),
	valid("tag with underscores and dots", inputs.Values{"image_tag": "v0.4.0_rc.1"}),
	valid("source ignores the tag", inputs.Values{"image_source": "source", "image_tag": "bad tag"}),

	// substrate_revision
	invalid("prebuilt without a commit", inputs.Values{"substrate_revision": " "}, "substrate_revision", 1),
	valid("source build of HEAD", inputs.Values{"image_source": "source", "substrate_revision": ""}),
	invalid("revision too long", inputs.Values{"image_source": "source", "substrate_revision": strings.Repeat("a", 201)}, "substrate_revision", 0),

	// project_id
	invalid("no project", inputs.Values{"project_id": ""}, "project_id", 1),
	invalid("blank project", inputs.Values{"project_id": " \t"}, "project_id", 1),
	invalid("project too long", inputs.Values{"project_id": strings.Repeat("p", 97)}, "project_id", 0),

	// The free-text fields the wizard takes as typed, bar their limit.
	invalid("location too long", inputs.Values{"cluster_location": strings.Repeat("z", 97)}, "cluster_location", 0),
	valid("empty location", inputs.Values{"cluster_location": ""}),
	invalid("bucket too long", inputs.Values{"bucket_name": strings.Repeat("b", 97)}, "bucket_name", 0),
	invalid("machine type too long", inputs.Values{"machine_type": strings.Repeat("m", 97)}, "machine_type", 0),
	invalid("network too long", inputs.Values{"network": strings.Repeat("n", 97)}, "network", 0),
	invalid("subnetwork too long", inputs.Values{"subnetwork": strings.Repeat("s", 97)}, "subnetwork", 0),
	invalid("registry too long", inputs.Values{"ko_docker_repo": strings.Repeat("k", 97)}, "ko_docker_repo", 0),
	valid("multibyte at the limit", inputs.Values{"bucket_name": strings.Repeat("é", 96)}),

	// cluster_version
	valid("empty version is the default", inputs.Values{"cluster_version": ""}),
	valid("floor", inputs.Values{"cluster_version": "1.36"}),
	valid("floor patch", inputs.Values{"cluster_version": "1.36.2-gke.1100"}),
	valid("newer minor", inputs.Values{"cluster_version": "1.37"}),
	valid("newer major", inputs.Values{"cluster_version": "2.0"}),
	valid("alias", inputs.Values{"cluster_version": "latest"}),
	valid("bare major is unreadable", inputs.Values{"cluster_version": "1"}),
	valid("minor with a suffix is unreadable", inputs.Values{"cluster_version": "1.30-gke"}),
	valid("signed newer minor", inputs.Values{"cluster_version": "+1.40"}),
	invalid("below the floor", inputs.Values{"cluster_version": "1.35"}, "cluster_version", 1),
	invalid("below the floor, patch", inputs.Values{"cluster_version": "1.30.2-gke.1"}, "cluster_version", 1),
	invalid("older major", inputs.Values{"cluster_version": "0.99"}, "cluster_version", 1),
	invalid("signed older minor", inputs.Values{"cluster_version": "+1.3"}, "cluster_version", 1),
	invalid("version too long", inputs.Values{"cluster_version": strings.Repeat("1", 97)}, "cluster_version", 0),

	// release_channel
	valid("empty channel is the default", inputs.Values{"release_channel": ""}),
	valid("regular", inputs.Values{"release_channel": "regular"}),
	valid("channel in capitals", inputs.Values{"release_channel": "STABLE"}),
	invalid("extended channel", inputs.Values{"release_channel": "Extended"}, "release_channel", 2),
	invalid("no channel", inputs.Values{"release_channel": "none"}, "release_channel", 1),
	invalid("channel too long", inputs.Values{"release_channel": strings.Repeat("r", 97)}, "release_channel", 0),

	// artifact_registry_repository
	valid("default repository", inputs.Values{"image_source": "source", "artifact_registry_repository": ""}),
	valid("repository name", inputs.Values{"image_source": "source", "artifact_registry_repository": "my-images-2"}),
	invalid("repository with capitals", inputs.Values{"image_source": "source", "artifact_registry_repository": "Images"}, "artifact_registry_repository", 1),
	invalid("repository starting with a digit", inputs.Values{"image_source": "source", "artifact_registry_repository": "1images"}, "artifact_registry_repository", 1),
	invalid("repository ending with a dash", inputs.Values{"image_source": "source", "artifact_registry_repository": "images-"}, "artifact_registry_repository", 1),
	invalid("repository of 64", inputs.Values{"image_source": "source", "artifact_registry_repository": "a" + strings.Repeat("b", 63)}, "artifact_registry_repository", 1),
	valid("repository of 63", inputs.Values{"image_source": "source", "artifact_registry_repository": "a" + strings.Repeat("b", 62)}),
	valid("prebuilt ignores the repository", inputs.Values{"artifact_registry_repository": "Images"}),
	invalid("repository too long", inputs.Values{"artifact_registry_repository": strings.Repeat("a", 97)}, "artifact_registry_repository", 0),

	// cluster_name
	invalid("no cluster name", inputs.Values{"cluster_name": ""}, "cluster_name", 1),
	invalid("cluster name too long", inputs.Values{"cluster_name": strings.Repeat("c", 41)}, "cluster_name", 0),
	valid("existing cluster", inputs.Values{"create_cluster": false, "cluster_name": "prod"}),

	// autoscaling
	valid("autoscaling from zero", inputs.Values{"autoscaling_min_nodes": 0, "autoscaling_max_nodes": 1}),
	valid("autoscaling fixed", inputs.Values{"autoscaling_min_nodes": 3, "autoscaling_max_nodes": 3}),
	invalid("negative min", inputs.Values{"autoscaling_min_nodes": -1}, "autoscaling_min_nodes", 1),
	invalid("five-digit min", inputs.Values{"autoscaling_min_nodes": 10000, "autoscaling_max_nodes": 10000}, "autoscaling_min_nodes", 1),
	invalid("fractional min", inputs.Values{"autoscaling_min_nodes": 1.5}, "autoscaling_min_nodes", 0),
	invalid("zero max", inputs.Values{"autoscaling_min_nodes": 0, "autoscaling_max_nodes": 0}, "autoscaling_max_nodes", 1),
	invalid("max below min", inputs.Values{"autoscaling_min_nodes": 4, "autoscaling_max_nodes": 3}, "autoscaling_max_nodes", 2),
	invalid("fractional max", inputs.Values{"autoscaling_max_nodes": 2.5}, "autoscaling_max_nodes", 0),
	valid("skipped autoscaling ignores bounds", inputs.Values{"autoscaling_enabled": false, "autoscaling_min_nodes": 4, "autoscaling_max_nodes": 0}),

	// sandbox_class
	valid("micro-VM", inputs.Values{"sandbox_class": "microvm"}),
	invalid("unknown sandbox", inputs.Values{"sandbox_class": "kata"}, "sandbox_class", 0),

	// HCL escaping: none of these may be read as a template.
	valid("template sequences in free text", inputs.Values{"bucket_name": "${oops}%{if}", "network": `a"b\c`}),
}

func merged(c conformanceCase) inputs.Values {
	v := inputs.Values{}
	for k, x := range base {
		v[k] = x
	}
	for k, x := range c.vals {
		v[k] = x
	}
	return v
}

func TestValidateCases(t *testing.T) {
	for _, c := range cases {
		p := inputs.Validate(inputs.All, merged(c))
		switch {
		case c.fail == "" && p != nil:
			t.Errorf("%s: inputs.Validate = %v, want valid", c.name, p)
		case c.fail != "" && p == nil:
			t.Errorf("%s: inputs.Validate = valid, want %s rule %d to fail", c.name, c.fail, c.rule)
		case c.fail != "" && (p.Input.Name != c.fail || p.Rule != c.rule):
			t.Errorf("%s: inputs.Validate failed %s rule %d (%s), want %s rule %d", c.name, p.Input.Name, p.Rule, p.Message, c.fail, c.rule)
		}
	}
}

// TestEveryRuleHasAFailingCase keeps the conformance test honest: a rule no
// case fails is a rule whose HCL nothing checks.
func TestEveryRuleHasAFailingCase(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range cases {
		if c.fail != "" {
			covered[fmt.Sprintf("%s/%d", c.fail, c.rule)] = true
		}
	}
	for _, in := range inputs.All {
		for i := range in.AllRules() {
			if !covered[fmt.Sprintf("%s/%d", in.Name, i)] {
				t.Errorf("no case fails %s rule %d (%s)", in.Name, i, in.AllRules()[i].Message)
			}
		}
	}
}

// TestGeneratedFilesAreUpToDate is the build-step guard: the committed module
// must be exactly what the declarations render to.
//
// The charts are checked too when a checkout of the pinned Substrate commit is
// at hand: $SUBSTRATE_ROOT, or the one tfgen fetches into the user cache.
// Without one only the module's own files are checked, unless
// SUBSTRATE_REQUIRE_TERRAFORM is set.
func TestGeneratedFilesAreUpToDate(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	substrate := os.Getenv("SUBSTRATE_ROOT")
	if substrate == "" {
		if cache, err := os.UserCacheDir(); err == nil {
			dir := filepath.Join(cache, "substrate-gke", "tfgen-substrate-"+snapshot.ShortCommit())
			if _, err := os.Stat(filepath.Join(dir, snapshot.CompleteMarker)); err == nil {
				substrate = dir
			}
		}
	}
	if substrate == "" {
		if os.Getenv("SUBSTRATE_REQUIRE_TERRAFORM") != "" {
			t.Fatal("no Substrate checkout to render the charts from; set SUBSTRATE_ROOT or run `make generate`")
		}
		t.Log("no Substrate checkout at hand; not checking the charts")
	}
	files, err := GeneratedFiles(substrate)
	if err != nil {
		t.Fatal(err)
	}
	if substrate != "" {
		for _, dir := range OwnedDirs {
			filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					rel, _ := filepath.Rel(root, path)
					if _, ok := files[filepath.ToSlash(rel)]; !ok {
						t.Errorf("%s is no longer generated; run `make generate`", rel)
					}
				}
				return nil
			})
		}
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Errorf("%s: %v; run `make generate`", name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is out of date; run `make generate`", name)
		}
	}
}

// TestTerraformConformance runs every case through `terraform plan` on the
// generated variables and requires Terraform's verdict to match inputs.Validate's:
// the same input fails, with the same message. It needs a terraform (1.9 or
// later) on PATH and skips without one, unless SUBSTRATE_REQUIRE_TERRAFORM is
// set, as it should be in CI.
func TestTerraformConformance(t *testing.T) {
	tf, err := exec.LookPath("terraform")
	if err != nil {
		if os.Getenv("SUBSTRATE_REQUIRE_TERRAFORM") != "" {
			t.Fatal("terraform is not on PATH")
		}
		t.Skip("terraform is not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "variables.tf"), RenderVariables(), 0o644); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vals := merged(c)
			vars := filepath.Join(dir, fmt.Sprintf("case%d.tfvars.json", i))
			data, err := json.Marshal(vals)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(vars, data, 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(tf, "plan", "-json", "-input=false", "-lock=false", "-var-file="+vars)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
			out, _ := cmd.Output()
			tfFailures, other := planFailures(t, out)
			if len(other) > 0 {
				t.Fatalf("terraform reported errors that are not variable validation:\n%s", strings.Join(other, "\n"))
			}
			// inputs.Validate stops at the first failure; terraform reports every
			// failing rule of every variable. Compare at that grain: for
			// each input, the messages of all the rules it fails.
			for _, in := range inputs.All {
				want := goFailures(in, vals)
				got := tfFailures[in.Name]
				sort.Strings(want)
				sort.Strings(got)
				// Terraform skips a variable whose rules read a variable
				// that failed, so there it may report nothing at all.
				if len(got) == 0 && upstreamFailed(in, vals) {
					continue
				}
				if strings.Join(want, "\n") != strings.Join(got, "\n") {
					t.Errorf("%s:\n  go rejects with: %q\n  tf rejects with: %q", in.Name, want, got)
				}
			}
		})
	}
}

// upstreamFailed reports whether any other input in's rules read fails.
func upstreamFailed(in *inputs.Input, v inputs.Values) bool {
	for _, r := range in.AllRules() {
		for _, name := range references(in, r) {
			if name != in.Name && len(goFailures(inputs.Lookup(name), v)) > 0 {
				return true
			}
		}
	}
	return false
}

// goFailures is the message of every rule of in that v fails.
func goFailures(in *inputs.Input, v inputs.Values) []string {
	v = inputs.Resolve(v)
	var out []string
	for _, r := range in.AllRules() {
		if r.Applies(v) && !r.Passes(in, v) {
			out = append(out, r.Text(in, v))
		}
	}
	return out
}

var (
	variableDecl = regexp.MustCompile(`^variable "([^"]+)"`)
	checkedAt    = regexp.MustCompile(`\n\nThis was checked by the validation rule at variables\.tf:(\d+),`)
)

// planFailures reads `terraform plan -json` output into the messages of the
// variables that failed validation, keyed by variable, and any other error.
// The variable is found from the line of the validation rule that failed:
// the diagnostic's own range points into whichever file set the value.
func planFailures(t *testing.T, out []byte) (map[string][]string, []string) {
	t.Helper()
	// owner[i] is the variable declared at or before line i+1.
	var owner []string
	current := ""
	for _, l := range strings.Split(string(RenderVariables()), "\n") {
		if m := variableDecl.FindStringSubmatch(l); m != nil {
			current = m[1]
		}
		owner = append(owner, current)
	}
	failures := map[string][]string{}
	var other []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var line struct {
			Type       string `json:"type"`
			Diagnostic struct {
				Severity string `json:"severity"`
				Summary  string `json:"summary"`
				Detail   string `json:"detail"`
			} `json:"diagnostic"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.Type != "diagnostic" || line.Diagnostic.Severity != "error" {
			continue
		}
		d := line.Diagnostic
		loc := checkedAt.FindStringSubmatchIndex(d.Detail)
		if d.Summary != "Invalid value for variable" || loc == nil {
			other = append(other, d.Summary+": "+d.Detail)
			continue
		}
		n, _ := strconv.Atoi(d.Detail[loc[2]:loc[3]])
		if n < 1 || n > len(owner) || owner[n-1] == "" {
			other = append(other, d.Summary+": "+d.Detail)
			continue
		}
		failures[owner[n-1]] = append(failures[owner[n-1]], d.Detail[:loc[0]])
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return failures, other
}

// TestTerraformDerivations holds locals.tf to the Go it mirrors, by reading
// the module's setup output for configurations whose derived values differ.
func TestTerraformDerivations(t *testing.T) {
	tf, err := exec.LookPath("terraform")
	if err != nil {
		if os.Getenv("SUBSTRATE_REQUIRE_TERRAFORM") != "" {
			t.Fatal("terraform is not on PATH")
		}
		t.Skip("terraform is not on PATH")
	}
	// Plan locals.tf on its own, with what it reads: the module's resources
	// would need providers and credentials.
	module := t.TempDir()
	src := filepath.Join("..", "..", "..", ModuleDir)
	for _, name := range []string{"locals.tf", "defaults.tf", "variables.tf"} {
		data, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(module, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outputs := `output "setup" {
  value = {
    region                      = local.region
    bucket_name                 = local.bucket_name
    creates_artifact_repository = local.creates_artifact_repository
    build_repository            = local.prebuilt ? "" : local.build_repository
  }
}
`
	if err := os.WriteFile(filepath.Join(module, "outputs.tf"), []byte(outputs), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []inputs.Values{
		{"project_id": "acme", "cluster_name": "c", "cluster_location": "us-west1-c"},
		{"project_id": "acme", "cluster_name": "c", "cluster_location": "europe-west4", "image_source": "source"},
		{"project_id": "Acme-Project-With-A-Long-Name", "cluster_name": strings.Repeat("long", 10), "cluster_location": "us-central1-a", "image_source": "source", "artifact_registry_repository": "mine"},
		{"project_id": "acme", "bucket_name": "explicit", "image_source": "source", "ko_docker_repo": "gcr.io/acme/x"},
	} {
		t.Run(fmt.Sprint(c), func(t *testing.T) {
			v := inputs.Resolve(c)
			st := state.NewSetup()
			st.ProjectID, st.ClusterName, st.Zone = v["project_id"].(string), v["cluster_name"].(string), v["cluster_location"].(string)
			st.BucketName = v["bucket_name"].(string)
			st.KoDockerRepo = v["ko_docker_repo"].(string)
			st.ArtifactRegistryRepository = v["artifact_registry_repository"].(string)
			if v["image_source"] == inputs.ImagesPrebuilt {
				st.ImageRepo, st.ImageTag = v["image_repo"].(string), v["image_tag"].(string)
			}
			if err := st.ApplyProjectDefaults(); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{
				"region":                      st.Region(),
				"bucket_name":                 st.BucketName,
				"creates_artifact_repository": st.CreatesArtifactRepository(),
				"build_repository":            "",
			}
			if !st.Prebuilt() {
				want["build_repository"] = st.BuildRepository()
			}

			data, _ := json.Marshal(c)
			vars := filepath.Join(t.TempDir(), "vars.tfvars.json")
			if err := os.WriteFile(vars, data, 0o644); err != nil {
				t.Fatal(err)
			}
			// console crashes on these locals (terraform 1.9.6), so read the
			// setup output off a saved plan instead.
			planFile := filepath.Join(t.TempDir(), "plan")
			plan := exec.Command(tf, "plan", "-input=false", "-lock=false", "-var-file="+vars, "-out="+planFile)
			plan.Dir = module
			plan.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
			if out, err := plan.CombinedOutput(); err != nil {
				t.Fatalf("terraform plan: %v\n%s", err, out)
			}
			show := exec.Command(tf, "show", "-json", planFile)
			show.Dir = module
			out, err := show.Output()
			if err != nil {
				t.Fatalf("terraform show: %v", err)
			}
			var shown struct {
				PlannedValues struct {
					Outputs map[string]struct {
						Value map[string]any `json:"value"`
					} `json:"outputs"`
				} `json:"planned_values"`
			}
			if err := json.Unmarshal(out, &shown); err != nil {
				t.Fatal(err)
			}
			got := shown.PlannedValues.Outputs["setup"].Value
			for k, w := range want {
				if got[k] != w {
					t.Errorf("%s: terraform %v, go %v", k, got[k], w)
				}
			}
		})
	}
}

// TestExampleValidates keeps terraform/example working against the module.
// It validates rather than plans: a plan would configure the google provider,
// which needs credentials. init downloads the providers, so without a network
// (or a provider cache) the test skips, unless SUBSTRATE_REQUIRE_TERRAFORM
// is set.
func TestExampleValidates(t *testing.T) {
	tf, err := exec.LookPath("terraform")
	if err != nil {
		if os.Getenv("SUBSTRATE_REQUIRE_TERRAFORM") != "" {
			t.Fatal("terraform is not on PATH")
		}
		t.Skip("terraform is not on PATH")
	}
	example, err := filepath.Abs(filepath.Join("..", "..", "..", "terraform", "example"))
	if err != nil {
		t.Fatal(err)
	}
	// Keep .terraform and the lock file out of the checkout.
	data := t.TempDir()
	env := append(os.Environ(), "TF_IN_AUTOMATION=1", "TF_DATA_DIR="+data)
	run := func(args ...string) ([]byte, error) {
		cmd := exec.Command(tf, args...)
		cmd.Dir = example
		cmd.Env = env
		return cmd.CombinedOutput()
	}
	if out, err := run("init", "-input=false", "-backend=false", "-lockfile=readonly"); err != nil {
		if os.Getenv("SUBSTRATE_REQUIRE_TERRAFORM") == "" {
			t.Skipf("terraform init could not install the providers (offline?): %v\n%s", err, out)
		}
		t.Fatalf("terraform init: %v\n%s", err, out)
	}
	if out, err := run("validate", "-no-color"); err != nil {
		t.Fatalf("terraform validate: %v\n%s", err, out)
	}
}

// TestExampleServicesAreMinimal holds the example's APIs to a subset of the
// ones setup-gcp's bootstrap enables, which the wizard's install runs: the
// example deliberately enables fewer, but never one the install does not.
func TestExampleServicesAreMinimal(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	services := func(path, start string) []string {
		t.Helper()
		src, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		_, rest, ok := strings.Cut(string(src), start)
		if !ok {
			t.Fatalf("%s: no %q", path, start)
		}
		block, _, _ := strings.Cut(rest, "}")
		block, _, _ = strings.Cut(block, "]")
		got := regexp.MustCompile(`"([a-z0-9.-]+\.googleapis\.com)"`).FindAllStringSubmatch(block, -1)
		var out []string
		for _, m := range got {
			out = append(out, m[1])
		}
		sort.Strings(out)
		return out
	}
	setupGCP := services("tools/setup-gcp/cmd/api.go", "services := []string{")
	example := services("terraform/example/main.tf", "required_services = [")
	if len(setupGCP) == 0 || len(example) == 0 {
		t.Fatalf("could not read the lists: setup-gcp %v, example %v", setupGCP, example)
	}
	for _, svc := range example {
		if !slices.Contains(setupGCP, svc) {
			t.Errorf("terraform/example enables %s, which setup-gcp does not (%v)", svc, setupGCP)
		}
	}
	// The install talks to GKE, Artifact Registry and GCS whatever else is
	// trimmed.
	for _, svc := range []string{"container.googleapis.com", "artifactregistry.googleapis.com", "storage.googleapis.com"} {
		if !slices.Contains(example, svc) {
			t.Errorf("terraform/example must enable %s", svc)
		}
	}
}

// TestGCPFactsMatchSetupGCP holds inputs.GCP, which shapes the module's GCP
// resources, to the settings setup-gcp's bootstrap uses for the wizard.
func TestGCPFactsMatchSetupGCP(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "tools", "setup-gcp", "cmd")
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	block := func(src, start, end string) string {
		t.Helper()
		_, rest, ok := strings.Cut(src, start)
		if !ok {
			t.Fatalf("setup-gcp no longer has %q; update inputs.GCP and this test", start)
		}
		b, _, _ := strings.Cut(rest, end)
		return b
	}
	quoted := regexp.MustCompile(`"([^"]+)"`)
	strs := func(s string) []string {
		var out []string
		for _, m := range quoted.FindAllStringSubmatch(s, -1) {
			out = append(out, m[1])
		}
		sort.Strings(out)
		return out
	}
	sorted := func(in []string) []string {
		out := slices.Clone(in)
		sort.Strings(out)
		return out
	}
	check := func(what string, got, want []string) {
		t.Helper()
		if len(want) == 0 || strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%s: inputs.GCP %v, setup-gcp %v", what, got, want)
		}
	}
	cluster, iam, bucket := read("cluster.go"), read("iam.go"), read("bucket.go")

	check("beta APIs", sorted(GCP.BetaAPIs), strs(block(cluster, "var requiredBetaAPIs = []string{", "}")))
	var sysctls []string
	for k, v := range GCP.NodeSysctls {
		sysctls = append(sysctls, k, v)
	}
	check("node sysctls", sorted(sysctls), strs(block(cluster, "var requiredNodeSysctls = map[string]string{", "}")))
	if want := fmt.Sprintf("InitialNodeCount: %d,", GCP.InitialNodes); !strings.Contains(cluster, want) {
		t.Errorf("setup-gcp's node pool no longer has %q", want)
	}
	roles := func(s string) []string {
		var out []string
		for _, q := range strs(s) {
			if strings.HasPrefix(q, "roles/") {
				out = append(out, q)
			}
		}
		return out
	}
	check("node roles", sorted(GCP.NodeRoles), roles(block(iam, "func grantGkeNodePermissions", "if !changed1")))
	check("atelet roles", sorted(GCP.AteletRoles), roles(block(iam, "func grantAteletPermissions", "if !changed1")))
	check("bucket roles", sorted(GCP.BucketRoles), strs(block(bucket, "var snapshotBucketRoles = []string{", "}")))
	check("bucket subjects", sorted(GCP.BucketSubjects), strs(block(bucket, "var snapshotBucketSubjects = []string{", "}")))
}

// TestEveryCustomCheckHasHCL: every custom check inputs registers needs an HCL
// condition here, or the module could not render its rule.
func TestEveryCustomCheckHasHCL(t *testing.T) {
	for name := range inputs.CustomChecks {
		if customHCL[name] == "" {
			t.Errorf("custom check %q has no HCL in customHCL", name)
		}
	}
}

// TestChartsRender lints and renders the committed charts with helm, with
// stand-in key pools, so a template error fails here rather than at apply.
func TestChartsRender(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		if os.Getenv("SUBSTRATE_REQUIRE_TERRAFORM") != "" {
			t.Fatal("helm is not on PATH")
		}
		t.Skip("helm is not on PATH")
	}
	charts := filepath.Join("..", "..", "..", ChartsDir)
	values := filepath.Join(t.TempDir(), "values.yaml")
	v := "jwtIssuer: https://container.googleapis.com/v1/projects/p/locations/l/clusters/c\nkeyPools:\n"
	for _, p := range chartgen.KeyPools {
		v += "  " + p.Key + ": {pool: x, cert: c, key: k}\n"
	}
	if err := os.WriteFile(values, []byte(v), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"lint", filepath.Join(charts, "substrate-crds")},
		{"lint", filepath.Join(charts, "substrate"), "-f", values},
		{"template", "substrate", filepath.Join(charts, "substrate"), "-n", "ate-system", "-f", values},
	} {
		out, err := exec.Command(helm, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("helm %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		if args[0] == "template" {
			for _, want := range []string{"name: atelet-v0-4-0-gke-0", "--credential-provider-name=k8s.io", "-f /etc/substrate-setup/setup.sql", "name: actor-id-ca-certs"} {
				if !bytes.Contains(out, []byte(want)) {
					t.Errorf("rendered chart lacks %q", want)
				}
			}
		}
	}
}
