// Package obligations validates the machine-readable falsification matrix
// manifest. The checker is itself code and data under this repo's
// discipline: its RED set is the tamper tests in obligations_test.go.
package obligations

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var legalStatuses = map[string]bool{
	"ENFORCED":          true,
	"UNVERIFIED":        true,
	"ACCEPTED_RESIDUAL": true,
}

type obligation struct {
	ID        string   `json:"id"`
	Layer     string   `json:"layer"`
	Dimension string   `json:"dimension"`
	Status    string   `json:"status"`
	Note      string   `json:"note"`
	Tests     []string `json:"tests"`
}

type uncitedEntry struct {
	Package string `json:"package"`
	Test    string `json:"test"`
	Reason  string `json:"reason"`
}

type manifestFile struct {
	Obligations []obligation   `json:"obligations"`
	Uncited     []uncitedEntry `json:"uncited"`
}

var idPattern = regexp.MustCompile(`GH-[A-Z0-9-]+`)

// Check validates the manifest against the actual test listings and the
// README. It proves referential integrity, not just name existence:
// schema and status enum, unique IDs, package-qualified citations that
// resolve in the named package, README↔manifest ID agreement in both
// directions, and exact classification of every listed test as cited or
// declared-uncited-with-reason.
func Check(manifest []byte, listings map[string][]string, readme string) error {
	var m manifestFile
	if err := json.Unmarshal(manifest, &m); err != nil {
		return fmt.Errorf("manifest 不是合法 JSON：%w", err)
	}
	if len(m.Obligations) == 0 {
		return fmt.Errorf("manifest 没有任何义务条目")
	}

	seenIDs := map[string]bool{}
	readmeIDs := map[string]bool{}
	for _, id := range idPattern.FindAllString(readme, -1) {
		readmeIDs[id] = true
	}
	manifestIDs := map[string]bool{}
	cited := map[string]bool{} // "pkg:Test" keys

	for _, ob := range m.Obligations {
		if ob.ID == "" {
			return fmt.Errorf("义务缺少 id")
		}
		if seenIDs[ob.ID] {
			return fmt.Errorf("义务 id 重复：%s", ob.ID)
		}
		seenIDs[ob.ID] = true
		manifestIDs[ob.ID] = true
		if ob.Layer == "" || ob.Dimension == "" {
			return fmt.Errorf("义务 %s 缺少 layer/dimension", ob.ID)
		}
		if !legalStatuses[ob.Status] {
			return fmt.Errorf("义务 %s 的 status %q 不在合法枚举（ENFORCED/UNVERIFIED/ACCEPTED_RESIDUAL）——拼写错误不得静默豁免", ob.ID, ob.Status)
		}
		switch ob.Status {
		case "ENFORCED":
			if len(ob.Tests) == 0 {
				return fmt.Errorf("ENFORCED 义务 %s 未引用任何测试", ob.ID)
			}
		default:
			if ob.Note == "" {
				return fmt.Errorf("%s 义务 %s 缺少 note", ob.Status, ob.ID)
			}
			if len(ob.Tests) > 0 {
				return fmt.Errorf("%s 义务 %s 不应引用测试", ob.Status, ob.ID)
			}
		}
		for _, ref := range ob.Tests {
			parts := strings.SplitN(ref, ":", 2)
			if len(parts) != 2 {
				return fmt.Errorf("义务 %s 的引用 %q 不是 package:Test 形式", ob.ID, ref)
			}
			pkg, name := parts[0], parts[1]
			names, ok := listings[pkg]
			if !ok {
				return fmt.Errorf("义务 %s 引用了未知包 %q", ob.ID, pkg)
			}
			found := false
			for _, listed := range names {
				if listed == name {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("义务 %s 引用的 %s:%s 在该包的测试清单中不存在", ob.ID, pkg, name)
			}
			cited[ref] = true
		}
	}

	// README ↔ manifest: every id in the matrix prose must exist in the
	// manifest, and every manifest id must appear in the prose.
	for id := range manifestIDs {
		if !readmeIDs[id] {
			return fmt.Errorf("manifest id %s 未出现在 README 矩阵中", id)
		}
	}
	for id := range readmeIDs {
		if !manifestIDs[id] {
			return fmt.Errorf("README 提及的 id %s 不在 manifest 中", id)
		}
	}

	// Every listed test must be classified exactly once: cited by some
	// ENFORCED entry, or declared uncited with a reason — and never both.
	var pkgs []string
	for pkg := range listings {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	for _, pkg := range pkgs {
		for _, name := range listings[pkg] {
			ref := pkg + ":" + name
			declaredUncited := false
			for _, u := range m.Uncited {
				if u.Package == pkg && u.Test == name {
					if cited[ref] {
						return fmt.Errorf("测试 %s 同时被引用和声明为未引用", ref)
					}
					if u.Reason == "" {
						return fmt.Errorf("未引用测试 %s 缺少理由", ref)
					}
					declaredUncited = true
				}
			}
			if !cited[ref] && !declaredUncited {
				return fmt.Errorf("测试 %s 既未被引用也未声明为未引用（每个测试必须显式分类）", ref)
			}
		}
	}
	for _, u := range m.Uncited {
		if _, ok := listings[u.Package]; !ok {
			return fmt.Errorf("未引用条目引用未知包 %q", u.Package)
		}
		found := false
		for _, name := range listings[u.Package] {
			if name == u.Test {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("未引用条目 %s:%s 不在测试清单中", u.Package, u.Test)
		}
	}
	return nil
}
