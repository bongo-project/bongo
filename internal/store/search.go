package store

import (
	"container/list"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
)

// MailDocument is the document structure indexed in Bleve for mail search.
type MailDocument struct {
	Subject       string   `json:"subject"`
	From          string   `json:"from"`
	To            []string `json:"to"`
	Body          string   `json:"body"`
	Date          int64    `json:"date"`
	HasAttachment bool     `json:"has_attachment"`
	SpamScore     float64  `json:"spam_score"`
	Tags          []string `json:"tags"`
}

// SearchResult represents a single search hit.
type SearchResult struct {
	GUID  string
	Score float64
}

// GALEntry represents a Global Address List entry.
type GALEntry struct {
	UserID      string
	Email       string
	DisplayName string
}

// galDocument is the Bleve document for the GAL index.
type galDocument struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

// SearchManager manages per-user Bleve indexes with an LRU cache,
// plus a global GAL index.
type SearchManager struct {
	dataDir  string
	usersDir string

	mu      sync.Mutex
	cache   map[string]*list.Element
	lruList *list.List
	maxOpen int

	gal bleve.Index
}

type searchEntry struct {
	userID   string
	index    bleve.Index
	lastUsed time.Time
}

// NewSearchManager creates a SearchManager at the given data directory.
func NewSearchManager(dataDir string) (*SearchManager, error) {
	searchDir := filepath.Join(dataDir, "search")
	usersDir := filepath.Join(searchDir, "users")
	galDir := filepath.Join(searchDir, "gal")

	if err := os.MkdirAll(usersDir, 0o750); err != nil {
		return nil, fmt.Errorf("creating search dirs: %w", err)
	}

	gal, err := openOrCreateIndex(galDir, galIndexMapping())
	if err != nil {
		return nil, fmt.Errorf("opening GAL index: %w", err)
	}

	return &SearchManager{
		dataDir:  searchDir,
		usersDir: usersDir,
		cache:    make(map[string]*list.Element),
		lruList:  list.New(),
		maxOpen:  128,
		gal:      gal,
	}, nil
}

// IndexMail indexes a mail message for a user.
func (sm *SearchManager) IndexMail(userID, guid string, subject, from string, to []string, body string, receivedAt int64, hasAttachment bool, tags []string, spamScore ...float64) error {
	idx, err := sm.openUser(userID)
	if err != nil {
		return err
	}

	doc := MailDocument{
		Subject:       subject,
		From:          from,
		To:            to,
		Body:          body,
		Date:          receivedAt,
		HasAttachment: hasAttachment,
		Tags:          tags,
	}
	if len(spamScore) > 0 {
		doc.SpamScore = spamScore[0]
	}
	return idx.Index(guid, doc)
}

// Search performs a full-text search in a user's index.
func (sm *SearchManager) Search(userID, query string, limit int) ([]SearchResult, error) {
	idx, err := sm.openUser(userID)
	if err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 20
	}

	searchReq := bleve.NewSearchRequestOptions(bleve.NewQueryStringQuery(query), limit, 0, false)
	result, err := idx.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("bleve search: %w", err)
	}

	var results []SearchResult
	for _, hit := range result.Hits {
		results = append(results, SearchResult{
			GUID:  hit.ID,
			Score: hit.Score,
		})
	}
	return results, nil
}

// IndexMailPreservingBody re-indexes a message, carrying over whatever body
// text is already in the index rather than blanking it.
//
// Bleve's Index replaces a document; it does not merge. So a caller updating
// only the tags, and passing "" for the body because the body has not changed,
// silently deletes the indexed body -- after which the message is no longer
// findable by anything in its text. Tagging a message destroyed its own
// searchability.
//
// The body is read back from the index rather than re-extracted from the raw
// message on purpose: this carries forward exactly what was there, adds no
// blob read or MIME parse to the tagging path, and cannot start indexing
// bodies that were never indexed in the first place.
//
// If the body cannot be read -- unknown guid, index error -- it falls back to
// empty, which is the behaviour before this existed. Updating the tags matters
// more than preserving the body, so a read failure must not fail the caller.
func (sm *SearchManager) IndexMailPreservingBody(userID, guid string, subject, from string, to []string, receivedAt int64, hasAttachment bool, tags []string, spamScore ...float64) error {
	return sm.IndexMail(userID, guid, subject, from, to, sm.storedBody(userID, guid),
		receivedAt, hasAttachment, tags, spamScore...)
}

// storedBody returns the body text currently indexed for guid, or "".
func (sm *SearchManager) storedBody(userID, guid string) string {
	idx, err := sm.openUser(userID)
	if err != nil {
		return ""
	}
	req := bleve.NewSearchRequestOptions(bleve.NewDocIDQuery([]string{guid}), 1, 0, false)
	req.Fields = []string{"body"}
	res, err := idx.Search(req)
	if err != nil || len(res.Hits) == 0 {
		return ""
	}
	if v, ok := res.Hits[0].Fields["body"].(string); ok {
		return v
	}
	return ""
}

// IndexProfile indexes a user profile in the Global Address List.
func (sm *SearchManager) IndexProfile(userID, email, displayName string) error {
	doc := galDocument{
		Email:       email,
		DisplayName: displayName,
	}
	return sm.gal.Index(userID, doc)
}

// galOverfetch is how many hits to ask Bleve for per requested result. The
// domain filter below runs after the search, so asking for exactly limit hits
// would return fewer than limit whenever other tenants' entries sort highly.
// Bounded by galMaxFetch so a broad prefix cannot pull the whole index.
const (
	galOverfetch = 10
	galMaxFetch  = 500
)

// SearchGAL searches the Global Address List by prefix, restricted to the
// address domain of callerEmail.
//
// The scoping is the point. This index is global -- every user profile on the
// server is in it, across every hosted domain -- and it backs compose
// autocomplete, so without a filter any authenticated user could enumerate
// every address of every other tenant one prefix at a time. That directly
// contradicts the multi-domain PRD, whose stated goal is full tenant isolation
// where one tenant "cannot see or touch other tenants' users".
//
// callerEmail rather than a bare domain string is deliberate: the caller
// already has the session email to hand, and a signature taking a domain
// invites a call site that passes "" and silently restores the leak. An empty
// or domainless callerEmail returns no results, so the failure mode is an empty
// autocomplete rather than a disclosure.
//
// Single-domain deployments are unaffected: every profile shares the one
// domain, so the filter removes nothing.
func (sm *SearchManager) SearchGAL(prefix string, limit int, callerEmail string) ([]GALEntry, error) {
	if limit <= 0 {
		limit = 10
	}

	callerDomain := addressDomain(callerEmail)
	if callerDomain == "" {
		return nil, nil
	}

	fetch := limit * galOverfetch
	if fetch > galMaxFetch {
		fetch = galMaxFetch
	}

	query := bleve.NewPrefixQuery(prefix)
	searchReq := bleve.NewSearchRequestOptions(query, fetch, 0, false)
	searchReq.Fields = []string{"email", "display_name"}
	result, err := sm.gal.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("gal search: %w", err)
	}

	var entries []GALEntry
	for _, hit := range result.Hits {
		entry := GALEntry{UserID: hit.ID}
		if v, ok := hit.Fields["email"].(string); ok {
			entry.Email = v
		}
		if v, ok := hit.Fields["display_name"].(string); ok {
			entry.DisplayName = v
		}
		// Drop other tenants. A hit with no usable email is dropped too: it
		// cannot be shown to the user in any case, and a missing field must
		// never mean "unscoped".
		if addressDomain(entry.Email) != callerDomain {
			continue
		}
		entries = append(entries, entry)
		if len(entries) >= limit {
			break
		}
	}
	return entries, nil
}

// addressDomain returns the lower-cased domain part of an email address, or ""
// when there is not exactly one usable domain part.
func addressDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(email[at+1:]))
}

// ContactDocument is the document structure indexed in Bleve for contact search.
type ContactDocument struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	Company     string `json:"company"`
}

// ContactSearchResult represents a single contact search hit.
type ContactSearchResult struct {
	GUID        string
	DisplayName string
	Email       string
	Score       float64
}

// IndexContact indexes a contact for a user.
func (sm *SearchManager) IndexContact(userID, guid, displayName, email, phone, company string) error {
	idx, err := sm.openUser(userID)
	if err != nil {
		return err
	}

	doc := ContactDocument{
		DisplayName: displayName,
		Email:       email,
		Phone:       phone,
		Company:     company,
	}
	return idx.Index("contact:"+guid, doc)
}

// DeleteContactIndex removes a contact from the user's search index.
func (sm *SearchManager) DeleteContactIndex(userID, guid string) error {
	idx, err := sm.openUser(userID)
	if err != nil {
		return err
	}
	return idx.Delete("contact:" + guid)
}

// SearchContacts searches for contacts in a user's index.
func (sm *SearchManager) SearchContacts(userID, query string, limit int) ([]ContactSearchResult, error) {
	idx, err := sm.openUser(userID)
	if err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 10
	}

	q := bleve.NewQueryStringQuery(query)
	searchReq := bleve.NewSearchRequestOptions(q, limit, 0, false)
	searchReq.Fields = []string{"display_name", "email"}

	result, err := idx.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("bleve contact search: %w", err)
	}

	var results []ContactSearchResult
	for _, hit := range result.Hits {
		// Only include contact documents (prefixed with "contact:").
		if len(hit.ID) < 8 || hit.ID[:8] != "contact:" {
			continue
		}
		r := ContactSearchResult{
			GUID:  hit.ID[8:],
			Score: hit.Score,
		}
		if v, ok := hit.Fields["display_name"].(string); ok {
			r.DisplayName = v
		}
		if v, ok := hit.Fields["email"].(string); ok {
			r.Email = v
		}
		results = append(results, r)
	}
	return results, nil
}

// EventDocument is the document structure indexed in Bleve for event search.
type EventDocument struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Location    string `json:"location"`
	StartTime   int64  `json:"start_time"`
	EndTime     int64  `json:"end_time"`
}

// EventSearchResult represents a single event search hit.
type EventSearchResult struct {
	GUID  string
	Title string
	Score float64
}

// IndexEvent indexes a calendar event for a user.
func (sm *SearchManager) IndexEvent(userID, guid, title, description, location string, startTime, endTime int64) error {
	idx, err := sm.openUser(userID)
	if err != nil {
		return err
	}

	doc := EventDocument{
		Title:       title,
		Description: description,
		Location:    location,
		StartTime:   startTime,
		EndTime:     endTime,
	}
	return idx.Index("event:"+guid, doc)
}

// DeleteEventIndex removes an event from the user's search index.
func (sm *SearchManager) DeleteEventIndex(userID, guid string) error {
	idx, err := sm.openUser(userID)
	if err != nil {
		return err
	}
	return idx.Delete("event:" + guid)
}

// DeleteMailIndex removes a mail document from a user's search index. Mail is
// indexed under the bare guid (see IndexMail), so deletion uses the guid directly.
func (sm *SearchManager) DeleteMailIndex(userID, guid string) error {
	idx, err := sm.openUser(userID)
	if err != nil {
		return err
	}
	return idx.Delete(guid)
}

// SearchEvents searches for events in a user's index.
func (sm *SearchManager) SearchEvents(userID, query string, limit int) ([]EventSearchResult, error) {
	idx, err := sm.openUser(userID)
	if err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 20
	}

	q := bleve.NewQueryStringQuery(query)
	searchReq := bleve.NewSearchRequestOptions(q, limit, 0, false)
	searchReq.Fields = []string{"title"}

	result, err := idx.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("bleve event search: %w", err)
	}

	var results []EventSearchResult
	for _, hit := range result.Hits {
		if len(hit.ID) < 6 || hit.ID[:6] != "event:" {
			continue
		}
		r := EventSearchResult{
			GUID:  hit.ID[6:],
			Score: hit.Score,
		}
		if v, ok := hit.Fields["title"].(string); ok {
			r.Title = v
		}
		results = append(results, r)
	}
	return results, nil
}

// SearchWithRequest performs a search using a raw Bleve SearchRequest.
// This allows callers to build complex queries (boolean, date range, etc.).
func (sm *SearchManager) SearchWithRequest(userID string, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
	idx, err := sm.openUser(userID)
	if err != nil {
		return nil, err
	}
	return idx.Search(req)
}

// Close closes all open indexes.
func (sm *SearchManager) Close() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var errs []error
	for sm.lruList.Len() > 0 {
		elem := sm.lruList.Back()
		entry := elem.Value.(*searchEntry)
		if err := entry.index.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing search index %q: %w", entry.userID, err))
		}
		sm.lruList.Remove(elem)
		delete(sm.cache, entry.userID)
	}

	if err := sm.gal.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing GAL index: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("search manager close errors: %v", errs)
	}
	return nil
}

func (sm *SearchManager) openUser(userID string) (bleve.Index, error) {
	if !IsValidUserID(userID) {
		return nil, fmt.Errorf("invalid user id")
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	if elem, ok := sm.cache[userID]; ok {
		entry := elem.Value.(*searchEntry)
		entry.lastUsed = time.Now()
		sm.lruList.MoveToFront(elem)
		return entry.index, nil
	}

	if sm.lruList.Len() >= sm.maxOpen {
		if err := sm.evictOldestLocked(); err != nil {
			return nil, fmt.Errorf("evicting oldest search index: %w", err)
		}
	}

	idxDir := filepath.Join(sm.usersDir, userID)
	idx, err := openOrCreateIndex(idxDir, mailIndexMapping())
	if err != nil {
		return nil, fmt.Errorf("opening user search index %q: %w", userID, err)
	}

	entry := &searchEntry{
		userID:   userID,
		index:    idx,
		lastUsed: time.Now(),
	}
	elem := sm.lruList.PushFront(entry)
	sm.cache[userID] = elem

	return idx, nil
}

// DeleteUser closes the user's search index and removes its directory.
//
// Bleve holds file handles and a directory lock, so the index must leave the
// LRU cache before the directory is removed; otherwise a later flush recreates
// segment files under a half-deleted directory. Idempotent: a user that was
// never opened, or whose directory is already gone, is not an error.
func (sm *SearchManager) DeleteUser(userID string) error {
	if !IsValidUserID(userID) {
		return fmt.Errorf("invalid user id")
	}

	sm.mu.Lock()
	if elem, ok := sm.cache[userID]; ok {
		entry := elem.Value.(*searchEntry)
		sm.lruList.Remove(elem)
		delete(sm.cache, userID)
		if err := entry.index.Close(); err != nil {
			sm.mu.Unlock()
			return fmt.Errorf("closing search index %q: %w", userID, err)
		}
	}
	sm.mu.Unlock()

	idxDir := filepath.Join(sm.usersDir, userID)
	if err := os.RemoveAll(idxDir); err != nil {
		return fmt.Errorf("removing search index %q: %w", idxDir, err)
	}
	return nil
}

func (sm *SearchManager) evictOldestLocked() error {
	elem := sm.lruList.Back()
	if elem == nil {
		return nil
	}
	entry := elem.Value.(*searchEntry)
	sm.lruList.Remove(elem)
	delete(sm.cache, entry.userID)
	return entry.index.Close()
}

func openOrCreateIndex(dir string, m mapping.IndexMapping) (bleve.Index, error) {
	idx, err := bleve.Open(dir)
	if err == nil {
		return idx, nil
	}
	// Create new index.
	idx, err = bleve.New(dir, m)
	if err != nil {
		return nil, fmt.Errorf("creating bleve index at %s: %w", dir, err)
	}
	return idx, nil
}

func mailIndexMapping() mapping.IndexMapping {
	m := bleve.NewIndexMapping()

	mailMapping := bleve.NewDocumentMapping()

	textField := bleve.NewTextFieldMapping()
	keywordField := bleve.NewKeywordFieldMapping()
	dateField := bleve.NewNumericFieldMapping()
	boolField := bleve.NewBooleanFieldMapping()

	mailMapping.AddFieldMappingsAt("subject", textField)
	mailMapping.AddFieldMappingsAt("from", keywordField)
	mailMapping.AddFieldMappingsAt("to", keywordField)
	mailMapping.AddFieldMappingsAt("body", textField)
	mailMapping.AddFieldMappingsAt("date", dateField)
	mailMapping.AddFieldMappingsAt("has_attachment", boolField)

	spamScoreField := bleve.NewNumericFieldMapping()
	mailMapping.AddFieldMappingsAt("spam_score", spamScoreField)

	tagsField := bleve.NewKeywordFieldMapping()
	mailMapping.AddFieldMappingsAt("tags", tagsField)

	// Contact fields (shared index, contact docs use "contact:" prefix for ID).
	contactNameField := bleve.NewTextFieldMapping()
	contactNameField.Store = true
	contactEmailField := bleve.NewTextFieldMapping()
	contactEmailField.Store = true
	contactPhoneField := bleve.NewTextFieldMapping()
	contactCompanyField := bleve.NewTextFieldMapping()

	mailMapping.AddFieldMappingsAt("display_name", contactNameField)
	mailMapping.AddFieldMappingsAt("email", contactEmailField)
	mailMapping.AddFieldMappingsAt("phone", contactPhoneField)
	mailMapping.AddFieldMappingsAt("company", contactCompanyField)

	// Event fields (shared index, event docs use "event:" prefix for ID).
	eventTitleField := bleve.NewTextFieldMapping()
	eventTitleField.Store = true
	eventDescField := bleve.NewTextFieldMapping()
	eventLocationField := bleve.NewTextFieldMapping()
	eventStartField := bleve.NewNumericFieldMapping()
	eventEndField := bleve.NewNumericFieldMapping()

	mailMapping.AddFieldMappingsAt("title", eventTitleField)
	mailMapping.AddFieldMappingsAt("description", eventDescField)
	mailMapping.AddFieldMappingsAt("location", eventLocationField)
	mailMapping.AddFieldMappingsAt("start_time", eventStartField)
	mailMapping.AddFieldMappingsAt("end_time", eventEndField)

	m.DefaultMapping = mailMapping
	return m
}

func galIndexMapping() mapping.IndexMapping {
	m := bleve.NewIndexMapping()

	galMapping := bleve.NewDocumentMapping()

	emailField := bleve.NewTextFieldMapping()
	emailField.Store = true
	nameField := bleve.NewTextFieldMapping()
	nameField.Store = true

	galMapping.AddFieldMappingsAt("email", emailField)
	galMapping.AddFieldMappingsAt("display_name", nameField)

	m.DefaultMapping = galMapping
	return m
}
