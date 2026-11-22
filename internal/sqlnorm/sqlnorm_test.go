package sqlnorm

import (
	"encoding/json"
	"os"
	"testing"
)

// The shared contract lives at the repo root; the published SDK mirror does not carry it.
const vectorsPath = "../../../../testdata/sql_normalize_vectors.json"

func TestSharedVectors(t *testing.T) {
	raw, err := os.ReadFile(vectorsPath)
	if os.IsNotExist(err) {
		t.Skip("shared vectors not in this checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Vectors []struct {
			Name, Input, Normalized, Hash string
			PG                            bool
		}
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Vectors) == 0 {
		t.Fatal("no vectors")
	}
	for _, v := range f.Vectors {
		n := Normalize(v.Input, v.PG)
		if n != v.Normalized {
			t.Errorf("%s: Normalize = %q, want %q", v.Name, n, v.Normalized)
		}
		if h := Hash(v.Normalized); h != v.Hash {
			t.Errorf("%s: Hash = %s, want %s", v.Name, h, v.Hash)
		}
	}
}

func TestSummary(t *testing.T) {
	for _, c := range []struct{ in, op, table string }{
		{"SELECT * FROM users WHERE id = ?", "SELECT", "users"},
		{`update "public"."jobs" set a = ?`, "UPDATE", "public.jobs"},
		{"WITH x AS (SELECT ?) DELETE FROM t", "DELETE", "t"},
		{"PRAGMA wal_checkpoint(TRUNCATE)", "PRAGMA", ""},
	} {
		if op, table := Summary(c.in); op != c.op || table != c.table {
			t.Errorf("Summary(%q) = %q %q", c.in, op, table)
		}
	}
}
