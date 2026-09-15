package wireless

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/matthewlu070111/smart-srun/core/internal/domain"
	"github.com/matthewlu070111/smart-srun/core/internal/openwrt"
)

// commandRunner is the part of openwrt.Runner this store uses.
//
// Consumer-defined, like the adapter's own: it lets the composition -- which
// flags, in which order, and what is done with a failure -- be driven without a
// router, while the guarantees about running a process stay where they are
// tested against real ones.
type commandRunner interface {
	Run(ctx context.Context, program string, args ...string) (openwrt.Result, error)
}

// SystemConfigDir and SystemDeltaDir are uci's own defaults.
//
// Named and passed explicitly rather than left to uci, because the staging
// directory has to be passed explicitly anyway and a store that sometimes says
// where it is writing is a store that can be pointed somewhere unintended by
// forgetting a flag.
const (
	SystemConfigDir = "/etc/config"
	SystemDeltaDir  = "/tmp/.uci"
)

// networkInit is the service asked to make the new configuration live.
//
// `/etc/init.d/network reload` rather than `wifi reload`: the second restarts
// the radios and leaves netifd's view of the interfaces as it was, so a station
// that moved keeps the layer-3 configuration of the network it left. This
// project has been bitten by exactly that -- a stale route surviving a wireless
// change, and every subsequent request leaving through it.
const networkInit = "/etc/init.d/network"

// UCIStore is the Store over a router's real uci.
//
// It follows the baseline's sequence, which is the one that has run on real
// hardware: write the candidate into an isolated directory, check the live file
// has not moved underneath, then publish. The isolation is what makes a failure
// anywhere before the publish leave the running configuration untouched, and
// the check is what makes publishing a whole file equivalent to applying only
// this transaction's options.
type UCIStore struct {
	runner commandRunner
	// staging is this store's own uci root: a copy of the package being
	// changed, plus a delta directory, neither of them shared with the system.
	staging string
	// configDir and deltaDir are the system's. Fields rather than constants so
	// the whole store can be pointed at a temporary tree and tested against a
	// real uci binary.
	configDir string
	deltaDir  string

	// staged is the live file as it was when the candidate was built, per
	// package. Commit compares against it, and its absence is how "commit
	// without stage" is refused rather than guessed at.
	staged map[string][]byte
}

// StoreOptions configures a UCIStore. Only Staging is required.
type StoreOptions struct {
	Staging   string
	ConfigDir string
	DeltaDir  string
}

// NewUCIStore builds a store. It creates nothing yet: a store that made
// directories at construction would leave them behind on a router that never
// changes its wireless configuration, which is most of them.
func NewUCIStore(runner commandRunner, options StoreOptions) (*UCIStore, error) {
	if runner == nil {
		return nil, domain.Errorf(domain.CodeInvalidArgument,
			"无线配置存储需要一个命令执行器")
	}
	if options.Staging == "" {
		return nil, domain.Errorf(domain.CodeInvalidArgument,
			"无线配置存储需要一个独立的暂存目录")
	}
	store := &UCIStore{
		runner:    runner,
		staging:   options.Staging,
		configDir: options.ConfigDir,
		deltaDir:  options.DeltaDir,
		staged:    map[string][]byte{},
	}
	if store.configDir == "" {
		store.configDir = SystemConfigDir
	}
	if store.deltaDir == "" {
		store.deltaDir = SystemDeltaDir
	}
	return store, nil
}

// Read returns the current value of each key.
func (s *UCIStore) Read(ctx context.Context, pkg string, keys []Key) (map[Key]Value, error) {
	if err := checkName(pkg, "配置包"); err != nil {
		return nil, err
	}
	config, err := s.show(ctx, s.configDir, s.deltaDir, pkg)
	if err != nil {
		return nil, err
	}

	values := make(map[Key]Value, len(keys))
	for _, key := range keys {
		if err := checkKey(key); err != nil {
			return nil, err
		}
		section, ok := config.Section(key.Section)
		if !ok {
			values[key] = Value{}
			continue
		}
		option, present := section.Lookup(key.Option)
		if !present {
			values[key] = Value{}
			continue
		}
		// A list is not a string. Reading one as its first item would record a
		// "before" value that cannot restore what was there, and the rollback
		// would then quietly replace a list with a scalar -- so this refuses
		// rather than returns something that looks usable.
		if option.IsList {
			return nil, domain.Errorf(domain.CodeConflict,
				"%s.%s 是列表项，本次改动不处理列表", key.Section, key.Option)
		}
		values[key] = Value{Text: option.Text, Present: true}
	}
	return values, nil
}

// Stage builds the candidate in the isolated directory.
//
// The live file is copied in and remembered, the options are written against
// the copy, and uci commits into the copy. Nothing the system reads has been
// touched when this returns.
func (s *UCIStore) Stage(ctx context.Context, pkg string, changes []Change) error {
	if err := checkName(pkg, "配置包"); err != nil {
		return err
	}
	if len(changes) == 0 {
		return domain.Errorf(domain.CodeInvalidArgument,
			"没有要暂存的改动")
	}

	delta := filepath.Join(s.staging, "delta")
	if err := os.MkdirAll(delta, DirMode); err != nil {
		return domain.Errorf(domain.CodeInternal,
			"无法创建暂存目录 %s", delta).Wrap(err)
	}
	// A delta left by an interrupted attempt would be replayed by the commit
	// below, applying options this transaction never planned.
	if err := clearDir(delta); err != nil {
		return err
	}

	live := filepath.Join(s.configDir, pkg)
	before, err := os.ReadFile(live)
	if err != nil {
		return domain.Errorf(domain.CodeNotFound,
			"无法读取 %s", live).Wrap(err)
	}
	if err := writeFilePrivate(filepath.Join(s.staging, pkg), before); err != nil {
		return err
	}

	for _, change := range changes {
		if err := checkKey(change.Key); err != nil {
			return err
		}
		name := pkg + "." + change.Key.Section + "." + change.Key.Option
		args := []string{"-q", "-c", s.staging, "-t", delta, "set", name + "=" + change.Text}
		if change.Delete {
			args = []string{"-q", "-c", s.staging, "-t", delta, "delete", name}
		}
		if _, err := s.runner.Run(ctx, "uci", args...); err != nil {
			// The value is on that command line. Reporting the key alone says
			// enough to act on without putting a passphrase in a log.
			return domain.Errorf(domain.CodeInternal,
				"暂存 %s 失败", name).Wrap(err)
		}
	}

	if _, err := s.runner.Run(ctx, "uci", "-q", "-c", s.staging, "-t", delta,
		"commit", pkg); err != nil {
		return domain.Errorf(domain.CodeInternal,
			"无法生成 %s 的候选配置", pkg).Wrap(err)
	}
	s.staged[pkg] = before
	return nil
}

// Commit publishes the candidate, if the live file is still the one it was
// built from.
//
// Refusing on a change underneath is spec 04's "校验原配置仍未变": somebody
// else's edit between the copy and the publish would be overwritten by a file
// that never contained it.
func (s *UCIStore) Commit(ctx context.Context, pkg string) error {
	if err := checkName(pkg, "配置包"); err != nil {
		return err
	}
	before, staged := s.staged[pkg]
	if !staged {
		// Not a device state a caller can recover from by retrying: it is this
		// program calling its own store out of order.
		return domain.Errorf(domain.CodeInternal,
			"%s 还没有候选配置可以应用", pkg)
	}

	live := filepath.Join(s.configDir, pkg)
	current, err := os.ReadFile(live)
	if err != nil {
		return domain.Errorf(domain.CodeInternal,
			"无法读取 %s", live).Wrap(err)
	}
	if !bytes.Equal(current, before) {
		return domain.Errorf(domain.CodeConflict,
			"%s 在本次改动期间被其它操作修改，已放弃应用", pkg)
	}

	candidate, err := os.ReadFile(filepath.Join(s.staging, pkg))
	if err != nil {
		return domain.Errorf(domain.CodeInternal,
			"无法读取 %s 的候选配置", pkg).Wrap(err)
	}
	delete(s.staged, pkg)
	if bytes.Equal(candidate, before) {
		// uci accepted every option and none of them changed anything. Writing
		// the identical file would still bump the mtime and make a later "was
		// this touched" answer wrongly.
		return nil
	}
	return replaceFile(live, candidate)
}

// PendingChanges reports the system's own uncommitted changes.
//
// The system's delta, not this store's: the question is whether somebody else
// is mid-edit, and this transaction's own staging is by construction not.
func (s *UCIStore) PendingChanges(ctx context.Context, pkg string) ([]string, error) {
	if err := checkName(pkg, "配置包"); err != nil {
		return nil, err
	}
	result, err := s.runner.Run(ctx, "uci", "-c", s.configDir, "-t", s.deltaDir,
		"changes", pkg)
	if err != nil {
		return nil, missingPackage(pkg, err)
	}
	if result.StdoutTruncated {
		return nil, domain.Errorf(domain.CodeInternal,
			"%s 的未提交改动过多，无法完整读取", pkg)
	}
	changes, err := openwrt.ParseUCIChanges(result.Stdout)
	if err != nil {
		return nil, err
	}
	// Keys only. A pending change's value can be a passphrase somebody typed
	// into the wireless page, and this list exists to be shown in a refusal.
	names := make([]string, 0, len(changes))
	for _, change := range changes {
		names = append(names, string(change.Kind)+" "+change.Key)
	}
	return names, nil
}

// Reload makes the committed configuration take effect.
func (s *UCIStore) Reload(ctx context.Context) error {
	if _, err := s.runner.Run(ctx, networkInit, "reload"); err != nil {
		return domain.Errorf(domain.CodeInternal,
			"重载网络配置失败").Wrap(err)
	}
	return nil
}

func (s *UCIStore) show(ctx context.Context, configDir, deltaDir, pkg string) (
	openwrt.UCIConfig, error) {

	result, err := s.runner.Run(ctx, "uci", "-c", configDir, "-t", deltaDir,
		"show", pkg)
	if err != nil {
		return openwrt.UCIConfig{}, missingPackage(pkg, err)
	}
	if result.StdoutTruncated {
		return openwrt.UCIConfig{}, domain.Errorf(domain.CodeInternal,
			"配置 %s 过长，已截断", pkg)
	}
	return openwrt.ParseUCIShow(pkg, result.Stdout)
}

// missingPackage gives "there is no such configuration" its own code.
//
// A router with no radio has no /etc/config/wireless, and uci exits 1. That is
// a system with nothing to change, not a tool that failed.
func missingPackage(pkg string, err error) error {
	var exit *openwrt.ExitError
	if errors.As(err, &exit) && exit.Code == 1 {
		return domain.Errorf(domain.CodeNotFound,
			"系统上没有 %s 配置", pkg).Wrap(err)
	}
	return err
}

// writeFilePrivate writes the staging copy. It does not fsync: nothing outside
// this process reads it, and it is rebuilt from the live file every time.
func writeFilePrivate(path string, data []byte) error {
	if err := os.WriteFile(path, data, FileMode); err != nil {
		return domain.Errorf(domain.CodeInternal,
			"无法写入 %s", filepath.Base(path)).Wrap(err)
	}
	// WriteFile only applies the mode when it creates the file, and the copy
	// from a previous attempt is still there.
	if err := os.Chmod(path, FileMode); err != nil {
		return domain.Errorf(domain.CodeInternal,
			"无法设置 %s 的权限", filepath.Base(path)).Wrap(err)
	}
	return nil
}

// replaceFile publishes the candidate over the live file, atomically and
// durably, keeping the mode the live file already had.
//
// Durably because this one is the change: a rename that reached the directory
// but not the flash would leave a router that reboots into the old wireless
// configuration while the journal says the new one is live and awaiting
// confirmation -- the one state the recovery cannot tell from a real one.
func replaceFile(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return domain.Errorf(domain.CodeInternal,
			"无法读取 %s 的属性", path).Wrap(err)
	}

	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".smart-srun-*")
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

	if err := temp.Chmod(info.Mode().Perm()); err != nil {
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

func checkName(name, what string) error {
	if !openwrt.IsLogicalInterfaceName(name) {
		return domain.Errorf(domain.CodeInvalidArgument,
			"%q 不是有效的%s名", name, what)
	}
	return nil
}

// checkKey refuses anything that would not survive being spliced into
// `package.section.option`.
//
// uci's own names allow letters, digits and underscores and nothing else, so a
// section called "radio0.key" is not a name uci could have produced -- it is a
// value that arrived from somewhere it should not have, and turning it into a
// different option than intended is exactly the failure worth refusing.
func checkKey(key Key) error {
	if err := checkName(key.Section, "配置节"); err != nil {
		return err
	}
	return checkName(key.Option, "配置项")
}

// clearDir removes a directory's entries without removing the directory, so
// its mode survives.
func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return domain.Errorf(domain.CodeInternal,
			"无法读取暂存目录 %s", dir).Wrap(err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return domain.Errorf(domain.CodeInternal,
				"无法清理暂存目录 %s", dir).Wrap(err)
		}
	}
	return nil
}
