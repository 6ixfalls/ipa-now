package jobs

import "time"

type State string

const (
	Queued    State = "queued"
	Running   State = "running"
	Verifying State = "verifying"
	Cleaning  State = "cleaning"
	Completed State = "completed"
	Failed    State = "failed"
	Cancelled State = "cancelled"
)

func Terminal(s State) bool { return s == Completed || s == Failed || s == Cancelled }
func CanTransition(a, b State) bool {
	switch a {
	case Queued:
		return b == Running || b == Cleaning
	case Running:
		return b == Verifying || b == Cleaning
	case Verifying:
		return b == Cleaning
	case Cleaning:
		return Terminal(b)
	}
	return false
}

type Job struct {
	ID              string     `json:"id" gorm:"primaryKey;size:32"`
	Target          string     `json:"target"`
	Source          string     `json:"source"`
	State           State      `json:"state" gorm:"index"`
	Phase           string     `json:"phase"`
	Attempts        int        `json:"attempts"`
	CancelRequested bool       `json:"cancelRequested"`
	ErrorCode       string     `json:"errorCode,omitempty"`
	ErrorMessage    string     `json:"errorMessage,omitempty"`
	FailureReason   string     `json:"-"`
	CleanupError    string     `json:"cleanupError,omitempty"`
	PendingState    State      `json:"-"`
	Installed       bool       `json:"installed"`
	Replaced        bool       `json:"replaced"`
	Uninstalled     bool       `json:"uninstalled"`
	Current         int64      `json:"current"`
	Total           int64      `json:"total"`
	Bytes           int64      `json:"bytes"`
	SHA256          string     `json:"sha256,omitempty"`
	BundleID        string     `json:"bundleId,omitempty" gorm:"size:255"`
	Version         string     `json:"version,omitempty" gorm:"size:64"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	AvailableAt     time.Time  `json:"-"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
	ArtifactExpired bool       `json:"artifactExpired"`
}
type Device struct {
	ID          uint   `json:"-" gorm:"primaryKey"`
	Owner       string `json:"activeJob,omitempty"`
	Quarantined bool   `json:"cleanupRequired"`
	Reason      string `json:"reason,omitempty"`
}

// Operation maps a durable library attempt to its job. It is never API data.
// Retain these records and their journals with job metadata for crash recovery.
type Operation struct {
	ID        string `gorm:"primaryKey;size:32"`
	JobID     string `gorm:"index;not null"`
	CreatedAt time.Time
}
