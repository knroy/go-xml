package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Engine is one entry of bench/engines.json.
type Engine struct {
	Version        string              `json:"version"`
	VersionArgv    []string            `json:"version_argv"`
	Loop           bool                `json:"loop"`
	Files          []EngineFile        `json:"files"`
	Classpath      []string            `json:"classpath"`
	JavaOpts       []string            `json:"java_opts"`
	ParamFormat    []string            `json:"param_format"`
	InvalidPattern string              `json:"invalid_pattern"`
	Tasks          map[string][]string `json:"tasks"`
}

// EngineFile is a pinned download; fetch-engines.sh carries the same pins.
type EngineFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	URL    string `json:"url"`
}

type enginesFile struct {
	Engines map[string]*Engine `json:"engines"`
}

func loadEngines(path string) (map[string]*Engine, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f enginesFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f.Engines, nil
}

// Workload is one bench/workloads/<id>.json.
type Workload struct {
	ID          string `json:"id"`
	Area        string `json:"area"`
	Description string `json:"description"`
	Inputs      any    `json:"inputs"`
	Items       []Item `json:"items"`
	Normalise   string `json:"normalise"`
	// XSDVersion is the version go-xml validates with; Xerces always runs
	// its 1.1 processor and xmllint only knows 1.0. Default "1.1".
	XSDVersion string              `json:"xsd_version"`
	Engines    []string            `json:"engines"`
	Modes      []string            `json:"modes"`
	Args       map[string][]string `json:"args"`
}

// Item is one unit of work. Glob, when set, expands into one item per match
// with that file as Source. Area overrides the workload's.
type Item struct {
	Name       string            `json:"name"`
	Area       string            `json:"area"`
	Stylesheet string            `json:"stylesheet"`
	Query      string            `json:"query"`
	Schema     string            `json:"schema"`
	Source     string            `json:"source"`
	Glob       string            `json:"glob"`
	Params     map[string]string `json:"params"`
}

var areas = map[string]bool{"xslt": true, "xquery": true, "xsd": true, "rng": true, "parse": true, "c14n": true}

var normalisations = map[string]bool{"": true, "none": true, "xml-c14n": true, "whitespace": true, "text": true}

// parseWorkload decodes and checks a workload, expanding globs relative to
// root. Unknown fields are an error: a typo in a hand-written workload should
// not silently drop a setting.
func parseWorkload(data []byte, root string) (*Workload, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var w Workload
	if err := dec.Decode(&w); err != nil {
		return nil, err
	}
	if w.ID == "" {
		return nil, fmt.Errorf("workload has no id")
	}
	if !normalisations[w.Normalise] {
		return nil, fmt.Errorf("%s: unknown normalise %q", w.ID, w.Normalise)
	}
	if !slices.Contains(w.Engines, "go-xml") {
		return nil, fmt.Errorf("%s: engines must include go-xml, the baseline every output is checked against", w.ID)
	}
	if w.XSDVersion == "" {
		w.XSDVersion = "1.1"
	}
	if len(w.Modes) == 0 {
		w.Modes = []string{"cold", "warm"}
	}
	for _, m := range w.Modes {
		if m != "cold" && m != "warm" {
			return nil, fmt.Errorf("%s: unknown mode %q", w.ID, m)
		}
	}
	var items []Item
	for _, it := range w.Items {
		if it.Area == "" {
			it.Area = w.Area
		}
		if !areas[it.Area] {
			return nil, fmt.Errorf("%s/%s: unknown area %q", w.ID, it.Name, it.Area)
		}
		if it.Glob == "" {
			if it.Name == "" {
				it.Name = filepath.Base(it.Source)
			}
			items = append(items, it)
			continue
		}
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(it.Glob)))
		if err != nil {
			return nil, fmt.Errorf("%s: glob %q: %w", w.ID, it.Glob, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("%s: glob %q matches nothing", w.ID, it.Glob)
		}
		sort.Strings(matches)
		for _, m := range matches {
			x := it
			x.Glob = ""
			rel, _ := filepath.Rel(root, m)
			x.Source = filepath.ToSlash(rel)
			x.Name = filepath.Base(m)
			if it.Name != "" {
				x.Name = it.Name + "/" + x.Name
			}
			items = append(items, x)
		}
	}
	w.Items = items
	return &w, nil
}

// loadWorkloads reads every bench/workloads/*.json, or only the ids asked for.
func loadWorkloads(dir, root string, ids []string) ([]*Workload, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	all := len(ids) == 0 || (len(ids) == 1 && ids[0] == "all")
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []*Workload
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		w, err := parseWorkload(data, root)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if all || want[w.ID] {
			delete(want, w.ID)
			out = append(out, w)
		}
	}
	if !all && len(want) > 0 {
		var missing []string
		for id := range want {
			missing = append(missing, id)
		}
		return nil, fmt.Errorf("no workload with id %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// compileInput is what an engine compiles once for an item.
func (it Item) compileInput() string {
	switch it.Area {
	case "xslt":
		return it.Stylesheet
	case "xquery":
		return it.Query
	case "xsd", "rng":
		return it.Schema
	}
	return ""
}

// expandArgv fills an argv template. An argument with any placeholder that is
// empty is dropped, so "-s:{source}" disappears for a sourceless transform.
func expandArgv(tmpl []string, vars map[string]string, params map[string]string,
	paramFormat, extra []string) []string {
	var out []string
	for _, a := range tmpl {
		switch a {
		case "{params}":
			names := make([]string, 0, len(params))
			for k := range params {
				names = append(names, k)
			}
			sort.Strings(names)
			for _, k := range names {
				for _, f := range paramFormat {
					out = append(out, strings.NewReplacer("{name}", k, "{value}", params[k]).Replace(f))
				}
			}
			continue
		case "{args}":
			out = append(out, extra...)
			continue
		}
		s, empty := a, false
		for k, v := range vars {
			ph := "{" + k + "}"
			if strings.Contains(s, ph) {
				if v == "" {
					empty = true
				}
				s = strings.ReplaceAll(s, ph, v)
			}
		}
		if !empty {
			out = append(out, s)
		}
	}
	return out
}
