package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/a-h/templ"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	"github.com/bongo-go/bongo-go/internal/web/templates"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

const searchResultsPerPage = 25

// SearchHandler handles search endpoints.
type SearchHandler struct {
	pebble    *store.PebbleStore
	mailboxes *store.MailboxStore
	search    *store.SearchManager
}

// NewSearchHandler creates a SearchHandler.
func NewSearchHandler(pebble *store.PebbleStore, mailboxes *store.MailboxStore, search *store.SearchManager) *SearchHandler {
	return &SearchHandler{
		pebble:    pebble,
		mailboxes: mailboxes,
		search:    search,
	}
}

// Search performs a full-text search.
func (h *SearchHandler) Search(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	query := r.URL.Query().Get("q")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	var results []templates.SearchResult
	totalPages := 1

	if query != "" && h.search != nil {
		hits, err := h.search.Search(userID, query, 200)
		if err == nil {
			total := len(hits)
			totalPages = (total + searchResultsPerPage - 1) / searchResultsPerPage
			if totalPages < 1 {
				totalPages = 1
			}

			offset := (page - 1) * searchResultsPerPage
			end := offset + searchResultsPerPage
			if end > total {
				end = total
			}
			if offset < total {
				for _, hit := range hits[offset:end] {
					result := h.buildSearchResult(userID, hit.GUID)
					if result != nil {
						results = append(results, *result)
					}
				}
			}
		}
	}

	templ.Handler(templates.SearchPage(query, results, page, totalPages)).ServeHTTP(w, r)
}

func (h *SearchHandler) buildSearchResult(userID, guid string) *templates.SearchResult {
	key := fmt.Sprintf("/user/%s/mail/%s", userID, guid)
	data, err := h.pebble.Get([]byte(key))
	if err != nil {
		return nil
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		return nil
	}

	// Find which mailbox this message is in.
	mailbox, uid := h.findMessageLocation(userID, guid)

	return &templates.SearchResult{
		UID:     uid,
		Mailbox: mailbox,
		From:    meta.From,
		Subject: meta.Subject,
		Date:    time.Unix(meta.ReceivedAt, 0).Format("Jan 2, 2006"),
	}
}

func (h *SearchHandler) findMessageLocation(userID, guid string) (string, uint32) {
	if h.mailboxes == nil {
		return "INBOX", 0
	}
	// Check standard mailboxes first.
	for _, mb := range store.DefaultMailboxes {
		msgs, err := h.mailboxes.ListMessages(userID, mb)
		if err != nil {
			continue
		}
		for _, msg := range msgs {
			if msg.MailGuid == guid {
				return mb, msg.Uid
			}
		}
	}
	return "INBOX", 0
}
