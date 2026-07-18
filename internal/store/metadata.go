package store

import (
	"container/list"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Document types for the storeobject table.
const (
	DocTypeMail    = 1
	DocTypeEvent   = 2
	DocTypeContact = 3
	DocTypeFolder  = 4
)

// StoreObject represents a row in a user's storeobject table.
type StoreObject struct {
	GUID           string
	UserID         string
	CollectionGUID string
	Type           int
	Flags          int
	Size           int64
	CreatedAt      time.Time
	ModifiedAt     time.Time
}

// UserIndex is a per-user SQLite database containing that user's metadata.
type UserIndex struct {
	db     *sql.DB
	userID string
}

func openSQLiteDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite %s: %w", path, err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		closeErr := db.Close()
		return nil, errors.Join(fmt.Errorf("setting WAL mode: %w", err), closeErr)
	}
	return db, nil
}

func newUserIndex(dbPath, userID string) (*UserIndex, error) {
	db, err := openSQLiteDB(dbPath)
	if err != nil {
		return nil, err
	}

	idx := &UserIndex{db: db, userID: userID}
	if err := idx.createSchema(); err != nil {
		closeErr := db.Close()
		return nil, errors.Join(fmt.Errorf("creating user schema: %w", err), closeErr)
	}
	return idx, nil
}

func (u *UserIndex) createSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS storeobject (
		guid TEXT PRIMARY KEY,
		collection_guid TEXT,
		type INTEGER NOT NULL,
		flags INTEGER DEFAULT 0,
		size INTEGER,
		created_at INTEGER,
		modified_at INTEGER
	);

	CREATE INDEX IF NOT EXISTS idx_storeobject_type
		ON storeobject(type);

	CREATE INDEX IF NOT EXISTS idx_storeobject_type_created
		ON storeobject(type, created_at);

	CREATE TABLE IF NOT EXISTS maildocument (
		guid TEXT PRIMARY KEY REFERENCES storeobject(guid),
		conversation_guid TEXT,
		subject TEXT,
		sender TEXT,
		message_id TEXT,
		parent_message_id TEXT,
		list_id TEXT,
		received_at INTEGER
	);

	CREATE INDEX IF NOT EXISTS idx_maildocument_conversation
		ON maildocument(conversation_guid);

	CREATE INDEX IF NOT EXISTS idx_maildocument_message_id
		ON maildocument(message_id);

	CREATE TABLE IF NOT EXISTS eventdocument (
		guid TEXT PRIMARY KEY REFERENCES storeobject(guid),
		uid TEXT,
		summary TEXT,
		location TEXT,
		start_time INTEGER,
		end_time INTEGER,
		recurrence TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_eventdocument_time
		ON eventdocument(start_time, end_time);

	CREATE TABLE IF NOT EXISTS conversation (
		guid TEXT PRIMARY KEY,
		subject TEXT,
		last_date INTEGER,
		message_count INTEGER,
		unread_count INTEGER
	);

	CREATE INDEX IF NOT EXISTS idx_conversation_last_date
		ON conversation(last_date);

	CREATE TABLE IF NOT EXISTS message_tags (
		mail_guid TEXT NOT NULL,
		tag TEXT NOT NULL,
		PRIMARY KEY (mail_guid, tag)
	);

	CREATE INDEX IF NOT EXISTS idx_message_tags_tag
		ON message_tags(tag);
	`
	_, err := u.db.Exec(schema)
	return err
}

// IndexMail transactionally inserts a mail into storeobject and maildocument tables.
func (u *UserIndex) IndexMail(guid, subject, sender string, receivedAt, size int64) error {
	tx, err := u.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO storeobject (guid, type, flags, size, created_at, modified_at)
		VALUES (?, ?, 0, ?, ?, ?)
		ON CONFLICT(guid) DO UPDATE SET
			size = excluded.size,
			modified_at = excluded.modified_at
	`, guid, DocTypeMail, size, receivedAt, receivedAt)
	if err != nil {
		return fmt.Errorf("indexing storeobject: %w", err)
	}

	_, err = tx.Exec(`
		INSERT INTO maildocument (guid, subject, sender, received_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(guid) DO UPDATE SET
			subject = excluded.subject,
			sender = excluded.sender,
			received_at = excluded.received_at
	`, guid, subject, sender, receivedAt)
	if err != nil {
		return fmt.Errorf("indexing maildocument: %w", err)
	}

	return tx.Commit()
}

// IndexMailTags replaces all tags for the given mail GUID with the provided set.
// Passing an empty or nil tags slice removes all tags for the message.
func (u *UserIndex) IndexMailTags(mailGUID string, tags []string) error {
	tx, err := u.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM message_tags WHERE mail_guid = ?`, mailGUID); err != nil {
		return fmt.Errorf("deleting old tags: %w", err)
	}

	for _, tag := range tags {
		if _, err := tx.Exec(`INSERT INTO message_tags (mail_guid, tag) VALUES (?, ?)`, mailGUID, tag); err != nil {
			return fmt.Errorf("inserting tag %q: %w", tag, err)
		}
	}

	return tx.Commit()
}

// DeleteMail removes a mail's derived-index rows (storeobject, maildocument,
// message_tags) so the indexes don't retain entries for a deleted message. It is
// the symmetric inverse of IndexMail + IndexMailTags.
func (u *UserIndex) DeleteMail(mailGUID string) error {
	tx, err := u.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, stmt := range []string{
		`DELETE FROM message_tags WHERE mail_guid = ?`,
		`DELETE FROM maildocument WHERE guid = ?`,
		`DELETE FROM storeobject WHERE guid = ?`,
	} {
		if _, err := tx.Exec(stmt, mailGUID); err != nil {
			return fmt.Errorf("deleting mail index rows: %w", err)
		}
	}

	return tx.Commit()
}

// QueryByTag returns all mail GUIDs that have the given tag, ordered by mail_guid.
func (u *UserIndex) QueryByTag(tag string) ([]string, error) {
	rows, err := u.db.Query(`
		SELECT mail_guid FROM message_tags
		WHERE tag = ?
		ORDER BY mail_guid
	`, tag)
	if err != nil {
		return nil, fmt.Errorf("querying by tag: %w", err)
	}
	defer rows.Close()

	var guids []string
	for rows.Next() {
		var guid string
		if err := rows.Scan(&guid); err != nil {
			return nil, fmt.Errorf("scanning mail_guid: %w", err)
		}
		guids = append(guids, guid)
	}
	return guids, rows.Err()
}

// IndexObject upserts a StoreObject into this user's index.
func (u *UserIndex) IndexObject(obj StoreObject) error {
	_, err := u.db.Exec(`
		INSERT INTO storeobject (guid, collection_guid, type, flags, size, created_at, modified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(guid) DO UPDATE SET
			collection_guid = excluded.collection_guid,
			type = excluded.type,
			flags = excluded.flags,
			size = excluded.size,
			modified_at = excluded.modified_at
	`,
		obj.GUID,
		obj.CollectionGUID,
		obj.Type,
		obj.Flags,
		obj.Size,
		obj.CreatedAt.Unix(),
		obj.ModifiedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("indexing object: %w", err)
	}
	return nil
}

// QueryByType returns all store objects with the given document type.
func (u *UserIndex) QueryByType(docType int) ([]StoreObject, error) {
	rows, err := u.db.Query(`
		SELECT guid, collection_guid, type, flags, size, created_at, modified_at
		FROM storeobject
		WHERE type = ?
		ORDER BY created_at DESC
	`, docType)
	if err != nil {
		return nil, fmt.Errorf("querying by type: %w", err)
	}
	defer rows.Close()

	return u.scanStoreObjects(rows)
}

// QueryByDateRange returns objects of a given type within a time range.
func (u *UserIndex) QueryByDateRange(docType int, start, end time.Time) ([]StoreObject, error) {
	rows, err := u.db.Query(`
		SELECT guid, collection_guid, type, flags, size, created_at, modified_at
		FROM storeobject
		WHERE type = ? AND created_at >= ? AND created_at <= ?
		ORDER BY created_at DESC
	`, docType, start.Unix(), end.Unix())
	if err != nil {
		return nil, fmt.Errorf("querying by date range: %w", err)
	}
	defer rows.Close()

	return u.scanStoreObjects(rows)
}

// Close closes the user's SQLite database.
func (u *UserIndex) Close() error {
	return u.db.Close()
}

func (u *UserIndex) scanStoreObjects(rows *sql.Rows) ([]StoreObject, error) {
	var objects []StoreObject
	for rows.Next() {
		var obj StoreObject
		var collGUID sql.NullString
		var createdAt, modifiedAt int64

		if err := rows.Scan(
			&obj.GUID,
			&collGUID,
			&obj.Type,
			&obj.Flags,
			&obj.Size,
			&createdAt,
			&modifiedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning store object: %w", err)
		}

		obj.UserID = u.userID
		obj.CollectionGUID = collGUID.String
		obj.CreatedAt = time.Unix(createdAt, 0)
		obj.ModifiedAt = time.Unix(modifiedAt, 0)
		objects = append(objects, obj)
	}
	return objects, rows.Err()
}

// SystemIndex is a single system-wide SQLite database for users, domains, and aliases.
type SystemIndex struct {
	db *sql.DB
}

func newSystemIndex(dbPath string) (*SystemIndex, error) {
	db, err := openSQLiteDB(dbPath)
	if err != nil {
		return nil, err
	}

	idx := &SystemIndex{db: db}
	if err := idx.createSchema(); err != nil {
		closeErr := db.Close()
		return nil, errors.Join(fmt.Errorf("creating system schema: %w", err), closeErr)
	}
	return idx, nil
}

func (s *SystemIndex) createSchema() error {
	schema := `
	-- system.db is a DERIVED INDEX — rebuildable from Pebble.
	-- Credentials (password_hash, TOTP secrets) live in Pebble at /user/{id}/profile.
	-- This table is for admin directory queries only (listing users, checking enabled status).
	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		username TEXT NOT NULL UNIQUE,
		display_name TEXT,
		enabled INTEGER DEFAULT 1,
		created_at INTEGER,
		modified_at INTEGER
	);

	CREATE TABLE IF NOT EXISTS domains (
		name TEXT PRIMARY KEY,
		enabled INTEGER DEFAULT 1,
		created_at INTEGER
	);

	CREATE TABLE IF NOT EXISTS aliases (
		address TEXT PRIMARY KEY,
		target_user_id TEXT NOT NULL REFERENCES users(id),
		enabled INTEGER DEFAULT 1,
		created_at INTEGER
	);

	CREATE INDEX IF NOT EXISTS idx_aliases_target
		ON aliases(target_user_id);

	-- addresses is the global address index (FR-3 / US-RECV-2).
	-- DERIVED INDEX — rebuildable from Pebble (/system/address/{addr}).
	-- Addresses are stored lowercase. kind is one of: primary, alias, identity.
	-- can_send/verified are stored as 0/1 integers (SQLite boolean convention).
	CREATE TABLE IF NOT EXISTS addresses (
		address    TEXT PRIMARY KEY,
		user_id    TEXT NOT NULL,
		domain     TEXT,
		kind       TEXT,
		can_send   INTEGER,
		verified   INTEGER,
		created_at INTEGER
	);

	CREATE INDEX IF NOT EXISTS idx_addresses_user
		ON addresses(user_id);
	`
	_, err := s.db.Exec(schema)
	return err
}

// DB returns the underlying sql.DB for direct queries.
func (s *SystemIndex) DB() *sql.DB {
	return s.db
}

// ListUserIDs returns the IDs of all users known to the system index.
func (s *SystemIndex) ListUserIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM users`)
	if err != nil {
		return nil, fmt.Errorf("listing user ids: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning user id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Close closes the system database.
func (s *SystemIndex) Close() error {
	return s.db.Close()
}

// lruEntry tracks an open user database in the LRU cache.
type lruEntry struct {
	userID   string
	index    *UserIndex
	lastUsed time.Time
}

// MetadataManager manages per-user SQLite databases and the system index.
// It maintains an LRU cache of open user database connections.
type MetadataManager struct {
	dataDir  string
	usersDir string

	System *SystemIndex

	mu       sync.Mutex
	cache    map[string]*list.Element
	lruList  *list.List
	maxOpen  int
	idleTime time.Duration
}

// MetadataManagerOption configures a MetadataManager.
type MetadataManagerOption func(*MetadataManager)

// WithMaxOpen sets the maximum number of open user databases.
func WithMaxOpen(n int) MetadataManagerOption {
	return func(m *MetadataManager) { m.maxOpen = n }
}

// WithIdleTimeout sets the idle timeout for user database connections.
func WithIdleTimeout(d time.Duration) MetadataManagerOption {
	return func(m *MetadataManager) { m.idleTime = d }
}

// NewMetadataManager creates a MetadataManager at the given data directory.
// It creates {dataDir}/metadata/system.db and {dataDir}/metadata/users/.
func NewMetadataManager(dataDir string, opts ...MetadataManagerOption) (*MetadataManager, error) {
	metaDir := filepath.Join(dataDir, "metadata")
	usersDir := filepath.Join(metaDir, "users")

	if err := os.MkdirAll(usersDir, 0o750); err != nil {
		return nil, fmt.Errorf("creating metadata dirs: %w", err)
	}

	systemDB, err := newSystemIndex(filepath.Join(metaDir, "system.db"))
	if err != nil {
		return nil, fmt.Errorf("opening system index: %w", err)
	}

	mgr := &MetadataManager{
		dataDir:  metaDir,
		usersDir: usersDir,
		System:   systemDB,
		cache:    make(map[string]*list.Element),
		lruList:  list.New(),
		maxOpen:  256,
		idleTime: 5 * time.Minute,
	}

	for _, opt := range opts {
		opt(mgr)
	}

	return mgr, nil
}

// OpenUser opens or returns a cached UserIndex for the given user ID.
func (m *MetadataManager) OpenUser(userID string) (*UserIndex, error) {
	if !IsValidUserID(userID) {
		return nil, fmt.Errorf("invalid user id")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check cache.
	if elem, ok := m.cache[userID]; ok {
		entry := elem.Value.(*lruEntry)
		entry.lastUsed = time.Now()
		m.lruList.MoveToFront(elem)
		return entry.index, nil
	}

	// Evict if at capacity.
	if m.lruList.Len() >= m.maxOpen {
		if err := m.evictOldestLocked(); err != nil {
			return nil, fmt.Errorf("evicting oldest: %w", err)
		}
	}

	// Open new user database.
	dbPath := filepath.Join(m.usersDir, userID+".db")
	idx, err := newUserIndex(dbPath, userID)
	if err != nil {
		return nil, fmt.Errorf("opening user index %q: %w", userID, err)
	}

	entry := &lruEntry{
		userID:   userID,
		index:    idx,
		lastUsed: time.Now(),
	}
	elem := m.lruList.PushFront(entry)
	m.cache[userID] = elem

	return idx, nil
}

// CloseUser closes a specific user's database and removes it from the cache.
func (m *MetadataManager) CloseUser(userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	elem, ok := m.cache[userID]
	if !ok {
		return nil
	}

	entry := elem.Value.(*lruEntry)
	m.lruList.Remove(elem)
	delete(m.cache, userID)
	return entry.index.Close()
}

// IndexObject opens the user's database and indexes the object.
func (m *MetadataManager) IndexObject(userID string, obj StoreObject) error {
	idx, err := m.OpenUser(userID)
	if err != nil {
		return err
	}
	obj.UserID = userID
	return idx.IndexObject(obj)
}

// IndexMail opens the user's database and indexes a mail message.
func (m *MetadataManager) IndexMail(userID, guid, subject, sender string, receivedAt, size int64) error {
	idx, err := m.OpenUser(userID)
	if err != nil {
		return err
	}
	return idx.IndexMail(guid, subject, sender, receivedAt, size)
}

// IndexMailTags opens the user's database and replaces all tags for a mail message.
func (m *MetadataManager) IndexMailTags(userID, mailGUID string, tags []string) error {
	idx, err := m.OpenUser(userID)
	if err != nil {
		return err
	}
	return idx.IndexMailTags(mailGUID, tags)
}

// DeleteMail opens the user's database and removes a mail's derived-index rows.
func (m *MetadataManager) DeleteMail(userID, mailGUID string) error {
	idx, err := m.OpenUser(userID)
	if err != nil {
		return err
	}
	return idx.DeleteMail(mailGUID)
}

// QueryByTag opens the user's database and returns all mail GUIDs with the given tag.
func (m *MetadataManager) QueryByTag(userID, tag string) ([]string, error) {
	idx, err := m.OpenUser(userID)
	if err != nil {
		return nil, err
	}
	return idx.QueryByTag(tag)
}

// QueryByType opens the user's database and queries by document type.
func (m *MetadataManager) QueryByType(userID string, docType int) ([]StoreObject, error) {
	idx, err := m.OpenUser(userID)
	if err != nil {
		return nil, err
	}
	return idx.QueryByType(docType)
}

// QueryByDateRange opens the user's database and queries by date range.
func (m *MetadataManager) QueryByDateRange(userID string, docType int, start, end time.Time) ([]StoreObject, error) {
	idx, err := m.OpenUser(userID)
	if err != nil {
		return nil, err
	}
	return idx.QueryByDateRange(docType, start, end)
}

// OpenCount returns the number of currently open user databases.
func (m *MetadataManager) OpenCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lruList.Len()
}

// Close closes all open user databases and the system database.
func (m *MetadataManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error

	for m.lruList.Len() > 0 {
		elem := m.lruList.Back()
		entry := elem.Value.(*lruEntry)
		if err := entry.index.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing user %q: %w", entry.userID, err))
		}
		m.lruList.Remove(elem)
		delete(m.cache, entry.userID)
	}

	if err := m.System.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing system index: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("metadata manager close errors: %v", errs)
	}
	return nil
}

func (m *MetadataManager) evictOldestLocked() error {
	elem := m.lruList.Back()
	if elem == nil {
		return nil
	}
	entry := elem.Value.(*lruEntry)
	m.lruList.Remove(elem)
	delete(m.cache, entry.userID)
	return entry.index.Close()
}
