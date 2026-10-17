package git

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestCommitEntry(t *testing.T) {
	c, err := HeadCommit()
	if err != nil {
		t.Fatal(err)
	}

	cr, err := Commits(c)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range cr {
		for _, cV := range FieldNames {
			found := false

			for k := range c {
				if k == cV {
					found = true
				}
			}

			if !found {
				t.Errorf("failed to find field names: %q", c)
			}
		}
	}
}

func TestMarshal(t *testing.T) {
	buf, err := os.ReadFile("testdata/commits.json")
	if err != nil {
		t.Fatal(err)
	}

	cr := []CommitEntry{}
	if err := json.Unmarshal(buf, &cr); err != nil {
		t.Error(err)
	}

	for _, c := range cr {
		for _, cV := range FieldNames {
			found := false

			for k := range c {
				if k == cV {
					found = true
				}
			}

			if !found {
				t.Errorf("failed to find field names: %q", c)
			}
		}
	}
}

func TestVersionAtLeast(t *testing.T) {
	for _, tc := range []struct {
		have, want string
		ok         bool
	}{
		{"2.54.0", "1.9.5", true},
		{"1.9.5", "1.9.5", true},
		{"1.9.4", "1.9.5", false},
		{"1.9", "1.9.5", false},
		{"1.10.0", "1.9.5", true},
		{"2.42.0.windows.2", "1.9.5", true},
	} {
		got, err := versionAtLeast(tc.have, tc.want)
		if err != nil || got != tc.ok {
			t.Errorf("versionAtLeast(%q, %q) = %v, %v, want %v", tc.have, tc.want, got, err, tc.ok)
		}
	}

	if _, err := versionAtLeast("windows", "1.9.5"); !errors.Is(err, errNoVersionNumber) {
		t.Errorf("versionAtLeast(%q, _) error = %v, want errNoVersionNumber", "windows", err)
	}
}
