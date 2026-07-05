package pipeline

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

const envSuffix = ".env"

// SpoolStore manages the filesystem spool queue for the message processing pipeline.
// Messages flow through directories as they are processed:
//
//	incoming/ -> processing/ -> deliver/
//	                        \-> quarantine/
//	                        \-> error/
type SpoolStore struct {
	baseDir string
	logger  *slog.Logger
}

// NewSpoolStore creates the spool directory structure and returns a SpoolStore.
// It calls RecoverCrashed to move any messages left in processing/ back to incoming/.
func NewSpoolStore(baseDir string, logger *slog.Logger) (*SpoolStore, error) {
	dirs := []string{
		"incoming",
		"processing",
		"deliver",
		"quarantine",
		"error",
	}
	for _, d := range dirs {
		path := filepath.Join(baseDir, d)
		if err := os.MkdirAll(path, 0o750); err != nil {
			return nil, fmt.Errorf("spool: create directory %s: %w", path, err)
		}
	}

	s := &SpoolStore{
		baseDir: baseDir,
		logger:  logger,
	}

	n, err := s.RecoverCrashed()
	if err != nil {
		return nil, fmt.Errorf("spool: recover crashed: %w", err)
	}
	if n > 0 {
		logger.Info("spool: recovered crashed messages", "count", n)
	}

	return s, nil
}

// Enqueue marshals the envelope and writes it atomically to incoming/.
func (s *SpoolStore) Enqueue(env *bongov1.QueueEnvelope) error {
	return s.WriteEnvelope("incoming", env)
}

// Dequeue reads the oldest envelope from incoming/, moves it atomically to processing/,
// and returns it. Returns nil, nil if the queue is empty.
func (s *SpoolStore) Dequeue() (*bongov1.QueueEnvelope, error) {
	entries, err := os.ReadDir(filepath.Join(s.baseDir, "incoming"))
	if err != nil {
		return nil, fmt.Errorf("spool: read incoming: %w", err)
	}

	// Collect .env files and sort by name (lexicographic = chronological for UUID v7 or timestamp-prefixed IDs).
	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == envSuffix {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, nil
	}

	sort.Strings(names)
	oldest := names[0]
	id := oldest[:len(oldest)-len(envSuffix)]

	src := filepath.Join(s.baseDir, "incoming", oldest)
	dst := filepath.Join(s.baseDir, "processing", oldest)

	if err := os.Rename(src, dst); err != nil {
		return nil, fmt.Errorf("spool: dequeue rename %s: %w", id, err)
	}

	env, err := s.ReadEnvelope("processing", id)
	if err != nil {
		return nil, fmt.Errorf("spool: dequeue read %s: %w", id, err)
	}

	return env, nil
}

// Move moves {id}.env from the named from directory to the named to directory atomically.
func (s *SpoolStore) Move(id string, from string, to string) error {
	filename := id + envSuffix
	src := filepath.Join(s.baseDir, from, filename)
	dst := filepath.Join(s.baseDir, to, filename)
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("spool: move %s from %s to %s: %w", id, from, to, err)
	}
	return nil
}

// List returns all envelopes in the named directory, sorted by filename.
func (s *SpoolStore) List(dir string) ([]*bongov1.QueueEnvelope, error) {
	entries, err := os.ReadDir(filepath.Join(s.baseDir, dir))
	if err != nil {
		return nil, fmt.Errorf("spool: list %s: %w", dir, err)
	}

	var envs []*bongov1.QueueEnvelope
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != envSuffix {
			continue
		}
		id := e.Name()[:len(e.Name())-len(envSuffix)]
		env, err := s.ReadEnvelope(dir, id)
		if err != nil {
			return nil, fmt.Errorf("spool: list read %s/%s: %w", dir, id, err)
		}
		envs = append(envs, env)
	}

	return envs, nil
}

// ReadEnvelope reads and unmarshals a single envelope from dir.
func (s *SpoolStore) ReadEnvelope(dir string, id string) (*bongov1.QueueEnvelope, error) {
	path := filepath.Join(s.baseDir, dir, id+envSuffix)
	data, err := os.ReadFile(path) // #nosec G304 -- dir/id are internal spool names beneath baseDir
	if err != nil {
		return nil, fmt.Errorf("spool: read envelope %s/%s: %w", dir, id, err)
	}

	var env bongov1.QueueEnvelope
	if err := proto.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("spool: unmarshal envelope %s/%s: %w", dir, id, err)
	}

	return &env, nil
}

// WriteEnvelope marshals and writes an envelope to dir atomically.
// The envelope's Id field is used as the filename base.
func (s *SpoolStore) WriteEnvelope(dir string, env *bongov1.QueueEnvelope) error {
	data, err := proto.Marshal(env)
	if err != nil {
		return fmt.Errorf("spool: marshal envelope %s: %w", env.Id, err)
	}

	dirPath := filepath.Join(s.baseDir, dir)
	tmpPath := filepath.Join(dirPath, ".tmp-"+env.Id+envSuffix)
	finalPath := filepath.Join(dirPath, env.Id+envSuffix)

	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("spool: write temp %s: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		// Best-effort cleanup of temp file on rename failure.
		_ = os.Remove(tmpPath)
		return fmt.Errorf("spool: rename temp to final %s: %w", env.Id, err)
	}

	return nil
}

// Remove deletes the envelope file for id from dir.
func (s *SpoolStore) Remove(dir string, id string) error {
	path := filepath.Join(s.baseDir, dir, id+envSuffix)
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("spool: remove %s/%s: %w", dir, id, err)
	}
	return nil
}

// RecoverCrashed moves all .env files from processing/ back to incoming/ on startup.
// It returns the number of files recovered.
func (s *SpoolStore) RecoverCrashed() (int, error) {
	entries, err := os.ReadDir(filepath.Join(s.baseDir, "processing"))
	if err != nil {
		return 0, fmt.Errorf("spool: read processing for recovery: %w", err)
	}

	recovered := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != envSuffix {
			continue
		}
		src := filepath.Join(s.baseDir, "processing", e.Name())
		dst := filepath.Join(s.baseDir, "incoming", e.Name())
		if err := os.Rename(src, dst); err != nil {
			return recovered, fmt.Errorf("spool: recover %s: %w", e.Name(), err)
		}
		recovered++
		s.logger.Warn("spool: recovered crashed message", "file", e.Name())
	}

	return recovered, nil
}
