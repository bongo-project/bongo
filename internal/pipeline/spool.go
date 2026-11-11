package pipeline

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const envSuffix = ".env"

// deliver/ is no longer written (a completed envelope is removed), but it is
// still created so an older binary, which moves envelopes there, still runs.
var spoolDirs = []string{"incoming", "processing", "deliver", "quarantine", "error"}

// WriteEnvelope stages data in a temp file before the atomic rename. The temp
// name must not end in envSuffix: every scan selects envelopes by that suffix,
// and a scan that saw a half-written file would move or read it before the
// rename, failing the writer with "no such file or directory".
const (
	tmpPrefix = ".tmp-"
	tmpSuffix = ".tmp"
)

// isEnvelopeFile reports whether a directory entry is a finished envelope.
// Temp files are excluded by prefix as well as suffix so that ".tmp-*.env"
// files left by an older binary are never treated as envelopes.
func isEnvelopeFile(e os.DirEntry) bool {
	name := e.Name()
	return !e.IsDir() && filepath.Ext(name) == envSuffix && !strings.HasPrefix(name, tmpPrefix)
}

// removeStaleTempFiles deletes temp files a crash left behind in the spool
// directories. It runs at startup, when this process has no writers yet, and
// returns the number removed.
func (s *SpoolStore) removeStaleTempFiles() (int, error) {
	removed := 0
	for _, d := range spoolDirs {
		entries, err := os.ReadDir(filepath.Join(s.baseDir, d))
		if err != nil {
			return removed, fmt.Errorf("spool: read %s for temp cleanup: %w", d, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), tmpPrefix) {
				continue
			}
			if err := os.Remove(filepath.Join(s.baseDir, d, e.Name())); err != nil && !os.IsNotExist(err) {
				return removed, fmt.Errorf("spool: remove stale temp %s/%s: %w", d, e.Name(), err)
			}
			removed++
		}
	}
	return removed, nil
}

// SpoolStore manages the filesystem spool queue for the message processing pipeline.
// Messages flow through directories as they are processed:
//
//	incoming/ -> processing/ -> (removed once every stage has run)
//	                        \-> quarantine/
//	                        \-> error/
//
// deliver/ is legacy: an earlier binary moved completed envelopes there and
// nothing ever read them. NewSpoolStore deletes any it finds.
type SpoolStore struct {
	baseDir string
	logger  *slog.Logger

	// beforeListRead, when set, runs in List between the directory read and
	// each envelope's file read. Tests use it to move an envelope inside that
	// window, which cannot otherwise be hit deterministically.
	beforeListRead func(dir, id string)
}

// NewSpoolStore creates the spool directory structure and returns a SpoolStore.
// It calls RecoverCrashed to move any messages left in processing/ back to incoming/.
func NewSpoolStore(baseDir string, logger *slog.Logger) (*SpoolStore, error) {
	for _, d := range spoolDirs {
		path := filepath.Join(baseDir, d)
		if err := os.MkdirAll(path, 0o750); err != nil {
			return nil, fmt.Errorf("spool: create directory %s: %w", path, err)
		}
	}

	s := &SpoolStore{
		baseDir: baseDir,
		logger:  logger,
	}

	if n, err := s.removeStaleTempFiles(); err != nil {
		return nil, err
	} else if n > 0 {
		logger.Warn("spool: removed stale temp files", "count", n)
	}

	n, err := s.RecoverCrashed()
	if err != nil {
		return nil, fmt.Errorf("spool: recover crashed: %w", err)
	}
	if n > 0 {
		logger.Info("spool: recovered crashed messages", "count", n)
	}

	if n, err := s.removeDeliveredEnvelopes(); err != nil {
		return nil, err
	} else if n > 0 {
		logger.Info("spool: removed delivered envelopes", "count", n)
	}

	return s, nil
}

// removeDeliveredEnvelopes deletes the envelopes an earlier binary left in
// deliver/. Each belongs to a message whose pipeline finished, so nothing will
// read it again, and while it stays the blob GC counts its blob as referenced
// even after the mail is deleted. Only envelope files are removed. It returns
// the number removed.
func (s *SpoolStore) removeDeliveredEnvelopes() (int, error) {
	dir := filepath.Join(s.baseDir, "deliver")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("spool: read deliver for cleanup: %w", err)
	}
	removed := 0
	for _, e := range entries {
		if !isEnvelopeFile(e) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("spool: remove delivered envelope %s: %w", e.Name(), err)
		}
		removed++
	}
	return removed, nil
}

// Enqueue marshals the envelope and writes it atomically to incoming/.
func (s *SpoolStore) Enqueue(env *bongov1.QueueEnvelope) error {
	return s.WriteEnvelope("incoming", env)
}

// Dequeue reads the oldest envelope from incoming/, moves it atomically to processing/,
// and returns it. Returns nil, nil if the queue is empty.
//
// Oldest means the earliest file modification time, ties broken by name.
// Envelope IDs are random (uuid.New) or derived from a web send ID, so name
// order is arbitrary: picking by name processed a backlog in random order, and
// a message whose ID sorted late waited until the whole queue had drained
// (hours, in the load-test soak). Enqueue writes by rename, so the time is when
// the message was queued; RecoverCrashed and the shutdown return also move by
// rename and keep it.
func (s *SpoolStore) Dequeue() (*bongov1.QueueEnvelope, error) {
	entries, err := os.ReadDir(filepath.Join(s.baseDir, "incoming"))
	if err != nil {
		return nil, fmt.Errorf("spool: read incoming: %w", err)
	}

	var (
		oldest    string
		oldestMod time.Time
	)
	for _, e := range entries {
		if !isEnvelopeFile(e) {
			continue
		}
		info, err := e.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("spool: stat incoming/%s: %w", e.Name(), err)
		}
		mod := info.ModTime()
		if oldest == "" || mod.Before(oldestMod) || (mod.Equal(oldestMod) && e.Name() < oldest) {
			oldest, oldestMod = e.Name(), mod
		}
	}
	if oldest == "" {
		return nil, nil
	}
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
//
// The listing is a snapshot, not a lock: the runner renames envelopes between
// directories and removes them on completion while List runs. An envelope that
// vanishes between the directory read and its file read is therefore skipped,
// exactly as if it had gone a moment before the directory read, which List has
// always tolerated. Every caller reads the result that way (the blob GC's
// reference scan, the error and quarantine purges, the quarantine page), and
// failing instead aborted the blob GC on most passes under load. Any other
// read or unmarshal failure is still an error: that envelope is present and
// its contents are unknown.
func (s *SpoolStore) List(dir string) ([]*bongov1.QueueEnvelope, error) {
	entries, err := os.ReadDir(filepath.Join(s.baseDir, dir))
	if err != nil {
		return nil, fmt.Errorf("spool: list %s: %w", dir, err)
	}

	var envs []*bongov1.QueueEnvelope
	for _, e := range entries {
		if !isEnvelopeFile(e) {
			continue
		}
		id := e.Name()[:len(e.Name())-len(envSuffix)]
		if s.beforeListRead != nil {
			s.beforeListRead(dir, id)
		}
		env, err := s.ReadEnvelope(dir, id)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
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
	tmpPath := filepath.Join(dirPath, tmpPrefix+env.Id+tmpSuffix)
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
		if !isEnvelopeFile(e) {
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
