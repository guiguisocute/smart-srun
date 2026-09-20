package update

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalStagingConsumesOnlyVerifiedUploadFiles(t *testing.T) {
	w, task, _, _, _ := workerFixture(t, "split", true)
	inputs := LocalInputs(w.Paths, Candidate{Plan: task.Plan, Recovery: task.Recovery})
	for _, input := range inputs {
		if err := privateDir(filepath.Dir(input.Path)); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(input.Path, []byte(input.Asset.PackageVersion), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	foreign := filepath.Join(filepath.Dir(inputs[0].Path), "keep-me.apk")
	if err := os.WriteFile(foreign, []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := stageLocal(w.Paths, task); err != nil {
		t.Fatal(err)
	}
	for _, input := range inputs {
		if _, err := os.Lstat(input.Path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("upload duplicates retained: %v", err)
		}
	}
	for _, file := range append(packageFiles(w.Paths.Temporary(task.JobID), task.Plan.Assets), packageFiles(w.Paths.Backup(task.JobID), task.Recovery.Assets)...) {
		if err := VerifyFile(file); err != nil {
			t.Fatalf("verified staging lost: %v", err)
		}
	}
	if data, err := os.ReadFile(foreign); err != nil || string(data) != "unrelated" {
		t.Fatal("unrelated inbox content deleted")
	}
}

func TestWorkerCopyLivesUntilSuccessfulCompletion(t *testing.T) {
	for _, fail := range []bool{false, true} {
		w, _, device, _, _ := workerFixture(t, "bundle", true)
		if fail {
			device.install = func([]LocalPackage) error { return errors.New("synthetic installer failure") }
		}
		err := w.Run(t.Context())
		if (err != nil) != fail {
			t.Fatalf("unexpected result: %v", err)
		}
		_, statErr := os.Lstat(w.Paths.Worker())
		if fail && statErr != nil || !fail && !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("wrong worker retention: failure=%v stat=%v", fail, statErr)
		}
	}
}

func TestCompletedWorkerCleanupRefusesAReplacement(t *testing.T) {
	w, _, _, _, _ := workerFixture(t, "bundle", true)
	before, err := os.Lstat(w.Paths.Worker())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.Paths.Worker(), []byte("new-task-worker"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := cleanupCompletedWorker(w.Paths, before); err == nil {
		t.Fatal("replacement worker was accepted for deletion")
	}
	if _, err := os.Stat(w.Paths.Worker()); err != nil {
		t.Fatal("replacement worker deleted")
	}
}
