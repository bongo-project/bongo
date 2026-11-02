package store

import (
	"database/sql"
	"fmt"
)

// SetMailHeaders records the header-derived view columns of an indexed mail.
// It is a separate UPDATE so IndexMail, which re-runs on every flag or tag
// change, never resets them. An empty listID is stored as NULL.
func (u *UserIndex) SetMailHeaders(guid string, v MailHeaderValues) error {
	toMe := 0
	if v.ToMe {
		toMe = 1
	}
	var listID, listName any
	if v.ListID != "" {
		listID, listName = v.ListID, v.ListName
	}
	if _, err := u.db.Exec(
		`UPDATE maildocument SET to_me = ?, sender_addr = ?, list_id = ?, list_name = ? WHERE guid = ?`,
		toMe, v.SenderAddr, listID, listName, guid); err != nil {
		return fmt.Errorf("setting mail headers: %w", err)
	}
	return nil
}

// PendingHeaders returns up to limit guids whose header columns have not been
// derived yet and which sort after afterGUID.
func (u *UserIndex) PendingHeaders(afterGUID string, limit int) ([]string, error) {
	return u.queryGUIDs(`SELECT guid FROM maildocument WHERE to_me IS NULL AND guid > ? ORDER BY guid LIMIT ?`,
		afterGUID, limit)
}

// QueryToMe returns the guids of mail addressed to the user, newest first.
func (u *UserIndex) QueryToMe() ([]string, error) {
	return u.queryGUIDs(`SELECT guid FROM maildocument WHERE to_me = 1 ORDER BY received_at DESC, guid`)
}

// QueryByListID returns the guids of mail carrying the given list id, newest first.
func (u *UserIndex) QueryByListID(listID string) ([]string, error) {
	return u.queryGUIDs(`SELECT guid FROM maildocument WHERE list_id = ? ORDER BY received_at DESC, guid`, listID)
}

// QueryBySenders returns the guids whose normalised sender address is in
// senders. It streams every sender once and filters in Go rather than building
// an IN list, which would hit SQLite's variable limit for a large contact book.
func (u *UserIndex) QueryBySenders(senders map[string]bool) ([]string, error) {
	if len(senders) == 0 {
		return nil, nil
	}
	rows, err := u.db.Query(`SELECT guid, sender_addr FROM maildocument WHERE sender_addr IS NOT NULL AND sender_addr <> ''`)
	if err != nil {
		return nil, fmt.Errorf("querying senders: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var guid, addr string
		if err := rows.Scan(&guid, &addr); err != nil {
			return nil, fmt.Errorf("scanning sender: %w", err)
		}
		if senders[addr] {
			out = append(out, guid)
		}
	}
	return out, rows.Err()
}

// ListGroup is one mailing list found in the index.
type ListGroup struct {
	ID string
	// Name is the list's display name from its most recent message that had one.
	Name  string
	GUIDs []string
}

// ListGroups returns every mailing list with its message guids. The guid
// counts are before any mailbox exclusion: the index has no mailbox column.
func (u *UserIndex) ListGroups() ([]ListGroup, error) {
	rows, err := u.db.Query(`
		SELECT list_id, COALESCE(list_name, ''), guid FROM maildocument
		WHERE list_id IS NOT NULL ORDER BY list_id, received_at DESC, guid`)
	if err != nil {
		return nil, fmt.Errorf("querying list groups: %w", err)
	}
	defer rows.Close()
	var (
		out []ListGroup
		cur *ListGroup
	)
	for rows.Next() {
		var id, name, guid string
		if err := rows.Scan(&id, &name, &guid); err != nil {
			return nil, fmt.Errorf("scanning list group: %w", err)
		}
		if cur == nil || cur.ID != id {
			out = append(out, ListGroup{ID: id})
			cur = &out[len(out)-1]
		}
		if cur.Name == "" {
			cur.Name = name
		}
		cur.GUIDs = append(cur.GUIDs, guid)
	}
	return out, rows.Err()
}

func (u *UserIndex) queryGUIDs(query string, args ...any) ([]string, error) {
	rows, err := u.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying guids: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetMailHeaders opens the user's database and records a mail's header columns.
func (m *MetadataManager) SetMailHeaders(userID, guid string, v MailHeaderValues) error {
	idx, err := m.OpenUser(userID)
	if err != nil {
		return err
	}
	return idx.SetMailHeaders(guid, v)
}

// PendingHeaders opens the user's database and lists mail awaiting header columns.
func (m *MetadataManager) PendingHeaders(userID, afterGUID string, limit int) ([]string, error) {
	idx, err := m.OpenUser(userID)
	if err != nil {
		return nil, err
	}
	return idx.PendingHeaders(afterGUID, limit)
}

// QueryToMe opens the user's database and returns the guids of mail to the user.
func (m *MetadataManager) QueryToMe(userID string) ([]string, error) {
	idx, err := m.OpenUser(userID)
	if err != nil {
		return nil, err
	}
	return idx.QueryToMe()
}

// QueryByListID opens the user's database and returns the guids of one list's mail.
func (m *MetadataManager) QueryByListID(userID, listID string) ([]string, error) {
	idx, err := m.OpenUser(userID)
	if err != nil {
		return nil, err
	}
	return idx.QueryByListID(listID)
}

// QueryBySenders opens the user's database and returns the guids of mail sent
// from any of the given normalised addresses.
func (m *MetadataManager) QueryBySenders(userID string, senders map[string]bool) ([]string, error) {
	idx, err := m.OpenUser(userID)
	if err != nil {
		return nil, err
	}
	return idx.QueryBySenders(senders)
}

// ListGroups opens the user's database and returns the mailing lists in it.
func (m *MetadataManager) ListGroups(userID string) ([]ListGroup, error) {
	idx, err := m.OpenUser(userID)
	if err != nil {
		return nil, err
	}
	return idx.ListGroups()
}

// UserAddresses returns the normalised addresses that count as the user's own
// when deciding whether a message was sent to them: every row of the addresses
// index (primary, alias and identity), the account email from the users table,
// and enabled rows of the legacy aliases table.
//
// The account email is read from users because that table is maintained by the
// FSM on every node, whereas the addresses index is only written on the node
// that handled the request. All of these are derived tables, so a rebuild or
// backfill uses the addresses current at that time, not those at delivery.
// A catch-all target is not included: mail it receives was addressed to a
// name the user does not own.
func (m *MetadataManager) UserAddresses(userID string) (map[string]bool, error) {
	out := make(map[string]bool)
	if m.System == nil {
		return out, nil
	}
	db := m.System.DB()
	collect := func(query string) error {
		rows, err := db.Query(query, userID)
		if err != nil {
			return fmt.Errorf("reading user addresses: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var a sql.NullString
			if err := rows.Scan(&a); err != nil {
				return fmt.Errorf("scanning user address: %w", err)
			}
			if n := NormaliseAddress(a.String); n != "" {
				out[n] = true
			}
		}
		return rows.Err()
	}
	for _, q := range []string{
		`SELECT address FROM addresses WHERE user_id = ?`,
		`SELECT username FROM users WHERE id = ?`,
		`SELECT address FROM aliases WHERE target_user_id = ? AND enabled <> 0`,
	} {
		if err := collect(q); err != nil {
			return nil, err
		}
	}
	return out, nil
}
