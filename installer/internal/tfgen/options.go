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
	"github.com/ai-on-gke/substrate-gke/installer/internal/inputs"
	"github.com/ai-on-gke/substrate-gke/installer/internal/snapshot"
)

// Option is a setting only the Terraform module has: how Terraform should
// behave, not something the wizard asks. Options are declared here, next to
// the inputs, so the generator can render them into the module and the
// example alongside the inputs, but they are not part of the wizard's
// contract and have no validation to keep in step.
type Option struct {
	Name        string
	Description string
	Kind        inputs.Kind
	Default     any
}

// Options are the module's Terraform-only settings.
var Options = []*Option{
	{
		Name:        "install_substrate",
		Description: "Install Substrate onto the cluster (control plane, micro-VM assets, Filestore driver, demo) once the GCP resources exist. false stops after the GCP resources. The install runs on the machine running Terraform and needs bash, gcloud, kubectl, git and go there, plus sha256sum, zstd, unzip, jq and make for the micro-VM sandbox.",
		Kind:        inputs.Bool,
		Default:     true,
	},
	{
		Name:        "substrate_work_dir",
		Description: "Directory the install fetches Substrate source trees into. Empty means .substrate under the root module.",
		Kind:        inputs.String,
		Default:     "",
	},
	{
		Name:        "deletion_protection",
		Description: "Set GKE deletion protection on a cluster the module creates. Leave it off to let `terraform destroy` delete the cluster.",
		Kind:        inputs.Bool,
		Default:     false,
	},
	{
		Name:        "bucket_force_destroy",
		Description: "Let `terraform destroy` delete the snapshot bucket while it still holds snapshots and micro-VM assets.",
		Kind:        inputs.Bool,
		Default:     false,
	},
}

// constants are values the module's HCL needs from the installer, rendered
// into defaults.tf so they move with it.
func constants() []struct{ name, value string } {
	return []struct{ name, value string }{
		{"substrate_repo", snapshot.RepoURL},
	}
}
