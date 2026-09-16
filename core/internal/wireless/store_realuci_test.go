//go:build unix

package wireless

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matthewlu070111/smart-srun/core/internal/openwrt"
)

// The store against the real uci, on a machine that has one.
//
// Everything else in this package drives fakeUCI, which is an emulation written
// from the baseline's usage and the man page. It was wrong in five places when
// a real uci was finally asked, and one of those was a production defect. This
// closes the loop: the same store, the same sequence, the real binary.
//
// It writes only inside t.TempDir(), so it is safe on a router somebody is
// using -- /etc/config is never named. What it needs is a uci, which the
// development host does not have; it runs on the OpenWrt device, and the
// evidence records whether it ran or skipped. A gate that quietly skips is
// worse than no gate, so nothing cites this as passing without that line.
func TestTheStoreAgainstRealUCI(t *testing.T) {
	runner := openwrt.Runner{}
	if _, err := runner.Resolve("uci"); err != nil {
		t.Skip("no uci on this host -- this test is for the OpenWrt device")
	}

	root := t.TempDir()
	config := filepath.Join(root, "config")
	delta := filepath.Join(root, "delta")
	staging := filepath.Join(root, "staging")
	for _, dir := range []string{config, delta} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}

	// Real /etc/config syntax, not the `uci show` form the fake uses. The store
	// treats the package file as opaque bytes either way; uci does not.
	live := filepath.Join(config, "wireless")
	original := "" +
		"config wifi-iface 'ap0'\n" +
		"\toption device 'radio0'\n" +
		"\toption mode 'ap'\n" +
		"\toption ssid 'HomeNet'\n" +
		"\toption key 'home-secret'\n" +
		"\n" +
		"config wifi-iface 'sta0'\n" +
		"\toption device 'radio0'\n" +
		"\toption mode 'sta'\n" +
		"\toption ssid 'old-network'\n" +
		"\toption encryption 'none'\n"
	if err := os.WriteFile(live, []byte(original), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	store, err := NewUCIStore(runner, StoreOptions{
		Staging: staging, ConfigDir: config, DeltaDir: delta})
	if err != nil {
		t.Fatalf("NewUCIStore: %v", err)
	}

	// Nothing staged anywhere, so nothing is pending.
	if pending, err := store.PendingChanges(t.Context(), "wireless"); err != nil {
		t.Fatalf("PendingChanges: %v", err)
	} else if len(pending) != 0 {
		t.Fatalf("pending = %v on a clean tree", pending)
	}

	// Reading: present, absent, and the section itself.
	values, err := store.Read(t.Context(), "wireless", []Key{
		ssid, key, {Section: "sta0"},
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if want := (Value{Text: "old-network", Present: true}); values[ssid] != want {
		t.Errorf("ssid = %+v, want %+v", values[ssid], want)
	}
	if values[key].Present {
		t.Errorf("key = %+v; the section has none", values[key])
	}
	if want := (Value{Text: "wifi-iface", Present: true}); values[Key{Section: "sta0"}] != want {
		t.Errorf("section = %+v, want its type", values[Key{Section: "sta0"}])
	}

	// A campus change, including the deletion of an option that is not there --
	// the shape every open network produces, and the one that made real uci
	// exit 1 where the fake had said fine.
	changes := []Change{
		{Key: ssid, Text: "jxnu_stu"},
		{Key: enc, Text: "psk2"},
		{Key: key, Text: passphrase},
		{Key: Key{Section: "sta0", Option: "bssid"}, Delete: true},
	}
	if err := store.Stage(t.Context(), "wireless", changes); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if got, err := os.ReadFile(live); err != nil || string(got) != original {
		t.Fatalf("staging touched the live file:\n%s", got)
	}

	if err := store.Commit(t.Context(), "wireless"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	published, err := os.ReadFile(live)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, want := range []string{"jxnu_stu", "psk2", passphrase} {
		if !strings.Contains(string(published), want) {
			t.Errorf("published file is missing %q:\n%s", want, published)
		}
	}
	// The household's access point is untouched, which is the promise spec 04
	// makes and the one worth checking against a real rewrite of the file.
	if !strings.Contains(string(published), "HomeNet") ||
		!strings.Contains(string(published), "home-secret") {
		t.Errorf("the home access point did not survive:\n%s", published)
	}

	// And the mode it had. uci wrote this file; the store published it.
	info, err := os.Stat(live)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %04o, want the 0600 it started with", got)
	}

	// Creating a section, and undoing it, through the real binary.
	station := Key{Section: "jxnu_sta_probe"}
	if err := store.Stage(t.Context(), "wireless", []Change{
		{Key: station, Text: "wifi-iface"},
		{Key: Key{Section: station.Section, Option: "mode"}, Text: "sta"},
	}); err != nil {
		t.Fatalf("Stage the section: %v", err)
	}
	if err := store.Commit(t.Context(), "wireless"); err != nil {
		t.Fatalf("Commit the section: %v", err)
	}
	if body, _ := os.ReadFile(live); !strings.Contains(string(body), "jxnu_sta_probe") {
		t.Fatalf("the section was not created:\n%s", body)
	}

	if err := store.Stage(t.Context(), "wireless", []Change{
		{Key: station, Delete: true},
	}); err != nil {
		t.Fatalf("Stage the removal: %v", err)
	}
	if err := store.Commit(t.Context(), "wireless"); err != nil {
		t.Fatalf("Commit the removal: %v", err)
	}
	body, _ := os.ReadFile(live)
	if strings.Contains(string(body), "jxnu_sta_probe") {
		t.Errorf("the section survived its removal:\n%s", body)
	}
	if !strings.Contains(string(body), "jxnu_stu") {
		t.Errorf("removing one section took another's options with it:\n%s", body)
	}
}

// And against a copy of the device's own wireless configuration.
//
// The test above uses a fixture, which means it meets the sections and values
// somebody wrote for it. This one meets a real router's: whatever list options,
// section names, section types and hand-edits are actually there. It is the
// difference between "the store works on a file I designed" and "the store
// works on the file it will be pointed at".
//
// Still a copy, in t.TempDir(). The live file is opened read-only and never
// named as the store's ConfigDir, so this is safe on a router in use -- what it
// cannot cover is the reload, which is the part that needs a window.
//
// Nothing here prints the file. It holds the household's wireless passphrases.
func TestTheStoreAgainstThisDevicesOwnConfiguration(t *testing.T) {
	runner := openwrt.Runner{}
	if _, err := runner.Resolve("uci"); err != nil {
		t.Skip("no uci on this host -- this test is for the OpenWrt device")
	}
	source := os.Getenv("SMARTSRUN_REAL_WIRELESS")
	if source == "" {
		t.Skip("set SMARTSRUN_REAL_WIRELESS=/etc/config/wireless to run this")
	}
	original, err := os.ReadFile(source)
	if err != nil {
		t.Skipf("cannot read %s: %v", source, err)
	}

	root := t.TempDir()
	config := filepath.Join(root, "config")
	delta := filepath.Join(root, "delta")
	for _, dir := range []string{config, delta} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	live := filepath.Join(config, "wireless")
	if err := os.WriteFile(live, original, 0o600); err != nil {
		t.Fatalf("copy: %v", err)
	}

	store, err := NewUCIStore(runner, StoreOptions{
		Staging: filepath.Join(root, "staging"), ConfigDir: config, DeltaDir: delta})
	if err != nil {
		t.Fatalf("NewUCIStore: %v", err)
	}

	// A section of our own, created from nothing, the way a first connection
	// does it -- and left disabled, which is what the plan writes before
	// anything is enabled.
	section := "smartsrun_gate_probe"
	changes := []Change{
		{Key: Key{Section: section}, Text: "wifi-iface"},
		{Key: Key{Section: section, Option: "mode"}, Text: "sta"},
		{Key: Key{Section: section, Option: "ssid"}, Text: "jxnu_stu"},
		{Key: Key{Section: section, Option: "disabled"}, Text: "1"},
		{Key: Key{Section: section, Option: "key"}, Delete: true},
	}

	before, err := store.Read(t.Context(), "wireless", keysOf(changes))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if before[Key{Section: section}].Present {
		t.Fatalf("%s already exists on this device; pick another name", section)
	}

	if err := store.Stage(t.Context(), "wireless", changes); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if err := store.Commit(t.Context(), "wireless"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	after, err := os.ReadFile(live)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(after), section) {
		t.Fatal("the section was not created in the device's own configuration")
	}

	// Every section that was there before is still there. Checked by name from
	// the parsed configuration rather than by diffing the bytes, because
	// committing makes uci rewrite the file in canonical form.
	kept, err := openwrt.ParseUCIShow("wireless", mustShow(t, runner, config, delta))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, name := range sectionNames(t, string(original)) {
		if _, ok := kept.Section(name); !ok {
			t.Errorf("section %s disappeared from this device's configuration", name)
		}
	}

	// And it comes back out, leaving what was there.
	if err := store.Stage(t.Context(), "wireless", []Change{
		{Key: Key{Section: section}, Delete: true}}); err != nil {
		t.Fatalf("Stage the removal: %v", err)
	}
	if err := store.Commit(t.Context(), "wireless"); err != nil {
		t.Fatalf("Commit the removal: %v", err)
	}
	final, err := os.ReadFile(live)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(final), section) {
		t.Error("the probe section survived its removal")
	}
	for _, name := range sectionNames(t, string(original)) {
		if !strings.Contains(string(final), name) {
			t.Errorf("section %s did not survive the removal", name)
		}
	}
}

func keysOf(changes []Change) []Key {
	keys := make([]Key, 0, len(changes))
	for _, change := range changes {
		keys = append(keys, change.Key)
	}
	return keys
}

func mustShow(t *testing.T, runner openwrt.Runner, config, delta string) []byte {
	t.Helper()
	result, err := runner.Run(t.Context(), "uci", "-c", config, "-t", delta,
		"show", "wireless")
	if err != nil {
		t.Fatalf("uci show: %v", err)
	}
	return result.Stdout
}

// sectionNames pulls `config <type> '<name>'` out of a real /etc/config file.
//
// Named sections only. An anonymous one has no name to check for, and this is
// asking "did the things that were there survive", not "is the file identical".
func sectionNames(t *testing.T, body string) []string {
	t.Helper()
	var names []string
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "config" {
			continue
		}
		names = append(names, strings.Trim(fields[2], "'\""))
	}
	if len(names) == 0 {
		t.Fatal("no named sections in the device configuration; nothing to check")
	}
	return names
}
