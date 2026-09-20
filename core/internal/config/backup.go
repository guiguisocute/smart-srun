package config

import (
	"encoding/json"
	"reflect"
	"unicode/utf8"

	"github.com/matthewlu070111/smart-srun/core/internal/domain"
)

const BackupFormat = "smart-srun-config"

// Backup is an explicit credential-bearing transfer document, never runtime state.
type Backup struct {
	Format        string          `json:"format"`
	FormatVersion int             `json:"format_version"`
	ConfigSchema  int             `json:"config_schema"`
	Config        json.RawMessage `json:"config"`
}

func ExportBackup(cfg domain.Config) (Backup, error) {
	data, err := Marshal(cfg)
	if err != nil {
		return Backup{}, err
	}
	result := Backup{BackupFormat, 1, domain.ConfigSchemaVersion, data}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > MaxConfigBytes {
		return Backup{}, invalidBackup()
	}
	return result, nil
}

// ParseBackup accepts only versioned exports. Errors intentionally omit supplied
// keys/values: a malformed document may put credentials anywhere, even in a key.
func ParseBackup(data []byte) (domain.Config, []string, error) {
	if len(data) > MaxConfigBytes || !utf8.Valid(data) || scanStrict(data) != nil {
		return domain.Config{}, nil, invalidBackup()
	}
	var envelope Backup
	if DecodePatch(data, &envelope) != nil || envelope.Format != BackupFormat || envelope.FormatVersion != 1 {
		return domain.Config{}, nil, invalidBackup()
	}
	var cfg domain.Config
	var warnings []string
	var err error
	switch envelope.ConfigSchema {
	case 1:
		cfg, warnings, err = importLegacy(envelope.Config)
	case domain.ConfigSchemaVersion:
		if !exactFields(envelope.Config, reflect.TypeFor[domain.Config]()) {
			return domain.Config{}, nil, invalidBackup()
		}
		cfg, err = Parse(envelope.Config)
	default:
		err = invalidBackup()
	}
	if err != nil {
		return domain.Config{}, nil, invalidBackup()
	}
	// An export can be restored onto another router. Enabling policy is always
	// a separate user action after checking credentials and interface names.
	cfg.Enabled = false
	cfg.Revision = 0
	return cfg, warnings, nil
}

func invalidBackup() error {
	return domain.Errorf(domain.CodeInvalidConfig, "备份格式或配置无效；请选择 1.6.1 或 2.0 导出的 JSON，检查字段类型、范围和账号引用（上限 512 KiB）")
}
