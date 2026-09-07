package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var ErrConflict = errors.New("job state conflict")
var ErrQueueFull = errors.New("queue full")

type Store struct{ db *gorm.DB }

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func Open(path string) (*Store, error) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Lstat(path + suffix)
		if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
			return nil, errors.New("database files must be private regular files")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}

	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String() + "?_busy_timeout=5000&_journal_mode=WAL&_synchronous=FULL&_foreign_keys=on"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, err
	}
	sql, err := db.DB()
	if err != nil {
		return nil, err
	}
	sql.SetMaxOpenConns(1)
	var version int
	if err = db.Raw("PRAGMA user_version").Scan(&version).Error; err != nil {
		sql.Close()
		return nil, err
	}
	if version > 1 {
		sql.Close()
		return nil, errors.New("database schema is newer than this binary")
	}
	if err = db.Transaction(func(tx *gorm.DB) error {
		if e := tx.AutoMigrate(&Job{}, &Device{}); e != nil {
			return e
		}
		return tx.Exec("PRAGMA user_version = 1").Error
	}); err != nil {
		sql.Close()
		return nil, err
	}
	if err = db.FirstOrCreate(&Device{ID: 1}, Device{ID: 1}).Error; err != nil {
		sql.Close()
		return nil, err
	}
	return &Store{db}, nil
}
func (s *Store) Close() error {
	db, err := s.db.DB()
	if err != nil {
		return err
	}
	return db.Close()
}
func (s *Store) Enqueue(j *Job, limit int) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&Job{}).Where("state IN ?", []State{Queued, Running, Verifying, Cleaning}).Count(&n).Error; err != nil {
			return err
		}
		if n >= int64(limit) {
			return ErrQueueFull
		}
		j.State = Queued
		j.AvailableAt = time.Now()
		return tx.Create(j).Error
	})
}
func (s *Store) List() ([]Job, error) {
	out := []Job{}
	err := s.db.Order("created_at DESC").Limit(200).Find(&out).Error
	return out, err
}
func (s *Store) All() ([]Job, error) { out := []Job{}; err := s.db.Find(&out).Error; return out, err }
func (s *Store) Get(id string) (Job, error) {
	var j Job
	err := s.db.First(&j, "id = ?", id).Error
	return j, err
}
func (s *Store) Device() (Device, error) { var d Device; err := s.db.First(&d, 1).Error; return d, err }

// A transaction and a durable singleton owner are the device lease. The process
// holds an OS lock for its entire lifetime; recovery precedes the next claim.
func (s *Store) Claim() (*Job, error) {
	var out Job
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var d Device
		if err := tx.First(&d, 1).Error; err != nil {
			return err
		}
		if d.Quarantined || d.Owner != "" {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Where("state = ? AND available_at <= ?", Queued, time.Now()).Order("created_at ASC").First(&out).Error; err != nil {
			return err
		}
		out.State = Running
		out.Attempts++
		out.Phase = "starting"
		if err := tx.Save(&out).Error; err != nil {
			return err
		}
		return tx.Model(&Device{}).Where("id = 1").Update("owner", out.ID).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &out, err
}
func (s *Store) Change(id string, to State, fields map[string]any) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var j Job
		if err := tx.First(&j, "id = ?", id).Error; err != nil {
			return err
		}
		if !CanTransition(j.State, to) {
			return ErrConflict
		}
		if fields == nil {
			fields = map[string]any{}
		}
		fields["state"] = to
		return tx.Model(&j).Updates(fields).Error
	})
}
func (s *Store) Progress(id, phase string, current, total int64) error {
	return s.db.Model(&Job{}).Where("id = ? AND state IN ?", id, []State{Running, Verifying}).Updates(map[string]any{"phase": phase, "current": current, "total": total}).Error
}
func (s *Store) Cancel(id string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var j Job
		if err := tx.First(&j, "id = ?", id).Error; err != nil {
			return err
		}
		if Terminal(j.State) || j.State == Cleaning {
			return ErrConflict
		}
		return tx.Model(&j).Update("cancel_requested", true).Error
	})
}
func (s *Store) Release(id string) error {
	return s.db.Model(&Device{}).Where("id = 1 AND owner = ?", id).Update("owner", "").Error
}
func (s *Store) Quarantine(id, reason string, desired State) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Job{}).Where("id = ?", id).Updates(map[string]any{"cleanup_error": reason, "pending_state": desired}).Error; err != nil {
			return err
		}
		return tx.Model(&Device{}).Where("id = 1").Updates(map[string]any{"quarantined": true, "reason": reason, "owner": ""}).Error
	})
}
func (s *Store) Resolve(id string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var j Job
		if err := tx.First(&j, "id = ?", id).Error; err != nil {
			return err
		}
		if j.State != Cleaning || j.CleanupError == "" || !Terminal(j.PendingState) {
			return ErrConflict
		}
		if err := tx.Model(&j).Updates(map[string]any{"cleanup_error": "", "state": j.PendingState, "pending_state": "", "phase": "operator-confirmed cleanup"}).Error; err != nil {
			return err
		}
		var n int64
		if err := tx.Model(&Job{}).Where("state = ? AND cleanup_error <> ''", Cleaning).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		return tx.Model(&Device{}).Where("id = 1").Updates(map[string]any{"quarantined": false, "reason": "", "owner": ""}).Error
	})
}

// Retries stay in running with a recorded attempt and wait under the same lease;
// state never moves backwards to queued.
func (s *Store) Retry(id string) error {
	return s.db.Model(&Job{}).Where("id = ? AND state = ?", id, Running).Updates(map[string]any{"attempts": gorm.Expr("attempts + 1"), "phase": "retry-wait"}).Error
}
func (s *Store) Metadata(id string, fields map[string]any) error {
	return s.db.Model(&Job{}).Where("id = ?", id).Updates(fields).Error
}
func (s *Store) Recover() error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var active []Job
		if err := tx.Where("state IN ?", []State{Running, Verifying, Cleaning}).Find(&active).Error; err != nil {
			return err
		}
		for _, j := range active {
			if j.State == Cleaning && j.CleanupError != "" {
				continue
			}
			if err := tx.Model(&j).Updates(map[string]any{"state": Cleaning, "pending_state": Failed, "cleanup_error": "Interrupted job: inspect device resources before resuming.", "error_code": "interrupted", "error_message": "The service stopped before cleanup was confirmed.", "failure_reason": "process-recovery"}).Error; err != nil {
				return err
			}
		}
		if len(active) > 0 {
			return tx.Model(&Device{}).Where("id = 1").Updates(map[string]any{"owner": "", "quarantined": true, "reason": "Interrupted or unconfirmed cleanup requires operator review."}).Error
		}
		return tx.Model(&Device{}).Where("id = 1").Update("owner", "").Error
	})
}

// BeginCleanup is the cancellation cut-off. Cancellation accepted before this
// transaction always wins, including during verification and publication.
func (s *Store) BeginCleanup(id string, desired State, code, message string) (State, error) {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var j Job
		if err := tx.First(&j, "id = ?", id).Error; err != nil {
			return err
		}
		if !CanTransition(j.State, Cleaning) {
			return ErrConflict
		}
		if j.CancelRequested {
			desired = Cancelled
			code = "cancelled"
			message = "The job was cancelled."
		}
		return tx.Model(&j).Updates(map[string]any{"state": Cleaning, "phase": "cleaning", "pending_state": desired, "error_code": code, "error_message": message, "failure_reason": code}).Error
	})
	return desired, err
}
