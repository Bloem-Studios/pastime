package identity_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoRawSessionKindComparisons keeps every trust decision explicit. A
// profile session is a browser session but not the household's, so code must
// say which it means with Actor.Browser or Actor.Household; comparing Kind
// with "session" would quietly mean one of them.
func TestNoRawSessionKindComparisons(t *testing.T) {
	raw := regexp.MustCompile(`Kind\s*[!=]=\s*("session"|(identity\.)?KindSession\b)|case\s+"session"\s*:`)
	root := filepath.Join("..")
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || filepath.ToSlash(path) == "../identity/model.go" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if raw.MatchString(line) {
				found = append(found, filepath.ToSlash(path)+":"+itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Fatalf("%d raw session kind comparisons; use Actor.Browser() or Actor.Household():\n%s", len(found), strings.Join(found, "\n"))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
