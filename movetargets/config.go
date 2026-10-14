package movetargets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// config is the plan written by "plan" and executed by "apply".
type config struct {
	SrcPackage              string      `json:"src_package"`
	DstPackage              string      `json:"dst_package"`
	StrictSourceDirectories bool        `json:"strict_source_directories"`
	PackageComment          string      `json:"package_comment"`
	LabelAttrs              []string    `json:"label_attrs"`
	FileAttrs               []string    `json:"file_attrs"`
	RefuseRuleKinds         []string    `json:"refuse_rule_kinds"`
	PackageRelativeAttrs    []string    `json:"package_relative_attrs"`
	OutputAttrs             []string    `json:"output_attrs"`
	Targets                 []target    `json:"targets"`
	ExtraNames              []target    `json:"extra_names,omitempty"`
	Statements              []lineRange `json:"statements,omitempty"`
}

type target struct {
	Name    string `json:"name"`
	NewName string `json:"new_name"`
	Kind    string `json:"kind,omitempty"`
	Alias   bool   `json:"alias"`
}

type lineRange struct {
	First int `json:"first_line"`
	Last  int `json:"last_line"`
}

func readConfig(filePath string) (*config, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cfg config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filePath, err)
	}
	cfg.SrcPackage = trimPkg(cfg.SrcPackage)
	cfg.DstPackage = trimPkg(cfg.DstPackage)
	return &cfg, nil
}

func (cfg *config) marshal() ([]byte, error) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func trimPkg(p string) string {
	return strings.TrimSuffix(strings.TrimPrefix(p, "//"), "/")
}

// validate checks that the destination is a subpackage of the source.
func (cfg *config) validate() error {
	if _, ok := relDir(cfg.SrcPackage, cfg.DstPackage); !ok {
		return fmt.Errorf("dst_package //%s must be strictly under src_package //%s", cfg.DstPackage, cfg.SrcPackage)
	}
	return nil
}

// relDir returns dst relative to src, or false unless dst is strictly under src.
func relDir(src, dst string) (string, bool) {
	if src == "" {
		return dst, dst != ""
	}
	rel, ok := strings.CutPrefix(dst, src+"/")
	if !ok || rel == "" {
		return "", false
	}
	return rel, true
}
