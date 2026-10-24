// Command checkimagepins fails when a third-party image in a compose file is
// not pinned by digest (name[:tag]@sha256:<64 hex>).
//
//	go run ./scripts/checkimagepins [compose-file]   # default deploy/docker-compose.yml
//
// The file is parsed as YAML, so quoted keys, flow-style mappings, anchors and
// multiple documents are all checked. Every key named image is checked, not only
// services.*.image, so an image under an x- extension also has to be pinned.
//
// The check is fail-closed: an image set through interpolation such as ${IMG},
// or any value that is not a non-empty string (null, number, list, mapping),
// counts as unpinned. A file with no image keys, a missing file and YAML that
// does not parse all fail too.
//
// Bongo's own image is built and published by this repository and pinned at
// release, so any tag passes. Nothing else is exempt.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	defaultPath = "deploy/docker-compose.yml"
	ownImage    = "ghcr.io/bongo-project/bongo"
)

var digestSuffix = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	path := defaultPath
	switch len(args) {
	case 0:
	case 1:
		path = args[0]
	default:
		fmt.Fprintln(stderr, "usage: checkimagepins [compose-file]")
		return 2
	}

	data, err := os.ReadFile(filepath.Clean(path)) // #nosec G304 G703 -- the compose file to check is named by the operator
	if err != nil {
		fmt.Fprintf(stderr, "check-image-pins: %s: %v\n", path, err)
		return 1
	}

	images, problems, err := check(data)
	if err != nil {
		fmt.Fprintf(stderr, "check-image-pins: %s: %v\n", path, err)
		return 1
	}
	for _, p := range problems {
		fmt.Fprintf(stdout, "unpinned image in %s: %s\n", path, p)
	}
	if images == 0 {
		fmt.Fprintf(stderr, "check-image-pins: no image keys in %s\n", path)
		return 1
	}
	if len(problems) > 0 {
		return 1
	}
	return 0
}

// check returns the number of image keys found across all documents in data
// and a description of each one that is not acceptably pinned.
func check(data []byte) (int, []string, error) {
	var images int
	var problems []string

	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, nil, fmt.Errorf("parse yaml: %w", err)
		}
		walk(&doc, &images, &problems)
	}
	return images, problems, nil
}

func walk(n *yaml.Node, images *int, problems *[]string) {
	if n == nil {
		return
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			walk(key, images, problems)
			k := key
			for k.Kind == yaml.AliasNode && k.Alias != nil {
				k = k.Alias
			}
			if k.Kind == yaml.ScalarNode && k.Value == "image" {
				*images++
				if msg := imageProblem(val); msg != "" {
					*problems = append(*problems, fmt.Sprintf("line %d: %s", val.Line, msg))
				}
			}
			walk(val, images, problems)
		}
		return
	}
	for _, c := range n.Content {
		walk(c, images, problems)
	}
}

// imageProblem returns a description of why val is not an acceptable image
// reference, or "" when it is.
func imageProblem(val *yaml.Node) string {
	for val.Kind == yaml.AliasNode && val.Alias != nil {
		val = val.Alias
	}
	if val.Kind != yaml.ScalarNode || val.Tag != "!!str" || val.Value == "" {
		return fmt.Sprintf("not a non-empty string (%q)", val.Value)
	}
	v := val.Value
	if v == ownImage || strings.HasPrefix(v, ownImage+":") || strings.HasPrefix(v, ownImage+"@") {
		return ""
	}
	if !digestSuffix.MatchString(v) {
		return v
	}
	return ""
}
