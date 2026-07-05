package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jhillyerd/enmime"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/rules"
	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	"github.com/bongo-go/bongo-go/internal/web/templates"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// RulesHandler handles filter rule endpoints.
type RulesHandler struct {
	rules     *rules.RuleStore
	mailboxes *store.MailboxStore
	pebble    *store.PebbleStore
	blobs     *store.BlobStore
	engine    *rules.Engine
}

// NewRulesHandler creates a RulesHandler.
func NewRulesHandler(rs *rules.RuleStore, mb *store.MailboxStore, pebble *store.PebbleStore, blobs *store.BlobStore) *RulesHandler {
	return &RulesHandler{
		rules:     rs,
		mailboxes: mb,
		pebble:    pebble,
		blobs:     blobs,
		engine:    rules.NewEngine(),
	}
}

// ListRules renders the rules list page.
func (h *RulesHandler) ListRules(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	ruleList, err := h.rules.List(userID)
	if err != nil {
		http.Error(w, "Failed to load rules", http.StatusInternalServerError)
		return
	}
	flash := r.URL.Query().Get("flash")

	infos := make([]templates.RuleInfo, len(ruleList))
	for i, rule := range ruleList {
		infos[i] = ruleToInfo(rule)
	}

	templ.Handler(templates.RulesListPage(infos, email, flash)).ServeHTTP(w, r)
}

// NewRule renders the new rule form.
func (h *RulesHandler) NewRule(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	mailboxes := h.listMailboxNames(userID)

	info := templates.RuleInfo{
		Enabled: true,
		Conditions: []templates.ConditionInfo{
			{Field: "from", Operator: "contains"},
		},
		Action: templates.ActionInfo{Type: "move"},
	}

	templ.Handler(templates.RuleFormPage(info, mailboxes, true, email, "")).ServeHTTP(w, r)
}

// CreateRule processes the new rule form.
func (h *RulesHandler) CreateRule(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	rule := h.parseRuleForm(r)
	rule.Id = uuid.New().String()
	rule.CreatedAt = time.Now().Unix()
	rule.UpdatedAt = rule.CreatedAt

	// Set priority to max+1.
	existing, _ := h.rules.List(userID)
	var maxPriority int32
	for _, ex := range existing {
		if ex.Priority > maxPriority {
			maxPriority = ex.Priority
		}
	}
	rule.Priority = maxPriority + 1

	if err := h.rules.Save(userID, rule); err != nil {
		http.Error(w, "Failed to save rule", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/settings/rules?flash=Rule+created", http.StatusSeeOther)
}

// EditRule renders the edit rule form.
func (h *RulesHandler) EditRule(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	ruleID := chi.URLParam(r, "id")

	rule, err := h.rules.Get(userID, ruleID)
	if err != nil {
		http.Error(w, "Rule not found", http.StatusNotFound)
		return
	}

	mailboxes := h.listMailboxNames(userID)
	info := ruleToInfo(rule)

	templ.Handler(templates.RuleFormPage(info, mailboxes, false, email, "")).ServeHTTP(w, r)
}

// UpdateRule processes the edit rule form.
func (h *RulesHandler) UpdateRule(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	ruleID := chi.URLParam(r, "id")

	existing, err := h.rules.Get(userID, ruleID)
	if err != nil {
		http.Error(w, "Rule not found", http.StatusNotFound)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	rule := h.parseRuleForm(r)
	rule.Id = ruleID
	rule.Priority = existing.Priority
	rule.CreatedAt = existing.CreatedAt
	rule.UpdatedAt = time.Now().Unix()

	if err := h.rules.Save(userID, rule); err != nil {
		http.Error(w, "Failed to save rule", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/settings/rules?flash=Rule+updated", http.StatusSeeOther)
}

// DeleteRule removes a rule.
func (h *RulesHandler) DeleteRule(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	ruleID := chi.URLParam(r, "id")

	if err := h.rules.Delete(userID, ruleID); err != nil {
		http.Error(w, "Failed to delete rule", http.StatusInternalServerError)
		return
	}

	// For HTMX delete, return empty to remove the row.
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Redirect(w, r, "/settings/rules?flash=Rule+deleted", http.StatusSeeOther)
}

// ReorderRules updates rule priorities.
func (h *RulesHandler) ReorderRules(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	var ruleIDs []string
	if err := json.NewDecoder(r.Body).Decode(&ruleIDs); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := h.rules.Reorder(userID, ruleIDs); err != nil {
		http.Error(w, "Failed to reorder", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// TestRule tests a rule against recent messages.
func (h *RulesHandler) TestRule(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	ruleID := chi.URLParam(r, "id")

	rule, err := h.rules.Get(userID, ruleID)
	if err != nil {
		http.Error(w, "Rule not found", http.StatusNotFound)
		return
	}

	results := h.testRuleAgainstMail(userID, rule)
	templ.Handler(templates.RuleTestResults(ruleID, results.Matches, results.Total, results.Samples)).ServeHTTP(w, r)
}

type testResults struct {
	Matches int
	Total   int
	Samples []templates.MailSample
}

func (h *RulesHandler) testRuleAgainstMail(userID string, rule *bongov1.FilterRule) testResults {
	// Load recent messages from INBOX.
	messages, _, _ := h.mailboxes.ListMessagesPaged(userID, "INBOX", 0, 100)

	var results testResults
	results.Total = len(messages)

	for _, msg := range messages {
		// Load MailMeta from Pebble.
		key := fmt.Sprintf("/user/%s/mail/%s", userID, msg.MailGuid)
		data, err := h.pebble.Get([]byte(key))
		if err != nil {
			continue
		}

		var meta bongov1.MailMeta
		if err := proto.Unmarshal(data, &meta); err != nil {
			continue
		}

		// Extract body text from raw blob for body matching.
		var bodyText string
		if meta.RawMessage != nil && meta.RawMessage.Hash != "" {
			rc, err := h.blobs.Get(meta.RawMessage.Hash)
			if err == nil {
				rawBytes, err := io.ReadAll(rc)
				if closeErr := rc.Close(); err == nil {
					err = closeErr
				}
				if err == nil {
					bodyText = extractBodyText(rawBytes)
				}
			}
		}

		action := h.engine.Evaluate([]*bongov1.FilterRule{rule}, &meta, nil, bodyText)
		if action != nil {
			results.Matches++
			if len(results.Samples) < 10 {
				results.Samples = append(results.Samples, templates.MailSample{
					Subject: meta.Subject,
					From:    meta.From,
					Date:    time.Unix(meta.ReceivedAt, 0).Format("2006-01-02"),
				})
			}
		}
	}

	return results
}

func (h *RulesHandler) parseRuleForm(r *http.Request) *bongov1.FilterRule {
	rule := &bongov1.FilterRule{
		Name:           r.FormValue("name"),
		Enabled:        r.FormValue("enabled") == "on" || r.FormValue("enabled") == "true",
		StopProcessing: r.FormValue("stop_processing") == "on",
	}

	// Parse conditions.
	fields := r.Form["cond_field"]
	operators := r.Form["cond_operator"]
	values := r.Form["cond_value"]

	for i := 0; i < len(fields); i++ {
		if i >= len(operators) || i >= len(values) {
			break
		}
		if fields[i] == "" {
			continue
		}
		rule.Conditions = append(rule.Conditions, &bongov1.Condition{
			Field:    fields[i],
			Operator: operators[i],
			Value:    values[i],
		})
	}

	// Parse action.
	actionType := r.FormValue("action_type")
	target := r.FormValue("action_target")
	if actionType == "forward" {
		target = r.FormValue("action_target_forward")
	}
	rule.Action = &bongov1.FilterAction{
		Type:   actionType,
		Target: target,
		Tags:   r.Form["action_tag"],
	}

	return rule
}

func (h *RulesHandler) listMailboxNames(userID string) []string {
	mailboxes, err := h.mailboxes.ListMailboxes(userID)
	if err != nil {
		return []string{"INBOX", "Drafts", "Sent", "Junk", "Trash"}
	}
	names := make([]string, len(mailboxes))
	for i, mb := range mailboxes {
		names[i] = mb.Name
	}
	return names
}

func ruleToInfo(rule *bongov1.FilterRule) templates.RuleInfo {
	info := templates.RuleInfo{
		ID:             rule.Id,
		Name:           rule.Name,
		Priority:       int(rule.Priority),
		Enabled:        rule.Enabled,
		StopProcessing: rule.StopProcessing,
	}

	for _, cond := range rule.Conditions {
		info.Conditions = append(info.Conditions, templates.ConditionInfo{
			Field:    cond.Field,
			Operator: cond.Operator,
			Value:    cond.Value,
		})
	}

	if rule.Action != nil {
		info.Action = templates.ActionInfo{
			Type:   rule.Action.Type,
			Target: rule.Action.Target,
			Tags:   rule.Action.Tags,
		}
	}

	// Build summary strings.
	var condParts []string
	for _, c := range info.Conditions {
		condParts = append(condParts, fmt.Sprintf("%s %s '%s'", c.Field, c.Operator, c.Value))
	}
	info.ConditionSummary = strings.Join(condParts, " AND ")

	if info.Action.Type == "tag" {
		info.ActionSummary = fmt.Sprintf("tag: %s", strings.Join(info.Action.Tags, ", "))
	} else if info.Action.Type != "" {
		info.ActionSummary = fmt.Sprintf("%s: %s", info.Action.Type, info.Action.Target)
	}

	return info
}

func extractBodyText(raw []byte) string {
	env, err := enmime.ReadEnvelope(strings.NewReader(string(raw)))
	if err != nil {
		return ""
	}
	return env.Text
}
