package presets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/matthewlu070111/smart-srun/core/internal/domain"
)

// FileMode and DirMode are what the cache is written with.
//
// A catalogue is public data -- it is fetched over the open internet by
// anybody -- so the mode is not protecting a secret. It matches the rest of
// what this program writes so that a directory listing has one answer rather
// than two, and the directory above it is what actually keeps other users out.
const (
	FileMode = 0o600
	DirMode  = 0o700
)

// Cache is the last good catalogue, on flash.
//
// On flash and not in the runtime directory: the whole point of it is that a
// router which boots with no internet still knows which schools exist, and a
// tmpfs copy would be gone exactly when it was needed. It is still not the
// user's data -- it is a copy of something published, regenerable from the
// network -- so it belongs beside the recovery journals rather than beside
// config.json, and it must not be preserved across a sysupgrade.
type Cache struct {
	path string
}

// NewCache addresses a cache file. It creates nothing: a router that never
// refreshes should not gain a directory for a file it will not write.
func NewCache(path string) *Cache { return &Cache{path: path} }

// Path is where it lives, for a caller that has to report it.
func (c *Cache) Path() string { return c.path }

// cached is the wrapper the file actually holds.
//
// The published document goes under its own key rather than having this
// program's bookkeeping merged into it. Adding _cached_at beside the
// publisher's own fields -- which is what the baseline does -- means a future
// schema that happens to use that name collides with it.
//
// Under its own key, not byte-for-byte: the encoder re-indents the nested
// document, so what comes back is the same JSON and not the same bytes. That is
// enough for the reason the payload is kept at all -- a later build with a
// different parser reads the content, and no JSON parser can see whitespace --
// and the indented form is what makes the file readable on a router, which is
// where somebody will be looking at it.
type cached struct {
	CachedAt  int64           `json:"cached_at"`
	SourceURL string          `json:"source_url"`
	Payload   json.RawMessage `json:"payload"`
}

// Entry is what Load found: the catalogue and where it came from.
type Entry struct {
	Catalogue Catalogue
	SourceURL string
	CachedAt  time.Time
	// Raw is the published document as JSON, so a later build with a different
	// parser can read a cache this one wrote. The encoder's indentation, not
	// the publisher's.
	Raw []byte
}

// Load reads the cache, reporting separately whether there was one.
//
// Three answers, not two. No file is the ordinary case on a fresh install and
// is not an error. A file this build cannot read is present and broken, and the
// caller is told both: it should fall back to the built-in catalogue rather
// than fail, but a cache that cannot be read is worth saying out loud instead
// of silently behaving like an empty one.
func (c *Cache) Load() (Entry, bool, error) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return Entry{}, false, nil
		}
		return Entry{}, true, domain.Errorf(domain.CodeInternal,
			"无法读取预设缓存 %s", c.path).Wrap(err)
	}

	var wrapper cached
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return Entry{}, true, domain.Errorf(domain.CodeProtocolInvalid,
			"预设缓存内容无法解析").Wrap(err)
	}
	if len(wrapper.Payload) == 0 {
		return Entry{}, true, domain.Errorf(domain.CodeProtocolInvalid,
			"预设缓存里没有 payload")
	}

	catalogue, err := Parse(wrapper.Payload)
	if err != nil {
		return Entry{}, true, err
	}
	return Entry{
		Catalogue: catalogue,
		SourceURL: wrapper.SourceURL,
		CachedAt:  time.Unix(wrapper.CachedAt, 0).UTC(),
		Raw:       append([]byte(nil), wrapper.Payload...),
	}, true, nil
}

// Save writes a payload that has already been validated.
//
// Already validated, and that ordering is the point. The baseline parses before
// it writes for a reason it records in a comment: a remote publication with an
// illegal schema_version written to the cache poisons it, and every later read
// then fails from the bad cache -- taking the built-in fallback down with it,
// because the same code path loads both. So Save takes the raw bytes and the
// catalogue they parsed into, and there is no way to call it with bytes nobody
// has read.
func (c *Cache) Save(raw []byte, catalogue Catalogue, sourceURL string,
	at time.Time) error {

	if len(raw) == 0 {
		return domain.Errorf(domain.CodeInvalidArgument,
			"不写入空的预设缓存")
	}
	if catalogue.SchemaVersion != SchemaVersion {
		// The caller passed a catalogue it did not parse from these bytes.
		return domain.Errorf(domain.CodeInternal,
			"预设缓存只接受已经解析过的内容")
	}

	document, err := json.MarshalIndent(cached{
		CachedAt:  at.Unix(),
		SourceURL: sourceURL,
		Payload:   json.RawMessage(raw),
	}, "", "  ")
	if err != nil {
		return domain.Errorf(domain.CodeInternal, "无法编码预设缓存").Wrap(err)
	}
	return writeAtomic(c.path, append(document, '\n'))
}

// Clear removes the cache. Absent is success: that is the state it asks for.
func (c *Cache) Clear() error {
	if err := os.Remove(c.path); err != nil && !os.IsNotExist(err) {
		return domain.Errorf(domain.CodeInternal,
			"无法删除预设缓存 %s", c.path).Wrap(err)
	}
	return nil
}

// writeAtomic writes a file that a reader never sees half of.
//
// Temp, fsync, rename, sync the directory. A cache written without the fsync
// would be a cache that is present but truncated after the power cut it exists
// to survive -- and a truncated cache is worse than none, because it is read
// and fails to parse where an absent one falls straight through to the
// built-in catalogue.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, DirMode); err != nil {
		return domain.Errorf(domain.CodeInternal,
			"无法创建目录 %s", dir).Wrap(err)
	}

	temp, err := os.CreateTemp(dir, ".presets-*")
	if err != nil {
		return domain.Errorf(domain.CodeInternal,
			"无法在 %s 创建临时文件", dir).Wrap(err)
	}
	name := temp.Name()
	committed := false
	defer func() {
		if !committed {
			temp.Close()
			os.Remove(name)
		}
	}()

	if err := temp.Chmod(FileMode); err != nil {
		return domain.Errorf(domain.CodeInternal, "无法设置文件权限").Wrap(err)
	}
	if _, err := temp.Write(data); err != nil {
		return domain.Errorf(domain.CodeInternal, "写入失败").Wrap(err)
	}
	if err := temp.Sync(); err != nil {
		return domain.Errorf(domain.CodeInternal, "未能写入磁盘").Wrap(err)
	}
	if err := temp.Close(); err != nil {
		return domain.Errorf(domain.CodeInternal, "未能写入磁盘").Wrap(err)
	}
	if err := os.Rename(name, path); err != nil {
		return domain.Errorf(domain.CodeInternal, "提交失败").Wrap(err)
	}
	committed = true
	return syncDir(dir)
}
