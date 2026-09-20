//go:build unix

package daemon

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/matthewlu070111/smart-srun/core/internal/config"
	"github.com/matthewlu070111/smart-srun/core/internal/domain"
)

func TestBackupRPCPreviewCASAndCredentialIsolation(t *testing.T) {
	service := start(t, nil)
	data, err := os.ReadFile("../../../tests/fixtures/config-backup-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	params := BackupImportParams{Data: string(data), CheckOnly: true}
	raw := service.call("config.import", params)
	var preview BackupImportResult
	if json.Unmarshal(raw, &preview) != nil || preview.CampusAccounts != 2 || preview.ExpectedRevision != 0 {
		t.Fatal("wrong preview")
	}
	if strings.Contains(string(raw), "synthetic-secret") {
		t.Fatal("preview exposed secret")
	}
	before := service.call("config.get", nil)
	if strings.Contains(string(before), "example-user") {
		t.Fatal("preview wrote config")
	}
	params.CheckOnly = false
	if codeOf(t, service.callExpectingError("config.import", params)) != domain.CodeInvalidArgument {
		t.Fatal("missing CAS accepted")
	}
	params.ExpectedRevision = &preview.ExpectedRevision
	result := service.call("config.import", params)
	if strings.Contains(string(result), "synthetic-secret") {
		t.Fatal("import receipt exposed secret")
	}
	if codeOf(t, service.callExpectingError("config.import", params)) != domain.CodeConflict {
		t.Fatal("stale import accepted")
	}
	persisted, err := config.LoadFile(service.paths.ConfigFile())
	if err != nil || persisted.Enabled || persisted.Revision != 1 || len(persisted.CampusAccounts) != 2 {
		t.Fatal("wrong persisted import")
	}
	if codeOf(t, service.callExpectingError("config.export", json.RawMessage(`{}`))) != domain.CodeInvalidArgument {
		t.Fatal("implicit credential export accepted")
	}
	export := service.call("config.export", json.RawMessage(`{"include_secrets":true}`))
	if _, _, err := config.ParseBackup(export); err != nil || !strings.Contains(string(export), "synthetic-secret") {
		t.Fatal("explicit export not restorable")
	}
	for _, method := range []string{"config.get", "campus.get", "hotspot.get", "status.get"} {
		if strings.Contains(string(service.call(method, nil)), "synthetic-secret") {
			t.Fatalf("%s exposed secret", method)
		}
	}
}
