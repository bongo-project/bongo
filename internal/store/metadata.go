package store

import (
	"database/sql"
	"fmt"
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

// StoreObject represents a row in the storeobject table.
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

// MetadataIndex provides a SQLite-backed metadata index for store objects.
type MetadataIndex struct {
	db *sql.DB
}

// NewMetadataIndex opens or creates a SQLite database at dbPath and creates
// the schema tables if they don't exist.
func NewMetadataIndex(dbPath string) (*MetadataIndex, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite: %w", err)
	}

	// Enable WAL mode for better concurrent read performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting WAL mode: %w", err)
	}

	idx := &MetadataIndex{db: db}
	if err := idx.createSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}

	return idx, nil
}

func (m *MetadataIndex) createSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS storeobject (
		guid TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		collection_guid TEXT,
		type INTEGER NOT NULL,
		flags INTEGER DEFAULT 0,
		size INTEGER,
		created_at INTEGER,
		modified_at INTEGER
	);

	CREATE INDEX IF NOT EXISTS idx_storeobject_user_type
		ON storeobject(user_id, type);

	CREATE INDEX IF NOT EXISTS idx_storeobject_user_created
		ON storeobject(user_id, type, created_at);

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
		user_id TEXT NOT NULL,
		subject TEXT,
		last_date INTEGER,
		message_count INTEGER,
		unread_count INTEGER
	);

	CREATE INDEX IF NOT EXISTS idx_conversation_user
		ON conversation(user_id, last_date);

	CREATE TABLE IF NOT EXISTS accesscontrol (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		resource_guid TEXT NOT NULL,
		principal_type INTEGER,
		principal_id TEXT,
		privileges INTEGER,
		deny INTEGER DEFAULT 0
	);

	CREATE INDEX IF NOT EXISTS idx_accesscontrol_resource
		ON accesscontrol(resource_guid);

	CREATE INDEX IF NOT EXISTS idx_accesscontrol_principal
		ON accesscontrol(principal_id);
	`

	_, err := m.db.Exec(schema)
	return err
}

// IndexObject upserts a StoreObject into the metadata index.
func (m *MetadataIndex) IndexObject(obj StoreObject) error {
	_, err := m.db.Exec(`
		INSERT INTO storeobject (guid, user_id, collection_guid, type, flags, size, created_at, modified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(guid) DO UPDATE SET
			user_id = excluded.user_id,
			collection_guid = excluded.collection_guid,
			type = excluded.type,
			flags = excluded.flags,
			size = excluded.size,
			modified_at = excluded.modified_at
	`,
		obj.GUID,
		obj.UserID,
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

// QueryByType returns all store objects for a user with the given document type.
func (m *MetadataIndex) QueryByType(userID string, docType int) ([]StoreObject, error) {
	rows, err := m.db.Query(`
		SELECT guid, user_id, collection_guid, type, flags, size, created_at, modified_at
		FROM storeobject
		WHERE user_id = ? AND type = ?
		ORDER BY created_at DESC
	`, userID, docType)
	if err != nil {
		return nil, fmt.Errorf("querying by type: %w", err)
	}
	defer rows.Close()

	return scanStoreObjects(rows)
}

// QueryByDateRange returns store objects for a user and type within a time range.
func (m *MetadataIndex) QueryByDateRange(userID string, docType int, start, end time.Time) ([]StoreObject, error) {
	rows, err := m.db.Query(`
		SELECT guid, user_id, collection_guid, type, flags, size, created_at, modified_at
		FROM storeobject
		WHERE user_id = ? AND type = ? AND created_at >= ? AND created_at <= ?
		ORDER BY created_at DESC
	`, userID, docType, start.Unix(), end.Unix())
	if err != nil {
		return nil, fmt.Errorf("querying by date range: %w", err)
	}
	defer rows.Close()

	return scanStoreObjects(rows)
}

// Close closes the SQLite database.
func (m *MetadataIndex) Close() error {
	if err := m.db.Close(); err != nil {
		return fmt.Errorf("closing metadata index: %w", err)
	}
	return nil
}

func scanStoreObjects(rows *sql.Rows) ([]StoreObject, error) {
	var objects []StoreObject
	for rows.Next() {
		var obj StoreObject
		var collGUID sql.NullString
		var createdAt, modifiedAt int64

		if err := rows.Scan(
			&obj.GUID,
			&obj.UserID,
			&collGUID,
			&obj.Type,
			&obj.Flags,
			&obj.Size,
			&createdAt,
			&modifiedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning store object: %w", err)
		}

		obj.CollectionGUID = collGUID.String
		obj.CreatedAt = time.Unix(createdAt, 0)
		obj.ModifiedAt = time.Unix(modifiedAt, 0)
		objects = append(objects, obj)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rows: %w", err)
	}

	return objects, nil
}
