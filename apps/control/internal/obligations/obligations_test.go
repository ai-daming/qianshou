package obligations

import (
	"encoding/json"
	"strings"
	"testing"
)

// The manifest the checker must accept: one obligation cited to an existing
// test, one residual with a note, the other listed test classified as
// uncited, README mentioning the manifest id.
const validManifest = `{
  "obligations": [
    {"id": "GH-X-WF-ONE", "layer": "transport", "dimension": "well-formedness", "status": "ENFORCED", "tests": ["ghfacts:TestOne"]},
    {"id": "GH-X-RS-TWO", "layer": "aggregation", "dimension": "freshness", "status": "ACCEPTED_RESIDUAL", "note": "undetectable without re-listing"}
  ],
  "uncited": [
    {"package": "scope", "test": "TestTwo", "reason": "happy-path helper covered by TestOne"}
  ]
}`

var validListings = map[string][]string{
	"ghfacts": {"TestOne"},
	"scope":   {"TestTwo"},
}

const validReadme = "matrix: GH-X-WF-ONE is enforced somewhere"

// stub until the implementation lands in GREEN: the tamper tests below must
// fail against it (they do — it accepts everything), proving RED.
func Check(manifest []byte, listings map[string][]string, readme string) error {
	_ = json.Marshal
	_ = strings.TrimSpace
	return nil
}

func TestValidManifestPasses(t *testing.T) {
	if err := Check([]byte(validManifest), validListings, validReadme); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestCheckerRejectsTamperedManifests(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		readme   string
	}{
		{"status typo exempts an obligation", strings.Replace(validManifest, `"status": "ENFORCED"`, `"status": "ENFORCED_TYPO"`, 1), validReadme},
		{"citation binds a test in the wrong package", strings.Replace(validManifest, "ghfacts:TestOne", "scope:TestOne", 1), validReadme},
		{"citation names an unknown test", strings.Replace(validManifest, "ghfacts:TestOne", "ghfacts:TestMissing", 1), validReadme},
		{"enforced entry without tests", strings.Replace(validManifest, `["ghfacts:TestOne"]`, `[]`, 1), validReadme},
		{"residual entry carrying tests", strings.Replace(validManifest, `"status": "ACCEPTED_RESIDUAL", "note": "undetectable without re-listing"`, `"status": "ACCEPTED_RESIDUAL", "note": "n", "tests": ["ghfacts:TestOne"]`, 1), validReadme},
		{"residual entry without note", strings.Replace(validManifest, `"note": "undetectable without re-listing"`, `"note": ""`, 1), validReadme},
		{"manifest id missing from readme", validManifest, "no ids here"},
		{"readme id missing from manifest", validManifest, validReadme + " GH-X-GHOST"},
		{"listed test neither cited nor classified", strings.Replace(validManifest, `{"package": "scope", "test": "TestTwo", "reason": "happy-path helper covered by TestOne"}`, `{"package": "scope", "test": "TestTwo", "reason": ""}`, 1), validReadme},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := Check([]byte(tc.manifest), validListings, tc.readme); err == nil {
				t.Fatalf("tampered manifest accepted")
			}
		})
	}
	// duplicate ids
	dup := strings.Replace(validManifest, "GH-X-RS-TWO", "GH-X-WF-ONE", 1)
	if err := Check([]byte(dup), validListings, validReadme); err == nil {
		t.Fatalf("duplicate ids accepted")
	}
	// unclassified new test appears in listings
	bigger := map[string][]string{"ghfacts": {"TestOne", "TestNew"}, "scope": {"TestTwo"}}
	if err := Check([]byte(validManifest), bigger, validReadme); err == nil {
		t.Fatalf("unclassified test accepted")
	}
	// uncited entry that is also cited
	both := strings.Replace(validManifest, `{"package": "scope", "test": "TestTwo", "reason": "happy-path helper covered by TestOne"}`, `{"package": "ghfacts", "test": "TestOne", "reason": "double"}`, 1)
	if err := Check([]byte(both), validListings, validReadme); err == nil {
		t.Fatalf("cited test also classified uncited")
	}
	// malformed json
	if err := Check([]byte("{"), validListings, validReadme); err == nil {
		t.Fatalf("malformed json accepted")
	}
}
