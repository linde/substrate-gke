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
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/ai-on-gke/substrate-gke/installer/internal/gcp"
	"github.com/ai-on-gke/substrate-gke/installer/internal/snapshot"
	"github.com/ai-on-gke/substrate-gke/installer/internal/state"
)

// Values of the image_source input.
const (
	ImagesPrebuilt = "prebuilt"
	ImagesSource   = "source"
)

// defaults are the wizard's own starting values, so every consumer's
// defaults move whenever the wizard's do.
var defaults = state.NewSetup()

// The inputs, in wizard order. Adding a field to a screen means adding it
// here, reading its label, limit and rules from here in the screen, and
// running `make generate`.
//
// Not here, on purpose: the welcome screen's track (quickstart only hides
// the advanced fields, whose defaults then apply), the upgrade track (it
// touches nothing in GCP), and the confirmations screens ask after probing
// live state ("tear it down?", "stage without KVM?"). Those are decisions
// about what a probe found, not inputs.
var (
	// ─── Choose your images ──────────────────────────────────────────────

	ImageSource = &Input{
		Name:        "image_source",
		Screen:      state.Images,
		Label:       "Install pre-built images, or build from source",
		Description: `Where the Substrate images come from: "prebuilt" installs published images; "source" builds every image with ko from substrate_revision and pushes them to your own registry.`,
		Kind:        String,
		Default:     ImagesPrebuilt,
		Rules: []Rule{
			oneOf(`Must be "prebuilt" or "source".`, ImagesPrebuilt, ImagesSource),
		},
	}
	ImageRepo = &Input{
		Name:        "image_repo",
		Screen:      state.Images,
		Label:       "Image registry",
		Placeholder: snapshot.ReleaseRepo,
		Description: "Registry of the pre-built images. Ignored when image_source is \"source\".",
		Kind:        String,
		Default:     snapshot.ReleaseRepo,
		MaxLen:      200,
		Rules: []Rule{
			required("An image registry is required.").If(Is("image_source", ImagesPrebuilt)),
		},
	}
	ImageTag = &Input{
		Name:        "image_tag",
		Screen:      state.Images,
		Label:       "Image tag",
		Placeholder: snapshot.ReleaseVersion,
		Description: "Tag of the pre-built images, which is also the Substrate version: it becomes the ate.dev/substrate-version node label. Ignored when image_source is \"source\".",
		Kind:        String,
		Default:     snapshot.ReleaseVersion,
		MaxLen:      200,
		Rules: []Rule{
			required("An image tag is required.").If(Is("image_source", ImagesPrebuilt)),
			imageVersionLength().If(Is("image_source", ImagesPrebuilt)),
			imageVersionLabel().If(Is("image_source", ImagesPrebuilt)),
		},
	}
	SubstrateRevision = &Input{
		Name:        "substrate_revision",
		Screen:      state.Images,
		Label:       "Commit the images were built from",
		Placeholder: snapshot.Commit,
		Description: "Revision of " + snapshot.RepoURL + " the manifests (and, for a source build, the images) come from. For pre-built images, the commit they were built from; for a source build, any branch, tag or commit, and empty for the default branch's HEAD.",
		Kind:        String,
		Default:     snapshot.Commit,
		MaxLen:      200,
		Rules: []Rule{
			required("The commit the images were built from is required.").If(Is("image_source", ImagesPrebuilt)),
		},
	}

	// ─── Choose your GCP project ─────────────────────────────────────────

	ProjectID = &Input{
		Name:        "project_id",
		Screen:      state.Project,
		Label:       "GCP project ID",
		Placeholder: "my-project",
		Description: "GCP project to install into.",
		Kind:        String,
		MaxLen:      96,
		Rules:       []Rule{required("A project ID is required.")},
	}
	ClusterLocation = &Input{
		Name:        "cluster_location",
		Screen:      state.Project,
		Label:       "Cluster location (zone)",
		Placeholder: "us-west1-c",
		Description: "GKE location of the cluster: a zone, or a region. The region the other resources go in is derived from it.",
		Kind:        String,
		Default:     defaults.Zone,
		MaxLen:      96,
	}
	BucketName = &Input{
		Name:        "bucket_name",
		Screen:      state.Project,
		Label:       "Snapshot bucket (leave empty for default)",
		Placeholder: "ate-snapshots-<project>-<cluster>-<zone>",
		Description: "GCS bucket for actor snapshots. Empty derives ate-snapshots-<project>-<cluster>-<location>.",
		Kind:        String,
		Default:     "",
		MaxLen:      96,
	}
	MachineType = &Input{
		Name:        "machine_type",
		Screen:      state.Project,
		Label:       "Node machine type",
		Placeholder: "c3-standard-4",
		Description: "Machine type of the Substrate node pool. (Advanced track.)",
		Kind:        String,
		Default:     defaults.MachineType,
		MaxLen:      96,
	}
	Network = &Input{
		Name:        "network",
		Screen:      state.Project,
		Label:       "VPC network (new clusters)",
		Placeholder: "default",
		Description: "VPC network of a cluster the install creates; an existing cluster keeps its own. (Advanced track.)",
		Kind:        String,
		Default:     defaults.Network,
		MaxLen:      96,
	}
	Subnetwork = &Input{
		Name:        "subnetwork",
		Screen:      state.Project,
		Label:       "VPC subnetwork (new clusters)",
		Placeholder: "default",
		Description: "VPC subnetwork of a cluster the install creates; an existing cluster keeps its own. (Advanced track.)",
		Kind:        String,
		Default:     defaults.Subnetwork,
		MaxLen:      96,
	}
	ClusterVersion = &Input{
		Name:        "cluster_version",
		Screen:      state.Project,
		Label:       "Cluster version (new clusters)",
		Placeholder: state.DefaultClusterVersion,
		Description: "GKE version of a cluster the install creates; ignored for an existing one. A bare minor is fine. Empty means " + state.DefaultClusterVersion + ". (Advanced track.)",
		Kind:        String,
		Default:     state.DefaultClusterVersion,
		MaxLen:      96,
		Rules:       []Rule{supportedRelease()},
	}
	ReleaseChannel = &Input{
		Name:        "release_channel",
		Screen:      state.Project,
		Label:       "Release channel (new clusters)",
		Placeholder: state.DefaultReleaseChannel,
		Description: "GKE release channel of a cluster the install creates (any case); ignored for an existing one. Empty means " + state.DefaultReleaseChannel + ". It must carry cluster_version in cluster_location, which only GKE can say. (Advanced track.)",
		Kind:        String,
		Default:     state.DefaultReleaseChannel,
		MaxLen:      96,
		Rules: []Rule{
			// Extended does not allow beta APIs, and setup-gcp asks for them
			// on every cluster it creates. "none" is not offered either: GKE
			// no longer lets new accounts create a cluster outside a channel.
			custom(CheckReleaseChannel, "Release channel {quoted} is not one of rapid, regular, or stable."),
			custom(CheckNotExtended, "The extended channel does not allow the beta APIs setup-gcp turns on for every cluster. Use rapid, regular, or stable."),
		},
	}
	KoDockerRepo = &Input{
		Name:        "ko_docker_repo",
		Screen:      state.Project,
		Label:       "Image registry (leave empty for default)",
		Placeholder: "<region>-docker.pkg.dev/<project>/ate-images",
		Description: "Registry a source build pushes to. Empty creates an Artifact Registry repository and pushes there. Ignored for pre-built images. (Advanced track.)",
		Kind:        String,
		Default:     "",
		MaxLen:      96,
	}
	ArtifactRegistryRepository = &Input{
		Name:        "artifact_registry_repository",
		Screen:      state.Project,
		Label:       "Artifact Registry repository (leave empty for default)",
		Placeholder: "ate-images",
		Description: "Name of the Artifact Registry repository a source build creates when ko_docker_repo is empty. Empty means ate-images. Ignored for pre-built images. (Advanced track.)",
		Kind:        String,
		Default:     "",
		MaxLen:      96,
		Rules: []Rule{
			matches(RepositoryNamePattern,
				"Repository names must be 1–63 lowercase letters, digits or hyphens, starting with a letter and ending with a letter or digit.",
			).If(Is("image_source", ImagesSource)),
		},
	}

	// ─── Connect your cluster ────────────────────────────────────────────

	CreateCluster = &Input{
		Name:        "create_cluster",
		Screen:      state.Cluster,
		Label:       "Create a new cluster, or choose an existing one",
		Description: "Whether to create cluster_name or install onto an existing cluster of that name.",
		Kind:        Bool,
		Default:     true,
	}
	ClusterName = &Input{
		Name:        "cluster_name",
		Screen:      state.Cluster,
		Label:       "New cluster name",
		Placeholder: defaults.ClusterName,
		Description: "Name of the GKE cluster.",
		Kind:        String,
		Default:     defaults.ClusterName,
		MaxLen:      40,
		Rules:       []Rule{required("A cluster name is required.")},
	}

	// ─── Install Filestore CSI driver ────────────────────────────────────

	InstallFilestoreCSI = &Input{
		Name:        "install_filestore_csi",
		Screen:      state.FilestoreCSI,
		Label:       "Install Filestore CSI driver (Substrate overlay)",
		Description: "Install the Substrate overlay of the GCP Filestore CSI driver. This disables GKE's managed Filestore CSI driver.",
		Kind:        Bool,
		Default:     true,
	}

	// ─── Configure autoscaling ───────────────────────────────────────────

	AutoscalingEnabled = &Input{
		Name:        "autoscaling_enabled",
		Screen:      state.Autoscaling,
		Label:       "Enable autoscaling on the node pool",
		Description: "Let GKE grow and shrink node_pool between the min and max node counts.",
		Kind:        Bool,
		Default:     true,
	}
	NodePool = &Input{
		Name:        "node_pool",
		Screen:      state.Autoscaling,
		Label:       "Node pool",
		Description: "Node pool to autoscale.",
		Kind:        String,
		Default:     defaults.NodePool,
	}
	AutoscalingMinNodes = &Input{
		Name:        "autoscaling_min_nodes",
		Screen:      state.Autoscaling,
		Label:       "min nodes",
		Description: "Fewest nodes autoscaling keeps in node_pool.",
		Kind:        Int,
		Default:     defaults.AutoscaleMin,
		MaxLen:      4,
		Rules: []Rule{
			between(0, 9999, "The minimum node count must be between 0 and 9999.").If(Is("autoscaling_enabled", true)),
		},
	}
	AutoscalingMaxNodes = &Input{
		Name:        "autoscaling_max_nodes",
		Screen:      state.Autoscaling,
		Label:       "max nodes",
		Description: "Most nodes autoscaling grows node_pool to.",
		Kind:        Int,
		Default:     defaults.AutoscaleMax,
		MaxLen:      4,
		Rules: []Rule{
			between(1, 9999, "The maximum node count must be between 1 and 9999.").If(Is("autoscaling_enabled", true)),
			atLeastMaxOf("autoscaling_min_nodes", 1, "The maximum node count must be at least the minimum.").If(Is("autoscaling_enabled", true)),
		},
	}

	// ─── Choose your sandbox runtime ─────────────────────────────────────

	SandboxClass = &Input{
		Name:        "sandbox_class",
		Screen:      state.Sandbox,
		Label:       "Sandbox runtime",
		Description: `Actor sandbox runtime: "gvisor" (installed with the control plane) or "microvm" (kata + cloud-hypervisor; needs a node pool with nested virtualization).`,
		Kind:        String,
		Default:     defaults.SandboxClass,
		Rules: []Rule{
			oneOf(`Must be "gvisor" or "microvm".`, state.SandboxGVisor, state.SandboxMicroVM),
		},
	}

	// ─── Deploy a demo workload ──────────────────────────────────────────

	DeployDemo = &Input{
		Name:        "deploy_demo",
		Screen:      state.Demo,
		Label:       "Deploy the counter demo",
		Description: "Deploy the counter demo (its micro-VM variant when sandbox_class is \"microvm\").",
		Kind:        Bool,
		Default:     true,
	}
)

// All is every input, in wizard order.
var All = []*Input{
	ImageSource, ImageRepo, ImageTag, SubstrateRevision,
	ProjectID, ClusterLocation, BucketName, MachineType, Network, Subnetwork,
	ClusterVersion, ReleaseChannel, KoDockerRepo, ArtifactRegistryRepository,
	CreateCluster, ClusterName,
	InstallFilestoreCSI,
	AutoscalingEnabled, NodePool, AutoscalingMinNodes, AutoscalingMaxNodes,
	SandboxClass,
	DeployDemo,
}

// The installer keeps these unexported, so they are restated here;
// TestImageTagRulesMatchCheckImageTag and TestRepositoryRuleMatchesState pin
// them to snapshot.CheckImageTag and state.ValidateRepositoryName.
const (
	// snapshot's labelValue and labelValueMaxLength: the Kubernetes
	// label-value grammar an image tag (the Substrate version) must fit.
	LabelValuePattern   = `^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`
	LabelValueMaxLength = 63
	// state's repositoryNameRE: Artifact Registry repository names.
	RepositoryNamePattern = `^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`
)

// The custom checks, by name.
const (
	CheckImageVersionLength = "image-version-length"
	CheckImageVersionLabel  = "image-version-label"
	CheckSupportedRelease   = "supported-release"
	CheckReleaseChannel     = "release-channel"
	CheckNotExtended        = "not-extended"
)

func init() {
	label := regexp.MustCompile(LabelValuePattern)
	CustomChecks[CheckImageVersionLength] = func(tag string) bool { return len(ImageVersion(tag)) <= LabelValueMaxLength }
	CustomChecks[CheckImageVersionLabel] = func(tag string) bool { return label.MatchString(ImageVersion(tag)) }
	// Empty is the default; a version gcp cannot read passes, as in the
	// wizard: GKE knows aliases like "latest", and rejects a version that
	// does not exist itself.
	CustomChecks[CheckSupportedRelease] = func(v string) bool { return v == "" || (gcp.Cluster{MasterVersion: v}).SupportedRelease() }
	// The first channel rule lets extended through, so the second can turn
	// it away with its own message.
	CustomChecks[CheckReleaseChannel] = func(v string) bool {
		return slices.Contains([]string{"", "rapid", "regular", "stable", "extended"}, strings.ToLower(v))
	}
	CustomChecks[CheckNotExtended] = func(v string) bool { return strings.ToLower(v) != "extended" }
}

// imageVersionLength and imageVersionLabel are snapshot.CheckImageTag, split
// in two so each failure has its own message.
func imageVersionLength() Rule {
	return custom(CheckImageVersionLength,
		fmt.Sprintf("{version} is too long; a version can be at most %d characters, because it becomes a node label.", LabelValueMaxLength))
}

func imageVersionLabel() Rule {
	return custom(CheckImageVersionLabel,
		"{version} cannot be a Substrate version: it becomes the ate.dev/substrate-version node label, so it must be letters, digits, '.', '-', or '_', beginning and ending with a letter or digit.")
}

// supportedRelease rejects a version below gcp.MinSupportedRelease.
func supportedRelease() Rule {
	return custom(CheckSupportedRelease,
		"Cluster version {value} is below "+gcp.MinSupportedRelease.String()+", the oldest release Substrate is supported on.")
}

func required(message string) Rule { return Rule{Kind: Required, Message: message} }

func matches(pattern, message string) Rule {
	regexp.MustCompile(pattern)
	return Rule{Kind: Matches, Pattern: pattern, Message: message}
}

func oneOf(message string, choices ...string) Rule {
	return Rule{Kind: OneOf, Choices: choices, Message: message}
}

func between(lo, hi int, message string) Rule {
	return Rule{Kind: IntBetween, Min: lo, Max: hi, Message: message}
}

func atLeastMaxOf(other string, floor int, message string) Rule {
	return Rule{Kind: IntAtLeastMaxOf, Other: other, Min: floor, Message: message}
}

func custom(check, message string) Rule {
	return Rule{Kind: Custom, Check: check, Message: message}
}
