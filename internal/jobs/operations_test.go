package jobs

import (
	"path/filepath"
	"testing"
)

func TestOperationMigrationAndOwnership(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "jobs.db")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	j := Job{ID: NewID(), Target: "com.example.app", Source: "installed"}
	if err = s.Enqueue(&j, 20); err != nil {
		t.Fatal(err)
	}
	// Model a v1 database: existing jobs survive the additive v2 migration.
	if err = s.db.Migrator().DropTable(&Operation{}); err != nil {
		t.Fatal(err)
	}
	if err = s.db.Exec("PRAGMA user_version = 1").Error; err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.StartOperation(j.ID); err != ErrConflict {
		t.Fatal("unclaimed job acquired operation", err)
	}
	if _, err = s.Claim(); err != nil {
		t.Fatal(err)
	}
	first, err := s.StartOperation(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.StartOperation(j.ID)
	if err != nil || second == first {
		t.Fatal(second, err)
	}
	s.Close()
	s, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ops, err := s.Operations(j.ID)
	if err != nil || len(ops) != 2 {
		t.Fatal(ops, err)
	}
	if err = s.Recover(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartOperation(j.ID); err != ErrConflict {
		t.Fatal("quarantined job acquired operation", err)
	}
}

func TestRecoveryPreservesCleanupIntent(t *testing.T) {
	for _, desired := range []State{Completed, Failed, Cancelled} {
		t.Run(string(desired), func(t *testing.T) {
			s := openTest(t)
			j := Job{ID: NewID(), Target: "com.example.app", Source: "installed"}
			if err := s.Enqueue(&j, 20); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Claim(); err != nil {
				t.Fatal(err)
			}
			if _, err := s.BeginCleanup(j.ID, desired, "", ""); err != nil {
				t.Fatal(err)
			}
			if err := s.Recover(); err != nil {
				t.Fatal(err)
			}
			got, err := s.Get(j.ID)
			if err != nil || got.PendingState != desired || got.State != Cleaning {
				t.Fatal(got, err)
			}
		})
	}
}
