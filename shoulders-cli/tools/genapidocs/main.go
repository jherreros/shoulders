// Command genapidocs generates Docusaurus API reference markdown from the
// Crossplane XRDs in 2-addons/manifests/crossplane/definitions/.
//
// Usage:
//
//	go run ./tools/genapidocs --defs <xrd-dir> --out <markdown-dir>
//
// Only stdlib plus sigs.k8s.io/yaml (already a CLI dependency) is used, so the
// docs build needs nothing beyond the Go toolchain.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// mdxAngle matches '<' that MDX would parse as JSX tag open (RE2: no lookahead).
var mdxAngle = regexp.MustCompile(`<([A-Za-z(/!])`)

type fieldRow struct {
	path        string
	typ         string
	description string
}

func main() {
	defsDir := flag.String("defs", "", "Directory containing *-xrd.yaml files")
	outDir := flag.String("out", "", "Output directory for generated markdown")
	flag.Parse()
	if *defsDir == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "usage: genapidocs --defs <xrd-dir> --out <markdown-dir>")
		os.Exit(2)
	}

	files, err := filepath.Glob(filepath.Join(*defsDir, "*-xrd.yaml"))
	if err != nil {
		fatal(err)
	}
	sort.Strings(files)
	if len(files) == 0 {
		fatal(fmt.Errorf("no *-xrd.yaml files in %s", *defsDir))
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatal(err)
	}
	// Clear previously generated pages (keep _category_.json, rewritten below).
	if old, _ := filepath.Glob(filepath.Join(*outDir, "*.md")); len(old) > 0 {
		for _, f := range old {
			if err := os.Remove(f); err != nil {
				fatal(err)
			}
		}
	}

	type indexEntry struct {
		title, stem, file, scope string
	}
	var index []indexEntry

	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			fatal(err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			fatal(fmt.Errorf("%s: %w", f, err))
		}
		spec, _ := doc["spec"].(map[string]any)
		group, _ := spec["group"].(string)
		names, _ := spec["names"].(map[string]any)
		kind, _ := names["kind"].(string)
		scope, _ := spec["scope"].(string)
		version := ""
		var schema map[string]any
		if versions, _ := spec["versions"].([]any); len(versions) > 0 {
			if v0, _ := versions[0].(map[string]any); v0 != nil {
				version, _ = v0["name"].(string)
				if s, _ := v0["schema"].(map[string]any); s != nil {
					schema, _ = s["openAPIV3Schema"].(map[string]any)
				}
			}
		}

		title := humanize(kind)
		if title == "" {
			title = strings.TrimSuffix(filepath.Base(f), ".yaml")
		}
		stem := strings.TrimSuffix(filepath.Base(f), ".yaml")
		index = append(index, indexEntry{title, stem, filepath.Base(f), scope})

		var b strings.Builder
		fmt.Fprintf(&b, "---\n# Auto-generated from 2-addons/manifests/crossplane/definitions/%s — do not edit by hand.\n# Regenerate with: npm run gen:api\n---\n\n", filepath.Base(f))
		fmt.Fprintf(&b, "# %s\n\n", title)
		fmt.Fprintf(&b, "Source: `%s` — group `%s`, scope `%s`, version `%s`.\n\n", filepath.Base(f), group, scope, version)

		var rows []fieldRow
		if schema != nil {
			if props, _ := schema["properties"].(map[string]any); props != nil {
				if specSchema, _ := props["spec"].(map[string]any); specSchema != nil {
					walkSchema(specSchema, "", requiredSet(specSchema), &rows)
				}
			}
		}
		if len(rows) == 0 {
			b.WriteString("_No `spec` fields found in the XRD schema._\n")
		} else {
			b.WriteString("## `spec` fields\n\n")
			b.WriteString("| Field | Type | Description |\n")
			b.WriteString("|---|---|---|\n")
			for _, r := range rows {
				desc := strings.ReplaceAll(r.description, "|", "\\|")
				desc = strings.ReplaceAll(desc, "\n", " ")
				// Bare <placeholder> text is parsed as JSX by Docusaurus MDX;
				// escape it (renders identically outside code spans).
				desc = mdxAngle.ReplaceAllString(desc, "&lt;$1")
				fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", r.path, r.typ, desc)
			}
		}

		if err := os.WriteFile(filepath.Join(*outDir, stem+".md"), []byte(b.String()), 0o644); err != nil {
			fatal(err)
		}
	}

	var idx strings.Builder
	idx.WriteString("---\n# Auto-generated from the XRD definitions — do not edit by hand.\n# Regenerate with: npm run gen:api\n---\n\n")
	idx.WriteString("# Generated API reference\n\n")
	idx.WriteString("| Resource | Source | Scope |\n")
	idx.WriteString("|---|---|---|\n")
	for _, e := range index {
		fmt.Fprintf(&idx, "| [%s](./%s) | `%s` | %s |\n", e.title, e.stem, e.file, e.scope)
	}
	if err := os.WriteFile(filepath.Join(*outDir, "index.md"), []byte(idx.String()), 0o644); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*outDir, "_category_.json"), []byte(`{"label": "Generated reference", "position": 99}`+"\n"), 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("API docs generated in %s (%d XRDs)\n", *outDir, len(files))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "genapidocs:", err)
	os.Exit(1)
}

func requiredSet(schema map[string]any) map[string]bool {
	set := map[string]bool{}
	if req, _ := schema["required"].([]any); req != nil {
		for _, r := range req {
			if s, ok := r.(string); ok {
				set[s] = true
			}
		}
	}
	return set
}

// walkSchema flattens an OpenAPI v3 schema subtree into field rows.
func walkSchema(node map[string]any, prefix string, required map[string]bool, rows *[]fieldRow) {
	props, _ := node["properties"].(map[string]any)
	if props == nil {
		return
	}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sub, _ := props[name].(map[string]any)
		if sub == nil {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		typ := displayType(sub)
		desc, _ := sub["description"].(string)
		desc = strings.TrimSpace(desc)
		if required[name] {
			if desc != "" {
				desc += " "
			}
			desc += "(required)"
		}
		if enum, ok := sub["enum"].([]any); ok && len(enum) > 0 {
			vals := make([]string, 0, len(enum))
			for _, v := range enum {
				vals = append(vals, fmt.Sprintf("%v", v))
			}
			if desc != "" {
				desc += " "
			}
			desc += "(allowed: " + strings.Join(vals, ", ") + ")"
		}
		if def, ok := sub["default"]; ok && def != nil {
			if desc != "" {
				desc += " "
			}
			desc += fmt.Sprintf("(default: `%v`)", def)
		}
		*rows = append(*rows, fieldRow{path, typ, desc})

		if sub["type"] == "object" {
			if _, ok := sub["properties"]; ok {
				walkSchema(sub, path, requiredSet(sub), rows)
			}
		}
		if sub["type"] == "array" {
			if items, _ := sub["items"].(map[string]any); items != nil {
				if _, ok := items["properties"]; ok {
					walkSchema(items, path+"[]", requiredSet(items), rows)
				}
			}
		}
	}
}

func displayType(sub map[string]any) string {
	t, _ := sub["type"].(string)
	if t == "" {
		return "-"
	}
	if t == "array" {
		if items, _ := sub["items"].(map[string]any); items != nil {
			if it, _ := items["type"].(string); it != "" && it != "object" {
				return "array<" + it + ">"
			}
		}
	}
	return t
}

// humanize splits CamelCase kinds: "StateStore" -> "State Store".
func humanize(kind string) string {
	var b strings.Builder
	for i, r := range kind {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := rune(kind[i-1])
			if (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9') {
				b.WriteByte(' ')
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}
