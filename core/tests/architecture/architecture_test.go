// Package architecture checks the dependency rules that keep the packages
// separable.
//
// These are asserted rather than documented because a wrong import compiles
// fine and is only noticed much later, when the package it corrupted can no
// longer be tested without a router attached.
package architecture

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const modulePath = "github.com/matthewlu070111/smart-srun/core"

// coreRoot is the module root, two levels up from tests/architecture.
func coreRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve core root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("core root %q has no go.mod: %v", root, err)
	}
	return root
}

// packageImports maps each package's import path to the set of paths it imports.
// Test files are excluded: a test may reach for anything it needs to build a
// fixture, and holding tests to the production dependency rules would only
// push fixtures into production code.
func packageImports(t *testing.T) map[string][]string {
	t.Helper()
	root := coreRoot(t)
	fileSet := token.NewFileSet()
	result := map[string][]string{}

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		importPath := modulePath
		if relative != "." {
			importPath += "/" + filepath.ToSlash(relative)
		}

		file, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imported := strings.Trim(spec.Path.Value, `"`)
			if !slices.Contains(result[importPath], imported) {
				result[importPath] = append(result[importPath], imported)
			}
		}
		if _, seen := result[importPath]; !seen {
			result[importPath] = nil
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("no packages found; the walk is looking in the wrong place")
	}
	return result
}

// domain is the vocabulary every other package shares. Giving it I/O would make
// every type that mentions it untestable without the thing it reached for.
func TestDomainHasNoIO(t *testing.T) {
	// "net" is forbidden but "net/netip" is not, and the difference is the
	// point: netip is value types with no dialing, listening or resolving in
	// it, so a Binding can name an address without the package that holds it
	// gaining the ability to open a socket.
	forbidden := []string{
		"os", "os/exec", "net", "net/http", "io/fs", "database/sql",
		"os/user", "syscall", "golang.org/x/sys/unix", "bufio",
	}
	imports := packageImports(t)[modulePath+"/internal/domain"]
	if imports == nil {
		t.Fatal("domain package not found")
	}
	for _, imported := range imports {
		if slices.Contains(forbidden, imported) {
			t.Errorf("domain imports %q; it must stay pure so every type that "+
				"mentions it can be tested without that dependency", imported)
		}
		if strings.HasPrefix(imported, modulePath) {
			t.Errorf("domain imports %q; the vocabulary package depends on "+
				"nothing inside the module", imported)
		}
	}
}

// The direction that matters: the layers that make decisions may use the layers
// that hold data, never the reverse. An adapter reaching back into the
// application is how a "small exception" becomes a cycle.
func TestDependencyDirection(t *testing.T) {
	rules := map[string][]string{
		"internal/domain": {"internal/config", "internal/protocol", "internal/control",
			"internal/cli", "internal/openwrt", "cmd"},
		"internal/protocol": {"internal/config", "internal/control", "internal/cli",
			"internal/openwrt", "cmd"},
		// The adapter is a leaf. It reads the router and speaks domain; it does
		// not read the user's configuration or answer RPCs. An adapter that
		// reaches back into the layers that use it is how a "small exception"
		// becomes a cycle, and it would also make every one of those layers
		// need a router to test.
		"internal/openwrt": {"internal/config", "internal/protocol",
			"internal/control", "internal/cli", "cmd"},
		"internal/config":  {"internal/control", "internal/cli", "internal/openwrt", "cmd"},
		"internal/control": {"internal/cli", "cmd"},
		"internal/cli":     {"cmd"},
	}

	imports := packageImports(t)
	for pkg, forbidden := range rules {
		full := modulePath + "/" + pkg
		for _, imported := range imports[full] {
			for _, banned := range forbidden {
				if strings.HasPrefix(imported, modulePath+"/"+banned) {
					t.Errorf("%s imports %s; dependencies point one way only",
						pkg, imported)
				}
			}
		}
	}
}

// The protocol layer is bytes in, bytes out. Spec 04 requires timestamps and
// callback names to arrive as arguments: a package that read the clock or the
// network could only be tested against a live gateway, which is exactly what
// fixed protocol vectors exist to avoid.
func TestProtocolReadsNothing(t *testing.T) {
	forbidden := []string{
		"net", "net/http", "net/url", "os", "os/exec", "time", "math/rand",
		"math/rand/v2", "crypto/rand", "io/ioutil", "bufio",
	}

	imports := packageImports(t)
	found := false
	for pkg, imported := range imports {
		if !strings.HasPrefix(pkg, modulePath+"/internal/protocol") {
			continue
		}
		found = true
		for _, name := range imported {
			if slices.Contains(forbidden, name) {
				t.Errorf("%s imports %q; the protocol layer takes its inputs as "+
					"arguments so its output is decided entirely by them", pkg, name)
			}
		}
	}
	if !found {
		t.Fatal("no protocol package found")
	}
}

// Every system command goes through the one runner.
//
// That runner is what makes a command cancellable, bounded in output and time,
// reaped along with its children, and free of a shell. A second place that
// starts a process gets none of it, and the way that happens is somebody
// needing one more query and reaching for exec.Command because it is three
// lines. Restricting the import to the file that owns the guarantees makes the
// shortcut visible instead of easy.
func TestOnlyTheRunnerStartsProcesses(t *testing.T) {
	// command.go declares the runner; the platform files carry the syscalls it
	// needs for process groups.
	allowed := map[string]bool{
		"command.go": true, "platform_unix.go": true, "platform_other.go": true,
	}

	root := filepath.Join(coreRoot(t), "internal", "openwrt")
	fileSet := token.NewFileSet()
	found := false

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read the adapter directory: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") {
			continue
		}
		found = true
		file, err := parser.ParseFile(fileSet, filepath.Join(root, name), nil,
			parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, spec := range file.Imports {
			imported := strings.Trim(spec.Path.Value, `"`)
			if (imported == "os/exec" || imported == "syscall") && !allowed[name] {
				t.Errorf("%s imports %q; system commands go through Runner, "+
					"which is what bounds their output and reaps their children",
					name, imported)
			}
		}
	}
	if !found {
		t.Fatal("no adapter sources found; the walk is looking in the wrong place")
	}
}

// cmd wires things together and exits. Business logic there would be reachable
// only by running the binary.
func TestCommandPackageOnlyWires(t *testing.T) {
	root := coreRoot(t)
	path := filepath.Join(root, "cmd", "srunnet", "main.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	lines := strings.Count(string(data), "\n")
	if lines > 120 {
		t.Errorf("cmd/srunnet/main.go is %d lines; it should construct "+
			"dependencies and exit, not carry logic", lines)
	}
}

// Third-party dependencies are a supply-chain decision, so each one gets
// recorded deliberately rather than arriving with a convenient helper.
func TestNoUnreviewedThirdPartyDependencies(t *testing.T) {
	// Reviewed and allowed. Spec 02 permits golang.org/x/sys/unix for Linux
	// syscalls and x/net/html plus its x/text charset support for portal
	// parsing. Nothing else may appear without being added here and to the
	// dependency record.
	allowed := []string{
		"golang.org/x/sys/unix",
		"golang.org/x/net/html",
		"golang.org/x/net/html/charset",
		"golang.org/x/text/encoding",
		"golang.org/x/text/encoding/htmlindex",
		"golang.org/x/text/transform",
	}

	for pkg, imports := range packageImports(t) {
		for _, imported := range imports {
			if strings.HasPrefix(imported, modulePath) {
				continue
			}
			// A standard library path has no dot in its first element.
			first, _, _ := strings.Cut(imported, "/")
			if !strings.Contains(first, ".") {
				continue
			}
			if !slices.Contains(allowed, imported) {
				t.Errorf("%s imports the unreviewed third-party package %q",
					pkg, imported)
			}
		}
	}
}

// A shared mutable bag is how ownership stops being traceable: any package can
// write any key, and nothing can be tested in isolation afterwards.
func TestNoGlobalServiceLocator(t *testing.T) {
	root := coreRoot(t)
	banned := []string{"ServiceLocator", "CoreAPI", "GlobalContext", "AppContext"}

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(data)
		for _, name := range banned {
			if strings.Contains(text, "type "+name) {
				relative, _ := filepath.Rel(root, path)
				t.Errorf("%s declares %s; dependencies are passed explicitly",
					filepath.ToSlash(relative), name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
