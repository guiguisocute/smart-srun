package openwrt

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/matthewlu070111/smart-srun/core/internal/domain"
)

// installTools puts real executable files on a search path of its own, so
// detection is answered by the filesystem rather than by a list of names.
func installTools(t *testing.T, names ...string) Runner {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("install %s: %v", name, err)
		}
	}
	return Runner{SearchPath: []string{dir}}
}

// The package manager is whichever one is actually installed and actually
// answers.
//
// Kwrt calls itself 25.12-SNAPSHOT and ships opkg; official 25.12 ships apk.
// Deciding from the release string would install with the wrong tool on every
// Kwrt device, and that is not a recoverable mistake. This is the real Kwrt
// case: a 25.12 release string with opkg.
func TestThePackageManagerIsDetectedFromTheBinaryNotTheRelease(t *testing.T) {
	// The release file from the same device, so the trap is concrete: this
	// firmware calls itself 25.12, and official 25.12 uses apk.
	release := string(testdata(t, "kwrt-25.12/openwrt_release"))
	if !strings.Contains(release, "25.12") {
		t.Fatalf("the fixture no longer says 25.12, so this proves nothing:\n%s",
			release)
	}

	runner := &recordingRunner{missing: []string{"apk"}}
	runner.respond(testdata(t, "kwrt-25.12/opkg-print-architecture.txt"),
		"opkg", "print-architecture")

	capabilities := Detect(t.Context(), runner)
	if capabilities.PackageManager != PackageManagerOpkg {
		t.Errorf("PackageManager = %q, want opkg on a firmware that ships it "+
			"regardless of its version string", capabilities.PackageManager)
	}
	if capabilities.Has(ToolAPK) {
		t.Error("apk was reported as present")
	}

	// And nothing was read to reach that answer except the binaries.
	for _, call := range runner.calls {
		for _, argument := range call {
			if strings.Contains(argument, "openwrt_release") {
				t.Errorf("detection read the release file: %v", call)
			}
		}
	}
}

// The architectures come from the tool and are ordered by the priority it
// reports, so the specific one is offered before the wildcard.
func TestArchitecturesAreOrderedByTheReportedPriority(t *testing.T) {
	runner := &recordingRunner{missing: []string{"apk"}}
	runner.respond(testdata(t, "kwrt-25.12/opkg-print-architecture.txt"),
		"opkg", "print-architecture")

	capabilities := Detect(t.Context(), runner)
	want := []string{"aarch64_cortex-a53", "all", "noarch"}
	if !slices.Equal(capabilities.PackageArchitectures, want) {
		t.Errorf("architectures = %v, want %v; `all` is valid for a LuCI file "+
			"package and never for the compiled core",
			capabilities.PackageArchitectures, want)
	}
}

// Ties keep the order the tool printed, so the answer is the same every run.
func TestEqualPrioritiesKeepTheReportedOrder(t *testing.T) {
	runner := &recordingRunner{missing: []string{"apk"}}
	runner.respond([]byte("arch all 1\narch noarch 1\narch mips_24kc 10\n"),
		"opkg", "print-architecture")

	for range 10 {
		capabilities := Detect(t.Context(), runner)
		want := []string{"mips_24kc", "all", "noarch"}
		if !slices.Equal(capabilities.PackageArchitectures, want) {
			t.Fatalf("architectures = %v, want %v",
				capabilities.PackageArchitectures, want)
		}
	}
}

// A binary that is present but cannot answer is not a working package manager.
// Reporting it as one would send the updater to a tool that fails at install
// time instead of at detection time.
func TestAPackageManagerThatCannotAnswerIsNotDetected(t *testing.T) {
	runner := &recordingRunner{missing: []string{"apk"}}
	runner.fail(&ExitError{Program: "opkg", Code: 127}, "opkg", "print-architecture")

	capabilities := Detect(t.Context(), runner)
	if capabilities.PackageManager != PackageManagerNone {
		t.Errorf("PackageManager = %q, want none", capabilities.PackageManager)
	}
	if !capabilities.Has(ToolOpkg) {
		t.Error("the binary is on disk and should still be reported as present")
	}
}

// Output with no architecture lines is the same kind of non-answer.
func TestEmptyArchitectureOutputIsNotAWorkingPackageManager(t *testing.T) {
	runner := &recordingRunner{missing: []string{"apk"}}
	runner.respond([]byte("\n\nnot an arch line\n"), "opkg", "print-architecture")

	if manager := Detect(t.Context(), runner).PackageManager; manager != PackageManagerNone {
		t.Errorf("PackageManager = %q, want none", manager)
	}
}

// apk is detected the same way, by asking it something.
func TestAPKIsDetectedWhenItIsTheOneThatAnswers(t *testing.T) {
	runner := &recordingRunner{missing: []string{"opkg"}}
	runner.respond([]byte("apk-tools 3.0.0\n"), "apk", "--version")

	capabilities := Detect(t.Context(), runner)
	if capabilities.PackageManager != PackageManagerAPK {
		t.Errorf("PackageManager = %q, want apk", capabilities.PackageManager)
	}
}

// Detection is against the filesystem, with real files.
func TestToolsAreFoundOnTheSearchPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executability is decided differently here")
	}
	runner := installTools(t, "uci", "ubus", "opkg")

	capabilities := Detect(t.Context(), runner)
	if !capabilities.Has(ToolUCI) || !capabilities.Has(ToolUbus) {
		t.Error("a tool that is on disk was not found")
	}
	if capabilities.Has(ToolIwinfo) || capabilities.Has(ToolAPK) {
		t.Error("a tool that is not on disk was reported as present")
	}
	// The path has to be the file that was found, not the name it was looked
	// up by: two directories on the search path can hold the same name, and the
	// one that will run is the one detection settled on.
	path, ok := capabilities.Path(ToolUCI)
	if !ok || filepath.Dir(path) != runner.SearchPath[0] ||
		filepath.Base(path) != "uci" {
		t.Errorf("path = %q, ok = %v, want the file in %q",
			path, ok, runner.SearchPath[0])
	}
}

// The default search path is the OpenWrt layout, and a Runner told nothing else
// uses it.
//
// procd starts the daemon with an environment of its own, so resolution cannot
// depend on an inherited PATH. These directories are where a real device keeps
// these tools: /sbin/uci, /bin/ubus, /usr/bin/iwinfo, /sbin/ip. Trimming the
// list to the two obvious ones would leave uci and ip unfindable.
func TestTheDefaultSearchPathCoversWhereOpenWrtKeepsItsTools(t *testing.T) {
	if got := (Runner{}).searchPath(); !slices.Equal(got, DefaultSearchPath) {
		t.Errorf("searchPath() = %v, want the default %v", got, DefaultSearchPath)
	}
	for _, required := range []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
		if !slices.Contains(DefaultSearchPath, required) {
			t.Errorf("%s is missing from the default search path", required)
		}
	}
}

// A missing tool is a capability that this firmware does not have. The M04 card
// requires that answer specifically: it tells the interface to say the feature
// is unavailable rather than to offer a retry that cannot succeed.
func TestRequiringAMissingToolReportsUnsupportedCapability(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executability is decided differently here")
	}
	capabilities := Detect(t.Context(), installTools(t, "uci", "ubus"))

	if err := capabilities.Require(ToolUCI, ToolUbus); err != nil {
		t.Errorf("Require on present tools: %v", err)
	}

	err := capabilities.Require(ToolUCI, ToolIwinfo)
	if err == nil {
		t.Fatal("Require accepted a tool that is not installed")
	}
	if code := codeOf(t, err); code != domain.CodeUnsupportedCapability {
		t.Errorf("code = %s, want UnsupportedCapability", code)
	}
	if got := err.Error(); !strings.Contains(got, "iwinfo") {
		t.Errorf("the message does not name the missing tool: %s", got)
	}
}
