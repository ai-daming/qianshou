package obligations

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Run executes the manifest check against the real tree from the module
// root (apps/control): collects per-package test listings, loads the
// manifest and README, validates referential integrity, then runs every
// cited test.
func Run() error {
	listings := map[string][]string{
		"ghfacts": nil,
		"scope":   nil,
	}
	pkgPaths := map[string]string{
		"ghfacts": "./internal/ghfacts",
		"scope":   "./internal/scope",
	}
	for pkg, path := range pkgPaths {
		out, err := exec.Command("go", "test", "-list", ".*", path).Output()
		if err != nil {
			return fmt.Errorf("列出 %s 的测试失败：%w", path, err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "Test") {
				listings[pkg] = append(listings[pkg], strings.TrimSpace(line))
			}
		}
	}
	manifest, err := os.ReadFile(filepath.Join("internal", "ghfacts", "obligations.json"))
	if err != nil {
		return fmt.Errorf("读取 manifest：%w", err)
	}
	readme, err := os.ReadFile(filepath.Join("internal", "ghfacts", "README.md"))
	if err != nil {
		return fmt.Errorf("读取 README：%w", err)
	}
	if err := Check(manifest, listings, string(readme)); err != nil {
		return err
	}
	// Run every cited test, selected by name.
	cited := map[string]bool{}
	var m manifestFile
	if err := jsonUnmarshalStrict(manifest, &m); err != nil {
		return err
	}
	for _, ob := range m.Obligations {
		for _, ref := range ob.Tests {
			cited[strings.SplitN(ref, ":", 2)[1]] = true
		}
	}
	var names []string
	for name := range cited {
		names = append(names, name)
	}
	pattern := "^(" + strings.Join(names, "|") + ")$"
	cmd := exec.Command("go", "test", "-count=1", "-run", pattern, pkgPaths["ghfacts"], pkgPaths["scope"])
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	fmt.Println("obligations: running", len(names), "cited tests")
	return cmd.Run()
}

func jsonUnmarshalStrict(data []byte, out *manifestFile) error {
	return json.Unmarshal(data, out)
}
