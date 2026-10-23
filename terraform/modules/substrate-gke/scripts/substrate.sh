#!/usr/bin/env bash
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

# Runs one step of the Substrate install, the way the wizard runs it
# (installer/internal/snapshot), against the cluster install.tf names. Every
# input arrives in the environment; see install.tf.
#
#   substrate.sh label-nodes|control-plane|microvm|filestore|demo|autoscaling

set -euo pipefail

# Written for bash 3.2 too (macOS): no ${var,,}, and empty arrays expanded
# with ${a[@]+"${a[@]}"} so set -u does not trip on them.

step=${1:?usage: substrate.sh label-nodes|control-plane|microvm|filestore|demo|autoscaling}

log() { echo "[substrate:${step}] $*"; }

need() {
  local missing=()
  for tool in "$@"; do
    command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
  done
  if ((${#missing[@]})); then
    echo "substrate.sh ${step}: missing on PATH: ${missing[*]}" >&2
    exit 1
  fi
}

# Credentials go to a throwaway KUBECONFIG: the install must target the
# cluster Terraform names, never whatever context happens to be current.
credentials() {
  need gcloud kubectl
  KUBECONFIG=$(mktemp)
  export KUBECONFIG
  trap 'rm -f "${KUBECONFIG}"' EXIT
  gcloud container clusters get-credentials "${CLUSTER_NAME}" \
    --location "${CLUSTER_LOCATION}" --project "${PROJECT_ID}" >/dev/null
}

# checkout leaves the shell in a Substrate tree at SUBSTRATE_REVISION. A
# full commit SHA is cached, since it can never move; a branch or tag is
# fetched afresh every time.
checkout() {
  need git go
  local rev=${SUBSTRATE_REVISION:-HEAD}
  local key
  key=$(printf '%s' "${rev}" | tr -c 'A-Za-z0-9._-' '_')
  local dir="${SUBSTRATE_WORK_DIR}/substrate-${key}"
  if [[ ! ${rev} =~ ^[0-9a-f]{40}$ ]] || [[ ! -e "${dir}/.substrate-complete" ]]; then
    log "fetching ${SUBSTRATE_REPO}@${rev}"
    rm -rf "${dir}"
    mkdir -p "${dir}"
    git -C "${dir}" init -q
    git -C "${dir}" fetch -q --depth 1 "${SUBSTRATE_REPO}" "${rev}"
    git -C "${dir}" checkout -q FETCH_HEAD
    echo .substrate-complete >>"${dir}/.git/info/exclude"
    touch "${dir}/.substrate-complete"
  fi
  cd "${dir}"
}

# images sets image_args and VERSION as the wizard does: pre-built images
# are named by flag and the version is their tag; a source build pushes to
# KO_DOCKER_REPO and is versioned by its commit.
images() {
  image_args=()
  if [[ ${IMAGE_SOURCE} == prebuilt ]]; then
    image_args=(--image-repo "${IMAGE_REPO}" --image-tag "${IMAGE_TAG}")
    export VERSION="${IMAGE_TAG%%@*}"
    unset KO_DOCKER_REPO KO_DEFAULTPLATFORMS
  else
    need docker
    export VERSION="substrate-$(git rev-parse HEAD | cut -c1-12)"
  fi
}

case "${step}" in
label-nodes)
  # An existing cluster's nodes: the atelet DaemonSet and the demo
  # WorkerPools select nodes by this label. Like ate-setup, label only the
  # nodes that have none (steps/version.go). A pool the module creates
  # carries the label itself, autoscaled nodes included.
  credentials
  log "labelling unlabelled nodes ate.dev/substrate-version=${SUBSTRATE_VERSION}"
  kubectl label nodes -l '!ate.dev/substrate-version' "ate.dev/substrate-version=${SUBSTRATE_VERSION}"
  ;;

control-plane)
  # A build from source only: pre-built images are installed by the Helm
  # chart.
  credentials
  checkout
  images
  log "go run ./cmd/ate-setup deploy ate-system ${image_args[*]-}"
  go run ./cmd/ate-setup deploy ate-system ${image_args[@]+"${image_args[@]}"}
  ;;

microvm)
  # Stages cloud-hypervisor, virtiofsd, the guest kernel and rootfs under
  # gs://BUCKET/kata-assets/ and applies the cluster-wide microvm
  # SandboxConfig.
  need sha256sum zstd unzip jq make
  credentials
  checkout
  images
  log "hack/install-microvm-deps.sh --install"
  hack/install-microvm-deps.sh --install
  ;;

demo)
  credentials
  checkout
  images
  demo=counter
  if [[ ${SANDBOX_CLASS} == microvm ]]; then
    demo=counter-microvm
    if [[ ! -d cmd/ate-setup/internal/demos/countermicrovm ]]; then
      echo "substrate.sh demo: substrate@${SUBSTRATE_REVISION} has no counter-microvm demo in ate-setup (added in v0.2.0)" >&2
      exit 1
    fi
  fi
  log "go run ./cmd/ate-setup deploy demo ${demo} ${image_args[*]-}"
  go run ./cmd/ate-setup deploy demo "${demo}" ${image_args[@]+"${image_args[@]}"}
  ;;

filestore)
  # The Substrate overlay of the Filestore CSI driver. GKE's own driver is
  # off on a cluster this module creates; an existing one may have it on.
  need git gcloud kubectl
  credentials
  addon=$(gcloud container clusters describe "${CLUSTER_NAME}" --project "${PROJECT_ID}" \
    --location "${CLUSTER_LOCATION}" --format='value(addonsConfig.gcpFilestoreCsiDriverConfig.enabled)' 2>/dev/null || true)
  if [[ $(printf '%s' "${addon}" | tr '[:upper:]' '[:lower:]') == true ]]; then
    log "disabling GKE's managed Filestore CSI driver"
    gcloud container clusters update "${CLUSTER_NAME}" --project "${PROJECT_ID}" \
      --location "${CLUSTER_LOCATION}" --update-addons=GcpFilestoreCsiDriver=DISABLED
  fi
  tmp=$(mktemp -d)
  trap 'rm -rf "${tmp}" "${KUBECONFIG}"' EXIT
  git clone -q --depth 1 https://github.com/kubernetes-sigs/gcp-filestore-csi-driver.git "${tmp}/driver"
  cd "${tmp}/driver/deploy/kubernetes/overlays/substrate"
  log "./deploy.sh --project-id ${PROJECT_ID}"
  ./deploy.sh --project-id "${PROJECT_ID}"
  ;;

autoscaling)
  # Only for an existing cluster: a pool this module creates gets its bounds
  # from Terraform.
  need gcloud
  log "gcloud container node-pools update ${NODE_POOL} --enable-autoscaling --min-nodes=${MIN_NODES} --max-nodes=${MAX_NODES}"
  gcloud container node-pools update "${NODE_POOL}" --project "${PROJECT_ID}" \
    --cluster "${CLUSTER_NAME}" --location "${CLUSTER_LOCATION}" \
    --enable-autoscaling --min-nodes="${MIN_NODES}" --max-nodes="${MAX_NODES}"
  ;;

*)
  echo "substrate.sh: unknown step ${step}" >&2
  exit 2
  ;;
esac
