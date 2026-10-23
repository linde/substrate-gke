# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# An example root module calling the substrate-gke module. Set project_id and
# sandbox_class (copy terraform.tfvars.example to terraform.tfvars), then:
#
#   terraform init && terraform plan
#
# It first enables the Google APIs the install needs, then waits for them to
# settle before anything else runs. Every other input has the wizard's
# default; the ones below are the quickstart's questions plus a few
# advanced-track ones to show the shape.

terraform {
  required_version = ">= 1.9"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 6.0"
    }
    time = {
      source  = "hashicorp/time"
      version = ">= 0.9"
    }
    helm = {
      source  = "hashicorp/helm"
      version = ">= 3.0"
    }
    tls = {
      source  = "hashicorp/tls"
      version = ">= 4.0"
    }
  }
}

variable "project_id" {
  description = "GCP project to install into."
  type        = string
}

variable "sandbox_class" {
  description = "Actor sandbox runtime: \"gvisor\" or \"microvm\""
  type        = string
}

provider "google" {
  project = var.project_id
}

# The module installs Substrate with Helm, into the cluster it creates (or
# names), so the helm provider is pointed at that cluster.
data "google_client_config" "default" {}

provider "helm" {
  kubernetes = {
    host                   = "https://${module.substrate.cluster_endpoint}"
    token                  = data.google_client_config.default.access_token
    cluster_ca_certificate = base64decode(module.substrate.cluster_ca_certificate)
  }
}

locals {
  # The APIs the install uses. A deliberately smaller set than setup-gcp's
  # bootstrap enables (tools/setup-gcp/cmd/api.go): cloudresourcemanager is
  # on in every new project already, and cloudtrace, telemetry and
  # networkconnectivity are not needed. TestExampleServicesAreMinimal keeps
  # this a subset of setup-gcp's list.
  required_services = [
    "artifactregistry.googleapis.com",
    "container.googleapis.com",
    "serviceusage.googleapis.com",
    "storage.googleapis.com",
    "logging.googleapis.com",
    "monitoring.googleapis.com",
  ]
}

resource "google_project_service" "required" {
  for_each = toset(local.required_services)

  project = var.project_id
  service = each.value

  # Leave the APIs on at destroy: other workloads in the project may use
  # them, and disabling one can fail or cascade.
  disable_on_destroy         = false
  disable_dependent_services = false
}

# A freshly enabled API can take a little while to start answering, and the
# first calls against it fail in the meantime. Everything else waits on this.
resource "time_sleep" "services_settle" {
  depends_on      = [google_project_service.required]
  create_duration = "30s"
}

module "substrate" {
  # From outside this repository, pin a release:
  #   source = "github.com/ai-on-gke/substrate-gke//terraform/modules/substrate-gke?ref=<release>"
  source = "../modules/substrate-gke"

  depends_on = [time_sleep.services_settle]

  # Choose your images: the published release (the default) ...
  image_source = "prebuilt"
  # ... or build from source:
  # image_source       = "source"
  # substrate_revision = "main"

  # Choose your GCP project.
  project_id       = var.project_id
  cluster_location = "us-west1-c"
  # Advanced track:
  # machine_type    = "c3-standard-4"
  # cluster_version = "1.37"
  # release_channel = "rapid"

  # Connect your cluster.
  create_cluster = true
  cluster_name   = "substrate-test"

  # Configure autoscaling.
  autoscaling_enabled   = true
  autoscaling_min_nodes = 1
  autoscaling_max_nodes = 5

  # Choose your sandbox runtime (required in this example; set it in
  # terraform.tfvars), and deploy a demo workload.
  sandbox_class = var.sandbox_class
  deploy_demo   = true
}

output "setup" {
  description = "The resolved configuration, as the wizard would hold it."
  value       = module.substrate.setup
}
