package config

import "github.com/matthewlu070111/smart-srun/core/internal/domain"

// FieldKind is the shape a value has, in terms a form can act on.
type FieldKind string

const (
	KindBool    FieldKind = "bool"
	KindInt     FieldKind = "int"
	KindSeconds FieldKind = "seconds"
	KindString  FieldKind = "string"
	KindEnum    FieldKind = "enum"
	KindClock   FieldKind = "clock"
	KindList    FieldKind = "list"
	KindMap     FieldKind = "map"
)

// Field describes one configurable value.
//
// Go owns types, defaults, bounds and choices; Lua keeps the labels, layout and
// help text it already has. Splitting it this way means the two cannot disagree
// about what a valid value is, while the frozen page keeps its own wording.
type Field struct {
	Path     string    `json:"path"`
	Kind     FieldKind `json:"kind"`
	Default  any       `json:"default,omitempty"`
	Min      *float64  `json:"min,omitempty"`
	Max      *float64  `json:"max,omitempty"`
	MaxBytes int       `json:"max_bytes,omitempty"`
	Choices  []string  `json:"choices,omitempty"`
	// Secret marks a value that must never appear in a list response, a status
	// snapshot, a log line, an error or an exported issue draft.
	Secret bool `json:"secret,omitempty"`
	// Required marks a value that may not be empty.
	Required bool `json:"required,omitempty"`
	// AppliesTo narrows a field to one access mode. Empty means both.
	AppliesTo domain.AccessMode `json:"applies_to,omitempty"`
	Note      string            `json:"note,omitempty"`
}

// Schema is the whole read-only contract handed to the UI and the CLI.
type Schema struct {
	SchemaVersion int     `json:"schema_version"`
	MaxBytes      int     `json:"max_config_bytes"`
	Global        []Field `json:"global"`
	CampusAccount []Field `json:"campus_account"`
	Hotspot       []Field `json:"hotspot"`
	Collections   struct {
		MaxCampusAccounts       int `json:"max_campus_accounts"`
		MaxHotspotProfiles      int `json:"max_hotspot_profiles"`
		MaxManagedWiredAccounts int `json:"max_managed_wired_accounts"`
		MaxSchoolExtraKeys      int `json:"max_school_extra_keys"`
	} `json:"collections"`
	// UnverifiedOperatorSuffix is published so the editor can show the hint for
	// it without hard-coding the sentinel in two languages.
	UnverifiedOperatorSuffix string `json:"unverified_operator_suffix"`
}

func num(value float64) *float64 { return &value }

func enumStrings[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}
	return out
}

// BuildSchema derives the contract from the same defaults and limits the
// validator enforces, so a changed default cannot reach the form as the old one.
func BuildSchema() Schema {
	defaults := Defaults()

	schema := Schema{
		SchemaVersion:            domain.ConfigSchemaVersion,
		MaxBytes:                 MaxConfigBytes,
		Global:                   globalFields(defaults),
		CampusAccount:            campusAccountFields(),
		Hotspot:                  hotspotFields(),
		UnverifiedOperatorSuffix: UnverifiedOperatorSuffix,
	}
	schema.Collections.MaxCampusAccounts = MaxCampusAccounts
	schema.Collections.MaxHotspotProfiles = MaxHotspotProfiles
	schema.Collections.MaxManagedWiredAccounts = MaxManagedWiredAccounts
	schema.Collections.MaxSchoolExtraKeys = MaxSchoolExtraKeys
	return schema
}

func globalFields(defaults domain.Config) []Field {
	return []Field{
		{Path: "enabled", Kind: KindBool, Default: defaults.Enabled},
		{Path: "multi_wan_enabled", Kind: KindBool, Default: defaults.MultiWANEnabled},
		{Path: "school", Kind: KindString, Default: defaults.School,
			MaxBytes: MaxNameBytes, Required: true},
		{Path: "sta_iface", Kind: KindString, Default: defaults.STAIface,
			MaxBytes: MaxNameBytes},

		{Path: "login_defaults.n", Kind: KindString, Default: defaults.LoginDefaults.N,
			MaxBytes: MaxNameBytes, Required: true,
			Note: "协议字符串，不转成整数"},
		{Path: "login_defaults.type", Kind: KindString, Default: defaults.LoginDefaults.Type,
			MaxBytes: MaxNameBytes, Required: true},
		{Path: "login_defaults.enc", Kind: KindString, Default: defaults.LoginDefaults.Enc,
			MaxBytes: MaxNameBytes, Required: true},

		{Path: "selection.active_campus_id", Kind: KindString, MaxBytes: MaxIDBytes},
		{Path: "selection.default_campus_id", Kind: KindString, MaxBytes: MaxIDBytes},
		{Path: "selection.active_hotspot_id", Kind: KindString, MaxBytes: MaxIDBytes},
		{Path: "selection.default_hotspot_id", Kind: KindString, MaxBytes: MaxIDBytes},

		{Path: "quiet.enabled", Kind: KindBool, Default: defaults.Quiet.Enabled},
		{Path: "quiet.start", Kind: KindClock, Default: defaults.Quiet.Start.String(),
			Note: "北京时间 UTC+08:00；start 等于 end 表示空窗口"},
		{Path: "quiet.end", Kind: KindClock, Default: defaults.Quiet.End.String()},
		{Path: "quiet.force_logout", Kind: KindBool, Default: defaults.Quiet.ForceLogout},

		{Path: "retry.enabled", Kind: KindBool, Default: defaults.Retry.Enabled},
		{Path: "retry.max_retries", Kind: KindInt, Default: defaults.Retry.MaxRetries,
			Min: num(MinMaxRetries), Max: num(MaxMaxRetries),
			Note: "首次尝试之后的重试次数；0 表示不设有限次数"},
		{Path: "retry.initial_seconds", Kind: KindSeconds,
			Default: defaults.Retry.InitialSeconds.Float(),
			Min:     num(0), Max: num(MaxCooldownSeconds)},
		{Path: "retry.max_seconds", Kind: KindSeconds,
			Default: defaults.Retry.MaxSeconds.Float(),
			Min:     num(0), Max: num(MaxCooldownSeconds),
			Note: "不得小于 retry.initial_seconds"},

		{Path: "checks.interval_seconds", Kind: KindInt, Default: defaults.Checks.IntervalSeconds,
			Min: num(MinIntervalSeconds), Max: num(MaxIntervalSeconds)},
		{Path: "checks.mode", Kind: KindEnum, Default: string(defaults.Checks.Mode),
			Choices: enumStrings(domain.CheckModes())},
		{Path: "checks.switch_timeout_seconds", Kind: KindInt,
			Default: defaults.Checks.SwitchTimeoutSeconds,
			Min:     num(MinSwitchTimeoutSeconds), Max: num(MaxSwitchTimeoutSeconds)},
		{Path: "checks.terminal_attempts", Kind: KindInt,
			Default: defaults.Checks.TerminalAttempts,
			Min:     num(MinTerminalAttempts), Max: num(MaxTerminalAttempts)},
		{Path: "checks.terminal_interval_seconds", Kind: KindInt,
			Default: defaults.Checks.TerminalIntervalSeconds,
			Min:     num(MinTerminalIntervalSeconds), Max: num(MaxTerminalIntervalSeconds)},

		{Path: "failover.enabled", Kind: KindBool, Default: defaults.Failover.Enabled},
		{Path: "failover.hotspot_failback_enabled", Kind: KindBool,
			Default: defaults.Failover.HotspotFailbackEnabled},

		{Path: "log.level", Kind: KindEnum, Default: string(defaults.Log.Level),
			Choices: enumStrings(domain.LogLevels()),
			Note:    "ALL 只是最低阈值，不是事件等级"},

		{Path: "campus_accounts", Kind: KindList},
		{Path: "hotspot_profiles", Kind: KindList},
		{Path: "school_extra", Kind: KindMap,
			Note: "只接受当前策略声明的描述符；未声明的键在规范化时丢弃"},
	}
}

func campusAccountFields() []Field {
	return []Field{
		{Path: "id", Kind: KindString, MaxBytes: MaxIDBytes, Required: true},
		{Path: "label", Kind: KindString, MaxBytes: MaxLabelBytes},
		{Path: "user_id", Kind: KindString, MaxBytes: MaxUserIDBytes, Required: true},
		{Path: "password", Kind: KindString, MaxBytes: MaxSecretBytes, Secret: true},
		{Path: "operator", Kind: KindString, MaxBytes: MaxLabelBytes,
			Note: "仅展示标签；不决定用户名"},
		{Path: "operator_suffix", Kind: KindString, MaxBytes: MaxSuffixBytes,
			Note: "空串表示明确无后缀；非空表示 user@suffix"},
		{Path: "access_mode", Kind: KindEnum, Default: string(domain.AccessModeWired),
			Choices: enumStrings(domain.AccessModes()), Required: true},

		{Path: "wired_iface", Kind: KindString, Default: DefaultWiredIface,
			MaxBytes: MaxNameBytes, Required: true, AppliesTo: domain.AccessModeWired},
		{Path: "auth_enabled", Kind: KindBool, Default: false,
			AppliesTo: domain.AccessModeWired,
			Note:      "仅在全局 multi_wan_enabled 打开时生效"},

		{Path: "base_url", Kind: KindString, MaxBytes: MaxURLBytes},
		{Path: "ac_id", Kind: KindString, MaxBytes: MaxNameBytes},

		{Path: "ssid", Kind: KindString, MaxBytes: MaxNameBytes, Required: true,
			AppliesTo: domain.AccessModeWiFi, Note: "首尾空格是合法的，不做裁剪"},
		{Path: "radio", Kind: KindString, MaxBytes: MaxNameBytes,
			AppliesTo: domain.AccessModeWiFi},
		{Path: "encryption", Kind: KindString, Default: EncryptionNone,
			MaxBytes: MaxNameBytes, Required: true, AppliesTo: domain.AccessModeWiFi},
		{Path: "key", Kind: KindString, MaxBytes: MaxSecretBytes, Secret: true,
			AppliesTo: domain.AccessModeWiFi},
		{Path: "ap_selection", Kind: KindEnum, Default: string(domain.APSelectionAuto),
			Choices: enumStrings(domain.APSelections()), AppliesTo: domain.AccessModeWiFi},
		{Path: "bssid", Kind: KindString, MaxBytes: MaxNameBytes,
			AppliesTo: domain.AccessModeWiFi,
			Note:      "ap_selection=fixed 时必填，且必须是单播 MAC"},

		{Path: "login.n", Kind: KindString, MaxBytes: MaxNameBytes},
		{Path: "login.type", Kind: KindString, MaxBytes: MaxNameBytes},
		{Path: "login.enc", Kind: KindString, MaxBytes: MaxNameBytes},
		{Path: "login.info_prefix", Kind: KindString, Default: DefaultInfoPrefix,
			MaxBytes: MaxNameBytes},
		{Path: "login.double_stack", Kind: KindBool, Default: DefaultDoubleStack,
			Note: "配置里是布尔值，发到网关时映射为 \"0\"/\"1\""},
		{Path: "login.os", Kind: KindString, Default: DefaultLoginOS, MaxBytes: MaxNameBytes},
		{Path: "login.name", Kind: KindString, Default: DefaultLoginName, MaxBytes: MaxNameBytes},

		{Path: "preset_id", Kind: KindString, MaxBytes: MaxIDBytes,
			Note: "只记录填写来源；刷新目录不得据此覆盖已保存参数"},
	}
}

func hotspotFields() []Field {
	return []Field{
		{Path: "id", Kind: KindString, MaxBytes: MaxIDBytes, Required: true},
		{Path: "label", Kind: KindString, MaxBytes: MaxLabelBytes},
		{Path: "ssid", Kind: KindString, MaxBytes: MaxNameBytes, Required: true},
		{Path: "encryption", Kind: KindString, Default: EncryptionNone,
			MaxBytes: MaxNameBytes},
		{Path: "key", Kind: KindString, MaxBytes: MaxSecretBytes, Secret: true},
		{Path: "radio", Kind: KindString, MaxBytes: MaxNameBytes},
	}
}
