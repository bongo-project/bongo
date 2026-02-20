package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// State tracks which messages have been imported for resume support.
type State struct {
	mu   sync.Mutex
	path string
	data stateData
}

type stateData struct {
	Folders map[string]*folderState `json:"folders"`
}

type folderState struct {
	Imported []string `json:"imported"`
	set      map[string]struct{}
}

// NewState creates or loads a state file. If path is empty, state is in-memory only.
func NewState(path string) (*State, error) {
	s := &State{
		path: path,
		data: stateData{Folders: make(map[string]*folderState)},
	}

	if path != "" {
		raw, err := os.ReadFile(path)
		if err == nil {
			if err := json.Unmarshal(raw, &s.data); err != nil {
				return nil, fmt.Errorf("parsing state file: %w", err)
			}
			if s.data.Folders == nil {
				s.data.Folders = make(map[string]*folderState)
			}
			// Rebuild lookup sets.
			for _, fs := range s.data.Folders {
				fs.set = make(map[string]struct{}, len(fs.Imported))
				for _, id := range fs.Imported {
					fs.set[id] = struct{}{}
				}
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading state file: %w", err)
		}
	}

	return s, nil
}

// IsImported returns true if the message has already been imported.
func (s *State) IsImported(folder, sourceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	fs := s.data.Folders[folder]
	if fs == nil {
		return false
	}
	_, ok := fs.set[sourceID]
	return ok
}

// MarkImported records a message as imported.
func (s *State) MarkImported(folder, sourceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fs := s.data.Folders[folder]
	if fs == nil {
		fs = &folderState{set: make(map[string]struct{})}
		s.data.Folders[folder] = fs
	}

	if _, ok := fs.set[sourceID]; ok {
		return
	}

	fs.Imported = append(fs.Imported, sourceID)
	fs.set[sourceID] = struct{}{}
}

// ImportedCount returns the number of imported messages for a folder.
func (s *State) ImportedCount(folder string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	fs := s.data.Folders[folder]
	if fs == nil {
		return 0
	}
	return len(fs.Imported)
}

// Save writes the state to disk. No-op if path is empty.
func (s *State) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.path == "" {
		return nil
	}

	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	if err := os.WriteFile(s.path, raw, 0o644); err != nil {
		return fmt.Errorf("writing state file: %w", err)
	}

	return nil
}
