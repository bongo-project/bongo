package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/tagname"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// tagStore is the subset of store.TagStore used by TagsHandler.
// Defined as an interface so tests can inject fakes.
type tagStore interface {
	ListTags(userID string) ([]store.TagWithCount, error)
	GetTag(userID, name string) (*bongov1.TagMeta, error)
	CreateTag(userID, name string) (*bongov1.TagMeta, error)
	RenameTag(userID, oldName, newName string) (*bongov1.TagMeta, error)
	DeleteTag(userID, name string) error
	UpdateTagColour(userID, name string, colourIndex int32) (*bongov1.TagMeta, error)
}

// tagPebbleStore is the subset of store.PebbleStore used by TagsHandler
// for scanning mail meta during tag deletion.
type tagPebbleStore interface {
	PrefixScan(prefix []byte) (store.Iterator, error)
}

// tagRaftApplier is the subset of store.RaftApplier used by TagsHandler.
type tagRaftApplier interface {
	ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error
}

// TagsHandler handles the /api/tags REST endpoints.
type TagsHandler struct {
	tags    tagStore
	pebble  tagPebbleStore
	applier tagRaftApplier
}

// NewTagsHandler creates a TagsHandler.
func NewTagsHandler(tags *store.TagStore, pebble *store.PebbleStore, applier store.RaftApplier) *TagsHandler {
	return &TagsHandler{tags: tags, pebble: pebble, applier: applier}
}

// tagResponse is the JSON representation of a tag returned by the API.
type tagResponse struct {
	Name        string `json:"name"`
	ColourIndex int32  `json:"colour_index"`
	SortOrder   int32  `json:"sort_order"`
	CreatedAt   int64  `json:"created_at"`
	Count       int    `json:"count"`
}

// createTagRequest is the JSON body for POST /api/tags.
type createTagRequest struct {
	Name string `json:"name"`
}

// updateTagRequest is the JSON body for PUT /api/tags/{name}.
// Both fields are optional: omit name to skip renaming, omit colour_index
// (or set to -1) to skip recolouring.
type updateTagRequest struct {
	Name        *string `json:"name"`
	ColourIndex *int32  `json:"colour_index"`
}

// ListTags handles GET /api/tags.
// Returns a JSON array of all tags for the authenticated user with message counts.
func (h *TagsHandler) ListTags(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	tags, err := h.tags.ListTags(userID)
	if err != nil {
		http.Error(w, "failed to list tags", http.StatusInternalServerError)
		return
	}

	resp := make([]tagResponse, len(tags))
	for i, twc := range tags {
		resp[i] = tagResponse{
			Name:        twc.Tag.Name,
			ColourIndex: twc.Tag.ColourIndex,
			SortOrder:   twc.Tag.SortOrder,
			CreatedAt:   twc.Tag.CreatedAt,
			Count:       twc.Count,
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// CreateTag handles POST /api/tags.
// Expects JSON body: {"name": "tag-name"}
// Returns 201 Created with the new tag on success.
func (h *TagsHandler) CreateTag(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	var req createTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	name, err := normaliseTagInput(req.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	meta, err := h.tags.CreateTag(userID, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusCreated, tagResponse{
		Name:        meta.Name,
		ColourIndex: meta.ColourIndex,
		SortOrder:   meta.SortOrder,
		CreatedAt:   meta.CreatedAt,
		Count:       0,
	})
}

// UpdateTag handles PUT /api/tags/{name}.
// Accepts optional fields: name (rename) and colour_index (recolour).
// Returns 200 OK with the updated tag.
func (h *TagsHandler) UpdateTag(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	currentName := chi.URLParam(r, "name")

	var req updateTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.Name == nil && req.ColourIndex == nil {
		http.Error(w, "at least one of name or colour_index is required", http.StatusBadRequest)
		return
	}

	// Verify the tag exists before making any changes.
	_, err := h.tags.GetTag(userID, currentName)
	if err != nil {
		http.Error(w, "tag not found", http.StatusNotFound)
		return
	}

	activeName := currentName

	// Rename if a new name was provided.
	if req.Name != nil {
		name, err := normaliseTagInput(*req.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		req.Name = &name
	}
	if req.Name != nil && *req.Name != currentName {
		meta, err := h.tags.RenameTag(userID, currentName, *req.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		activeName = meta.Name
	}

	// Recolour if a colour_index was provided.
	if req.ColourIndex != nil {
		_, err := h.tags.UpdateTagColour(userID, activeName, *req.ColourIndex)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	// Read back the final state to return accurate data.
	meta, err := h.tags.GetTag(userID, activeName)
	if err != nil {
		http.Error(w, "failed to read updated tag", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, tagResponse{
		Name:        meta.Name,
		ColourIndex: meta.ColourIndex,
		SortOrder:   meta.SortOrder,
		CreatedAt:   meta.CreatedAt,
	})
}

// DeleteTag handles DELETE /api/tags/{name}.
// Removes the tag definition and strips the tag from all messages that carry it.
func (h *TagsHandler) DeleteTag(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	name := chi.URLParam(r, "name")

	// Confirm the tag exists before doing anything.
	if _, err := h.tags.GetTag(userID, name); err != nil {
		http.Error(w, "tag not found", http.StatusNotFound)
		return
	}

	// Remove the tag from all messages that have it.
	if err := h.removeTagFromAllMessages(userID, name); err != nil {
		http.Error(w, "failed to remove tag from messages", http.StatusInternalServerError)
		return
	}

	// Delete the tag definition.
	if err := h.tags.DeleteTag(userID, name); err != nil {
		http.Error(w, "failed to delete tag", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ShowTagSettings renders the tag management settings page (GET /settings/tags).
func (h *TagsHandler) ShowTagSettings(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())

	tagsWithCount, err := h.tags.ListTags(userID)
	if err != nil {
		http.Error(w, "failed to load tags", http.StatusInternalServerError)
		return
	}

	items := make([]templates.TagSettingItem, len(tagsWithCount))
	for i, twc := range tagsWithCount {
		items[i] = templates.TagSettingItem{
			Name:        twc.Tag.Name,
			ColourIndex: twc.Tag.ColourIndex,
			Count:       twc.Count,
		}
	}

	flash := r.URL.Query().Get("flash")
	data := templates.TagSettingsData{
		Tags:  items,
		Email: email,
		Flash: flash,
	}
	templ.Handler(templates.TagSettingsPage(data)).ServeHTTP(w, r)
}

// removeTagFromAllMessages scans all MailMeta records for the user and removes
// the named tag from any message that carries it, persisting each change via Raft.
func (h *TagsHandler) removeTagFromAllMessages(userID, tagName string) error {
	if h.pebble == nil || h.applier == nil {
		// No storage available (e.g. in tests without pebble). Skip silently.
		return nil
	}

	prefix := []byte(fmt.Sprintf("/user/%s/mail/", userID))
	iter, err := h.pebble.PrefixScan(prefix)
	if err != nil {
		return fmt.Errorf("scanning mail meta: %w", err)
	}
	defer iter.Close()

	for iter.Next() {
		key := iter.Key()

		val, err := iter.Value()
		if err != nil {
			return fmt.Errorf("reading mail meta value: %w", err)
		}

		var meta bongov1.MailMeta
		if err := proto.Unmarshal(val, &meta); err != nil {
			// Skip malformed records.
			continue
		}

		// Check whether this message has the tag.
		hasTag := false
		for _, t := range meta.Tags {
			if t == tagName {
				hasTag = true
				break
			}
		}
		if !hasTag {
			continue
		}

		// Remove the tag in-place.
		if err := store.RemoveTags(&meta, []string{tagName}); err != nil {
			return fmt.Errorf("removing tag from meta: %w", err)
		}

		updated, err := proto.Marshal(&meta)
		if err != nil {
			return fmt.Errorf("marshaling updated meta: %w", err)
		}

		cmd := &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   key,
			Value: updated,
		}
		if err := h.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
			return fmt.Errorf("applying remove tag command: %w", err)
		}
	}

	return nil
}

// normaliseTagInput folds a typed tag name into the stored form ("Bob" becomes
// "bob", "My Tag" becomes "my-tag") and validates the result. A name that folds
// to nothing is rejected with the validation error for what was typed.
func normaliseTagInput(name string) (string, error) {
	folded := tagname.Normalise(name)
	if folded == "" {
		return "", store.ValidateTagName(name)
	}
	return folded, store.ValidateTagName(folded)
}

// normaliseTagInputs applies normaliseTagInput to each name.
func normaliseTagInputs(names []string) ([]string, error) {
	out := make([]string, 0, len(names))
	for _, name := range names {
		folded, err := normaliseTagInput(name)
		if err != nil {
			return nil, err
		}
		out = append(out, folded)
	}
	return out, nil
}
