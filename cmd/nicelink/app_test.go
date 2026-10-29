package main

import (
	"path/filepath"
	"testing"
)

// --- dataDir / dbPath / secretPath ---

func TestDataDir_DefaultsToWorkingDir(t *testing.T) {
	// DATA_DIR unset: paths resolve relative to the working directory, so
	// existing local behavior (files next to the binary) is unchanged.
	if got := dataDir(); got != "." {
		t.Fatalf("expected dataDir %q, got %q", ".", got)
	}
	if got := dbPath(); got != dbName {
		t.Fatalf("expected dbPath %q, got %q", dbName, got)
	}
	if got := secretPath(); got != secretName {
		t.Fatalf("expected secretPath %q, got %q", secretName, got)
	}
}

func TestDataDir_EnvOverride(t *testing.T) {
	// DATA_DIR set (e.g. "/data" on Fly.io, where a persistent volume is
	// mounted): both the database and the server secret must live there.
	t.Setenv("DATA_DIR", "/data")
	if got := dataDir(); got != "/data" {
		t.Fatalf("expected dataDir %q, got %q", "/data", got)
	}
	if got, want := dbPath(), filepath.Join("/data", dbName); got != want {
		t.Fatalf("expected dbPath %q, got %q", want, got)
	}
	if got, want := secretPath(), filepath.Join("/data", secretName); got != want {
		t.Fatalf("expected secretPath %q, got %q", want, got)
	}
}
