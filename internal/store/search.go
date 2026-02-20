package store

import (
	"container/list"
	"fmt"
	"os"
	"path/filepath"
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

	mu       sync.Mutex
	cache    map[string]*list.Element
	lruList  *list.List
	maxOpen  int

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
func (sm *SearchManager) IndexMail(userID, guid string, subject, from string, to []string, body string, receivedAt int64, hasAttachment bool) error {
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

// IndexProfile indexes a user profile in the Global Address List.
func (sm *SearchManager) IndexProfile(userID, email, displayName string) error {
	doc := galDocument{
		Email:       email,
		DisplayName: displayName,
	}
	return sm.gal.Index(userID, doc)
}

// SearchGAL searches the Global Address List by prefix.
func (sm *SearchManager) SearchGAL(prefix string, limit int) ([]GALEntry, error) {
	if limit <= 0 {
		limit = 10
	}

	query := bleve.NewPrefixQuery(prefix)
	searchReq := bleve.NewSearchRequestOptions(query, limit, 0, false)
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
		entries = append(entries, entry)
	}
	return entries, nil
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
