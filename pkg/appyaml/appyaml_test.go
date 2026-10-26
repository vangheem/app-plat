package appyaml

import "testing"

func TestParse(t *testing.T) {
	f, err := Parse("name: My App\nport: 8080\npersistence:\n  - postgresql\n  - blob\n  - keyvalue\n")
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "My App" || f.Port != 8080 || len(f.Persistence) != 3 {
		t.Fatalf("unexpected %#v", f)
	}
	if _, err := Parse("persistence:\n  - dynamodb\n"); err == nil {
		t.Fatal("expected error for unknown persistence")
	}
}
