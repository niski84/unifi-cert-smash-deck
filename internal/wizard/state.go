package wizard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Step constants
const (
	StepDiscover  = 0
	StepSSH       = 1
	StepDomain    = 2
	StepPreflight = 3
	StepInstall   = 4
	StepVerify    = 5
	StepDone      = 6
)

// CheckStatus describes the result of an individual check.
type CheckStatus string

const (
	StatusPending CheckStatus = "pending"
	StatusRunning CheckStatus = "running"
	StatusPassed  CheckStatus = "passed"
	StatusFailed  CheckStatus = "failed"
	StatusWarning CheckStatus = "warning"
	StatusSkipped CheckStatus = "skipped"
)

// Check is a single named assertion within a wizard step.
type Check struct {
	ID       string      `json:"id"`
	Label    string      `json:"label"`
	Status   CheckStatus `json:"status"`
	Detail   string      `json:"detail,omitempty"`
	Required bool        `json:"required"`
}

// StepResult holds the outcome of running one wizard step.
type StepResult struct {
	StepNum    int       `json:"step_num"`
	Status     string    `json:"status"` // pending | running | passed | failed
	Checks     []Check   `json:"checks,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// UDMLeState represents the detected state of the udm-le installation.
type UDMLeState string

const (
	UDMLeUnknown  UDMLeState = "unknown"
	UDMLeFresh    UDMLeState = "fresh"    // not installed
	UDMLeHealthy  UDMLeState = "healthy"  // installed, timer active, cert valid
	UDMLeDegraded UDMLeState = "degraded" // installed but timer stopped
	UDMLeBroken   UDMLeState = "broken"   // installed but cert expired/missing
	UDMLeMissing  UDMLeState = "missing"  // was installed but /data/udm-le wiped
)

// InstallAction is the action the wizard will take during the install step.
type InstallAction string

const (
	ActionInstall   InstallAction = "install"
	ActionRepair    InstallAction = "repair"
	ActionUpdate    InstallAction = "update"
	ActionRenew     InstallAction = "renew"
	ActionReinstall InstallAction = "reinstall"
)

// State holds the full wizard progress and all collected data.
type State struct {
	Version int `json:"version"`

	CurrentStep int `json:"current_step"`

	// Connection info
	UDMHost       string `json:"udm_host"`
	UDMPort       int    `json:"udm_port"`
	SSHUser       string `json:"ssh_user"`
	SSHKeyPath    string `json:"ssh_key_path"`
	SSHKnownHosts string `json:"ssh_known_hosts"`
	KeyGenerated  bool   `json:"key_generated"`

	// Detected UDM state
	UDMOSVersion string     `json:"udm_os_version"`
	UDMLeState   UDMLeState `json:"udm_le_state"`

	// Current remote certificate
	CurrentCertCN         string `json:"current_cert_cn"`
	CurrentCertDays       int    `json:"current_cert_days"`
	CurrentCertSelfSigned bool   `json:"current_cert_self_signed"`

	// Domain / cert config
	CertHosts   string `json:"cert_hosts"`
	CertEmail   string `json:"cert_email"`
	DNSProvider string `json:"dns_provider"`
	DNSZone     string `json:"dns_zone"`
	StagingMode bool   `json:"staging_mode"`

	// Planned action
	InstallAction InstallAction `json:"install_action"`

	// Issued cert info (post-install)
	IssuedCertCN     string    `json:"issued_cert_cn"`
	IssuedCertExpiry time.Time `json:"issued_cert_expiry,omitempty"`
	IssuedByLE       bool      `json:"issued_by_le"`

	// Per-step results
	Results map[int]*StepResult `json:"results,omitempty"`

	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

// NewState returns a State initialised with sensible defaults.
func NewState() State {
	return State{
		Version:    1,
		UDMPort:    22,
		SSHUser:    "root",
		UDMLeState: UDMLeUnknown,
		Results:    make(map[int]*StepResult),
	}
}

// StepPassed reports whether step n has been completed successfully.
func (s State) StepPassed(n int) bool {
	r, ok := s.Results[n]
	if !ok {
		return false
	}
	return r.Status == "passed"
}

// CanAccessStep reports whether the user may navigate to step n.
// Step 0 is always accessible. Every other step requires the previous one to have passed.
func (s State) CanAccessStep(n int) bool {
	if n <= 0 {
		return true
	}
	return s.StepPassed(n - 1)
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

// Store wraps State with a mutex and optional disk persistence.
type Store struct {
	mu    sync.Mutex
	path  string
	state *State
}

// NewStore loads wizard state from dataDir/wizard_state.json, or creates a fresh
// state if the file does not exist.
func NewStore(dataDir string) *Store {
	path := filepath.Join(dataDir, "wizard_state.json")
	st := &Store{path: path}

	raw, err := os.ReadFile(path)
	if err == nil {
		var loaded State
		if json.Unmarshal(raw, &loaded) == nil {
			if loaded.Results == nil {
				loaded.Results = make(map[int]*StepResult)
			}
			st.state = &loaded
			return st
		}
	}

	fresh := NewState()
	st.state = &fresh
	return st
}

// Save persists the current state to disk.
func (st *Store) Save() error {
	st.mu.Lock()
	defer st.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st.state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(st.path, b, 0o600)
}

// Snapshot returns a copy of the current state (safe for reading without holding the lock).
func (st *Store) Snapshot() State {
	st.mu.Lock()
	defer st.mu.Unlock()
	// Deep copy via JSON round-trip to avoid shared map references.
	b, _ := json.Marshal(st.state)
	var copy State
	_ = json.Unmarshal(b, &copy)
	if copy.Results == nil {
		copy.Results = make(map[int]*StepResult)
	}
	return copy
}

// Update calls fn with a pointer to the state while holding the lock,
// then saves to disk.
func (st *Store) Update(fn func(*State)) {
	st.mu.Lock()
	fn(st.state)
	st.mu.Unlock()
	_ = st.Save()
}

// Reset discards all progress and creates a fresh state.
func (st *Store) Reset() {
	fresh := NewState()
	st.mu.Lock()
	st.state = &fresh
	st.mu.Unlock()
	_ = st.Save()
}

// IsDone reports whether the wizard has been fully completed.
func (st *Store) IsDone() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return !st.state.CompletedAt.IsZero()
}
