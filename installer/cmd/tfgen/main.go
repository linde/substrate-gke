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

// Command tfgen writes the generated files of the Terraform module and its
// example from the wizard's input declarations (internal/inputs).
//
//	go run ./cmd/tfgen [-check] [repo-root]
//
// repo-root defaults to the parent of the current directory, which is right
// when run from installer/. The Helm charts are rendered from the Substrate
// commit the installer pins: -substrate names a checkout of it, and without
// one tfgen fetches it into the user cache (which needs git and the network
// the first time). With -check it writes nothing and exits 1 if any
// generated file is missing or stale, which is what `make verify` runs.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/ai-on-gke/substrate-gke/installer/internal/tfgen"
)

func main() {
	check := flag.Bool("check", false, "report stale generated files instead of writing them")
	substrate := flag.String("substrate", "", "a checkout of the pinned Substrate commit to render the Helm charts from; empty fetches it into the user cache")
	flag.Parse()
	root := ".."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	if *substrate == "" {
		tree, err := tfgen.SubstrateTree(context.Background())
		if err != nil {
			fail(fmt.Errorf("fetching the pinned Substrate tree for the charts: %w", err))
		}
		*substrate = tree
	}
	files, err := tfgen.GeneratedFiles(*substrate)
	if err != nil {
		fail(err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	stale := 0
	for _, name := range names {
		path := filepath.Join(root, name)
		want := files[name]
		if *check {
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, want) {
				fmt.Fprintf(os.Stderr, "tfgen: %s is out of date; run `make generate`\n", path)
				stale++
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fail(err)
		}
		if err := os.WriteFile(path, want, 0o644); err != nil {
			fail(err)
		}
	}
	// Files left in the directories tfgen owns that it no longer generates.
	for _, dir := range tfgen.OwnedDirs {
		filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			if _, ok := files[filepath.ToSlash(rel)]; ok {
				return nil
			}
			if *check {
				fmt.Fprintf(os.Stderr, "tfgen: %s is no longer generated; run `make generate`\n", path)
				stale++
				return nil
			}
			return os.Remove(path)
		})
	}
	if stale > 0 {
		os.Exit(1)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "tfgen:", err)
	os.Exit(1)
}
