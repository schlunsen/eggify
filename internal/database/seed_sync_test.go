package database

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Go's //go:embed cannot reference files outside the embedding package's own
// directory, so the provider and connector seed data is physically duplicated:
//
//	internal/database/providers_seed.json   <-> internal/providers/providers.json
//	internal/database/connectors_seed.json  <-> internal/connectors/connectors_seed.json
//
// The copies must stay byte-identical — migrations seed the database from the
// internal/database copy while the runtime registries read the sibling copy, so
// silent drift produces a database that disagrees with the running code.
//
// These tests fail loudly the moment one copy is edited without the other.
func TestSeedCopiesAreInSync(t *testing.T) {
	tests := []struct {
		name      string
		embedded  []byte
		localName string
		sibling   string
	}{
		{
			name:      "providers",
			embedded:  providersJSONData,
			localName: "providers_seed.json",
			sibling:   filepath.Join("..", "providers", "providers.json"),
		},
		{
			name:      "connectors",
			embedded:  connectorsSeedData,
			localName: "connectors_seed.json",
			sibling:   filepath.Join("..", "connectors", "connectors_seed.json"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sibling, err := os.ReadFile(tt.sibling)
			if err != nil {
				t.Fatalf("reading %s: %v", tt.sibling, err)
			}

			if !bytes.Equal(tt.embedded, sibling) {
				t.Errorf("internal/database/%s and %s have diverged (%d vs %d bytes).\n"+
					"These files must stay byte-identical — copy the intended version over the other.",
					tt.localName, tt.sibling, len(tt.embedded), len(sibling))
			}
		})
	}
}
