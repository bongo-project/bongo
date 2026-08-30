package handlers

import (
	"log/slog"
	"net/http"

	"github.com/bongo-project/bongo/internal/store"
)

// AddressRepairHandler serves the orphaned-address control-plane endpoints.
// Mounted on /internal/ behind RequireInternalAuth, like the queue handler.
type AddressRepairHandler struct {
	pebble    *store.PebbleStore
	addresses *store.AddressStore
	logger    *slog.Logger
}

func NewAddressRepairHandler(pebble *store.PebbleStore, addresses *store.AddressStore, logger *slog.Logger) *AddressRepairHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &AddressRepairHandler{pebble: pebble, addresses: addresses, logger: logger}
}

// List reports address-index records whose owning user no longer exists.
// Read-only: this is the dry run.
func (h *AddressRepairHandler) List(w http.ResponseWriter, r *http.Request) {
	orphans, err := store.FindOrphanedAddresses(h.pebble)
	if err != nil {
		h.logger.Error("address repair: scan failed", "error", err)
		http.Error(w, "failed to scan address index", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(orphans), "orphans": orphans})
}

// Repair deletes the orphaned records and reports what it removed.
func (h *AddressRepairHandler) Repair(w http.ResponseWriter, r *http.Request) {
	if h.addresses == nil {
		http.Error(w, "address store not available", http.StatusInternalServerError)
		return
	}
	orphans, err := store.FindOrphanedAddresses(h.pebble)
	if err != nil {
		h.logger.Error("address repair: scan failed", "error", err)
		http.Error(w, "failed to scan address index", http.StatusInternalServerError)
		return
	}
	deleted, err := store.DeleteOrphanedAddresses(h.pebble, h.addresses, orphans)
	for _, addr := range deleted {
		h.logger.Warn("address repair: removed orphaned address index record", "address", addr)
	}
	if err != nil {
		h.logger.Error("address repair: delete failed", "deleted", len(deleted), "error", err)
		http.Error(w, "failed to delete orphaned addresses", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "count": len(deleted)})
}
