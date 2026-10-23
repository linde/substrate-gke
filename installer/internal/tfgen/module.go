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
	"bytes"
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"
	"text/template"

	"github.com/ai-on-gke/substrate-gke/installer/internal/chartgen"
	"github.com/ai-on-gke/substrate-gke/installer/internal/inputs"
)

// GCPFacts are what the module's GCP resources are shaped by: the settings
// setup-gcp's bootstrap uses for the wizard (tools/setup-gcp/cmd). setup-gcp
// is a separate Go module, so they are restated here, and
// TestGCPFactsMatchSetupGCP fails when the two drift.
type GCPFacts struct {
	BetaAPIs       []string
	NodeSysctls    map[string]string
	InitialNodes   int
	NodeRoles      []string
	AteletRoles    []string
	BucketSubjects []string
	BucketRoles    []string
}

// GCP is setup-gcp's cluster, IAM and bucket shape.
var GCP = GCPFacts{
	// cluster.go requiredBetaAPIs.
	BetaAPIs: []string{
		"certificates.k8s.io/v1beta1/podcertificaterequests",
		"certificates.k8s.io/v1beta1/clustertrustbundles",
	},
	// cluster.go requiredNodeSysctls.
	NodeSysctls: map[string]string{
		"net.ipv4.neigh.default.gc_thresh1": "4096",
		"net.ipv4.neigh.default.gc_thresh2": "16384",
		"net.ipv4.neigh.default.gc_thresh3": "32768",
		"fs.inotify.max_user_instances":     "65536",
		"fs.inotify.max_user_watches":       "1048576",
	},
	// cluster.go buildCreateClusterRequest: InitialNodeCount.
	InitialNodes: 2,
	// iam.go grantGkeNodePermissions and grantAteletPermissions.
	NodeRoles:   []string{"roles/storage.objectViewer", "roles/artifactregistry.reader"},
	AteletRoles: []string{"roles/storage.objectAdmin", "roles/artifactregistry.reader"},
	// bucket.go snapshotBucketSubjects and snapshotBucketRoles.
	BucketSubjects: []string{"atelet", "ate-api-server"},
	BucketRoles:    []string{"roles/storage.objectAdmin", "roles/storage.bucketViewer"},
}

//go:embed templates/*.tf.tmpl
var templates embed.FS

// moduleTemplateData is what the module templates see.
type moduleTemplateData struct {
	GCPFacts
	Inputs  []*inputs.Input
	Options []*Option
	// CAPools maps each CA key pool to its key algorithm.
	CAPools map[string]string
	// ImageRefs maps each chart image to its HCL reference expression.
	ImageRefs map[string]string
}

var templateFuncs = template.FuncMap{
	// list renders a []string as an HCL list.
	"list": func(items []string) string {
		quoted := make([]string, len(items))
		for i, s := range items {
			quoted[i] = hclString(s)
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	},
	// map renders a map[string]string as an HCL object of strings, keys
	// sorted and aligned as terraform fmt does, one level of nesting in
	// (where the templates use it).
	"map": func(m map[string]string) string {
		quoted := map[string]string{}
		for k, v := range m {
			quoted[k] = hclString(v)
		}
		return object(1, quoted)
	},
	// object renders a map of HCL expressions as an object, nested depth
	// levels in.
	"object": object,
}

// renderModuleTemplates renders every templates/NAME.tf.tmpl to NAME.tf.
func renderModuleTemplates() map[string][]byte {
	data := moduleTemplateData{GCPFacts: GCP, Inputs: inputs.All, Options: Options,
		CAPools: map[string]string{}, ImageRefs: map[string]string{}}
	for _, p := range chartgen.KeyPools {
		if !p.JWT {
			data.CAPools[p.Key] = p.Algorithm
		}
	}
	for _, name := range chartgen.Images {
		data.ImageRefs[name] = `"${var.image_repo}/` + name + `:${local.substrate_version}"`
	}
	names, err := templates.ReadDir("templates")
	if err != nil {
		panic(err)
	}
	out := map[string][]byte{}
	for _, entry := range names {
		src, err := templates.ReadFile(path.Join("templates", entry.Name()))
		if err != nil {
			panic(err)
		}
		tmpl := template.Must(template.New(entry.Name()).Funcs(templateFuncs).Parse(string(src)))
		var b bytes.Buffer
		writeHeader(&b)
		b.WriteString("\n")
		if err := tmpl.Execute(&b, data); err != nil {
			panic(fmt.Sprintf("tfgen: %s: %v", entry.Name(), err))
		}
		out[strings.TrimSuffix(entry.Name(), ".tmpl")] = b.Bytes()
	}
	return out
}

// object renders m (keys to HCL expressions) as an HCL object nested depth
// levels in, keys quoted, sorted, and aligned as terraform fmt does.
func object(depth int, m map[string]string) string {
	keys := make([]string, 0, len(m))
	width := 0
	for k := range m {
		keys = append(keys, k)
		width = max(width, len(hclString(k)))
	}
	sort.Strings(keys)
	indent := strings.Repeat("  ", depth)
	var b strings.Builder
	b.WriteString("{\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s  %-*s = %s\n", indent, width, hclString(k), m[k])
	}
	b.WriteString(indent + "}")
	return b.String()
}
