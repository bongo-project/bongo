package handlers

import (
	"log/slog"
	"net/http"

	"github.com/bongo-project/bongo/internal/store"
)

// DirectoryListHandler serves read-only listings of accounts, hosted domains
// and address aliases for the CLI. Mounted on the control listener only,
// behind RequireInternalAuth; it never writes.
//
// Data comes from Pebble, not the per-node SQLite users table, so a follower
// lists the same accounts as the leader (up to Raft apply lag). Each response
// is {"count":n,"truncated":bool,"items":[...]} and holds at most
// store.DirectoryListLimit rows, sorted, so a pathological store cannot
// produce an unbounded body. Rows are dedicated DTOs: no credential, TOTP,
// recovery, DKIM key or session field is ever copied into them.
type DirectoryListHandler struct {
	pebble *store.PebbleStore
	logger *slog.Logger
}

func NewDirectoryListHandler(pebble *store.PebbleStore, logger *slog.Logger) *DirectoryListHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &DirectoryListHandler{pebble: pebble, logger: logger}
}

// domainFilter validates ?domain= inside the handler, after authentication,
// so an unsigned request gets 401 rather than 400.
func (h *DirectoryListHandler) domainFilter(w http.ResponseWriter, r *http.Request) (string, bool) {
	d, ok := store.NormaliseDirectoryDomain(r.URL.Query().Get("domain"))
	if !ok {
		http.Error(w, "invalid domain filter", http.StatusBadRequest)
	}
	return d, ok
}

func (h *DirectoryListHandler) respond(w http.ResponseWriter, what string, count int, items any, truncated bool, err error) {
	if err != nil {
		h.logger.Error("directory list failed", "list", what, "error", err)
		http.Error(w, "failed to list "+what, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": count, "truncated": truncated, "items": items})
}

// Users lists accounts, optionally limited to one email domain.
func (h *DirectoryListHandler) Users(w http.ResponseWriter, r *http.Request) {
	domain, ok := h.domainFilter(w, r)
	if !ok {
		return
	}
	items, trunc, err := store.ListUserSummaries(h.pebble, domain)
	h.respond(w, "users", len(items), orEmpty(items), trunc, err)
}

// Domains lists hosted domains.
func (h *DirectoryListHandler) Domains(w http.ResponseWriter, r *http.Request) {
	items, trunc, err := store.ListDomainSummaries(h.pebble)
	h.respond(w, "domains", len(items), orEmpty(items), trunc, err)
}

// AddressAliases lists address aliases (not domain aliases), optionally
// limited to one email domain.
func (h *DirectoryListHandler) AddressAliases(w http.ResponseWriter, r *http.Request) {
	domain, ok := h.domainFilter(w, r)
	if !ok {
		return
	}
	items, trunc, err := store.ListAddressAliases(h.pebble, domain)
	h.respond(w, "aliases", len(items), orEmpty(items), trunc, err)
}

// orEmpty makes a nil slice encode as [] rather than null.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
