// Command check-k8s-references verifies that required Kubernetes Secret and
// ConfigMap references are either defined by the manifests or documented as
// external resources in docs/deployment.md.
package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type resourceID struct {
	kind      string
	namespace string
	name      string
}

type reference struct {
	resourceID
	key      string
	optional bool
	source   string
}

type externalResource struct {
	keys map[string]bool
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func mapValue(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func metadata(document map[string]any) (name, namespace string) {
	meta := mapValue(document["metadata"])
	name = stringValue(meta["name"])
	namespace = stringValue(meta["namespace"])
	if namespace == "" {
		namespace = "default"
	}
	return name, namespace
}

func collectKeys(document map[string]any) map[string]bool {
	keys := make(map[string]bool)
	for _, field := range []string{"data", "binaryData", "stringData"} {
		for key := range mapValue(document[field]) {
			keys[key] = true
		}
	}
	return keys
}

func addReference(refs *[]reference, seen map[string]bool, ref reference) {
	identity := strings.Join([]string{ref.kind, ref.namespace, ref.name, ref.key, fmt.Sprint(ref.optional), ref.source}, "\x00")
	if ref.name == "" || seen[identity] {
		return
	}
	seen[identity] = true
	*refs = append(*refs, ref)
}

func walk(value any, namespace, source string, refs *[]reference, seen map[string]bool) {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			switch key {
			case "secretKeyRef", "configMapKeyRef":
				refMap := mapValue(child)
				kind := "Secret"
				if key == "configMapKeyRef" {
					kind = "ConfigMap"
				}
				addReference(refs, seen, reference{
					resourceID: resourceID{kind: kind, namespace: namespace, name: stringValue(refMap["name"])},
					key:        stringValue(refMap["key"]),
					optional:   boolValue(refMap["optional"]),
					source:     source,
				})
			case "secretRef", "configMapRef":
				refMap := mapValue(child)
				kind := "Secret"
				if key == "configMapRef" {
					kind = "ConfigMap"
				}
				addReference(refs, seen, reference{
					resourceID: resourceID{kind: kind, namespace: namespace, name: stringValue(refMap["name"])},
					optional:   boolValue(refMap["optional"]),
					source:     source,
				})
			case "secretName":
				addReference(refs, seen, reference{
					resourceID: resourceID{kind: "Secret", namespace: namespace, name: stringValue(child)},
					source:     source,
				})
			case "secret":
				refMap := mapValue(child)
				addReference(refs, seen, reference{
					resourceID: resourceID{kind: "Secret", namespace: namespace, name: stringValue(refMap["secretName"])},
					optional:   boolValue(refMap["optional"]),
					source:     source,
				})
			case "configMap":
				refMap := mapValue(child)
				addReference(refs, seen, reference{
					resourceID: resourceID{kind: "ConfigMap", namespace: namespace, name: stringValue(refMap["name"])},
					optional:   boolValue(refMap["optional"]),
					source:     source,
				})
			case "annotations":
				for annotation, rawName := range mapValue(child) {
					if strings.HasSuffix(annotation, "/auth-secret") {
						addReference(refs, seen, reference{
							resourceID: resourceID{kind: "Secret", namespace: namespace, name: stringValue(rawName)},
							source:     source,
						})
					}
				}
			}
			walk(child, namespace, source, refs, seen)
		}
	case []any:
		for _, child := range node {
			walk(child, namespace, source, refs, seen)
		}
	}
}

func loadManifests(directory string) (map[resourceID]map[string]bool, []reference, error) {
	// OpenRoot scopes every subsequent read under `directory`, so even a
	// caller-supplied path cannot escape to an arbitrary filesystem location
	// (gosec G304/G703 path traversal).
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()

	names, err := fs.Glob(root.FS(), "*.yaml")
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(names)

	definitions := make(map[resourceID]map[string]bool)
	var refs []reference
	seen := make(map[string]bool)

	for _, name := range names {
		path := filepath.Join(directory, name)
		content, err := fs.ReadFile(root.FS(), name)
		if err != nil {
			return nil, nil, err
		}
		decoder := yaml.NewDecoder(bytes.NewReader(content))
		for documentNumber := 1; ; documentNumber++ {
			var document map[string]any
			if err := decoder.Decode(&document); err != nil {
				if err == io.EOF {
					break
				}
				return nil, nil, fmt.Errorf("%s document %d: %w", path, documentNumber, err)
			}
			if len(document) == 0 {
				continue
			}
			name, namespace := metadata(document)
			kind := stringValue(document["kind"])
			source := fmt.Sprintf("%s document %d", path, documentNumber)
			if (kind == "Secret" || kind == "ConfigMap") && name != "" {
				definitions[resourceID{kind: kind, namespace: namespace, name: name}] = collectKeys(document)
			}
			walk(document, namespace, source, &refs, seen)
		}
	}
	return definitions, refs, nil
}

func documentedSecrets(path string) (map[resourceID]externalResource, error) {
	// Scope the read under the file's directory so a caller-controlled path
	// cannot traverse outside it (gosec G304/G703 path traversal).
	dir, base := filepath.Split(path)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	content, err := fs.ReadFile(root.FS(), base)
	if err != nil {
		return nil, err
	}
	normalized := regexp.MustCompile(`\\\r?\n\s*`).ReplaceAll(content, []byte(" "))
	commandPattern := regexp.MustCompile(`(?m)kubectl create secret (generic|tls)\s+([a-z0-9.-]+)([^\n]*)`)
	literalPattern := regexp.MustCompile(`--from-literal=([^=\s]+)=`)
	filePattern := regexp.MustCompile(`--from-file=([^=\s]+)(?:=[^\s]+)?`)

	resources := make(map[resourceID]externalResource)
	for _, match := range commandPattern.FindAllSubmatch(normalized, -1) {
		name := string(match[2])
		arguments := string(match[3])
		keys := make(map[string]bool)
		for _, keyMatch := range literalPattern.FindAllStringSubmatch(arguments, -1) {
			keys[keyMatch[1]] = true
		}
		for _, keyMatch := range filePattern.FindAllStringSubmatch(arguments, -1) {
			keys[keyMatch[1]] = true
		}
		if string(match[1]) == "tls" {
			keys["tls.crt"] = true
			keys["tls.key"] = true
		}
		resources[resourceID{kind: "Secret", namespace: "one-api", name: name}] = externalResource{keys: keys}
	}
	return resources, nil
}

func main() {
	manifestDirectory := "deployments/k8s"
	deploymentDoc := "docs/deployment.md"
	if len(os.Args) == 3 {
		manifestDirectory = os.Args[1]
		deploymentDoc = os.Args[2]
	} else if len(os.Args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: go run ./scripts/check-k8s-references.go [manifest-directory deployment-doc]\n")
		os.Exit(2)
	}

	definitions, refs, err := loadManifests(manifestDirectory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load Kubernetes manifests: %v\n", err)
		os.Exit(1)
	}
	externalSecrets, err := documentedSecrets(deploymentDoc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load deployment documentation: %v\n", err)
		os.Exit(1)
	}

	var failures []string
	for _, ref := range refs {
		if ref.optional {
			failures = append(failures, fmt.Sprintf("%s: required %s %s must not set optional: true", ref.source, ref.kind, ref.name))
		}

		keys, defined := definitions[ref.resourceID]
		if !defined && ref.kind == "Secret" {
			if external, documented := externalSecrets[ref.resourceID]; documented {
				keys = external.keys
				defined = true
			}
		}
		if !defined {
			failures = append(failures, fmt.Sprintf("%s: %s %s/%s is neither defined by the manifests nor documented", ref.source, ref.kind, ref.namespace, ref.name))
			continue
		}
		if ref.key != "" && !keys[ref.key] {
			failures = append(failures, fmt.Sprintf("%s: %s %s/%s does not define documented key %q", ref.source, ref.kind, ref.namespace, ref.name, ref.key))
		}
	}

	if len(failures) > 0 {
		sort.Strings(failures)
		fmt.Fprintln(os.Stderr, "Kubernetes resource reference errors:")
		for _, failure := range failures {
			fmt.Fprintf(os.Stderr, "  %s\n", failure)
		}
		os.Exit(1)
	}

	fmt.Printf("Kubernetes references: OK (%d references, %d in-manifest resources, %d documented external Secrets)\n", len(refs), len(definitions), len(externalSecrets))
}
