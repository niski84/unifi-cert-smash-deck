package certdeck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RuntimeState is persisted between restarts for dashboard continuity.
type RuntimeState struct {
	CommonName        string    `json:"common_name"`
	NotAfter          time.Time `json:"not_after"`
	LastCheckAt       time.Time `json:"last_check_at"`
	LastSyncAt        time.Time `json:"last_sync_at"`
	LastError         string    `json:"last_error,omitempty"`
	Renewing          bool      `json:"renewing"`
	LastActiveClients int       `json:"last_active_clients"`
}

type StateStore struct {
	mu   sync.RWMutex
	path string
	s    RuntimeState
}

func NewStateStore() *StateStore {
	p := filepath.Join(DataDir(), "unificert-state.json")
	st := &StateStore{path: p}
	raw, err := os.ReadFile(p)
	if err == nil {
		var s RuntimeState
		if json.Unmarshal(raw, &s) == nil {
			st.s = s
		}
	}
	return st
}

func (st *StateStore) Snapshot() RuntimeState {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.s
}

func (st *StateStore) Update(fn func(*RuntimeState)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(&st.s)
	b, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(st.path, b, 0o644)
}
