package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// vacationDateLayout is the value format of an <input type="date">.
const vacationDateLayout = "2006-01-02"

// VacationHandler serves the vacation reply settings page.
type VacationHandler struct {
	prefs   *store.PreferenceStore
	pebble  *store.PebbleStore
	applier store.RaftApplier
	logger  *slog.Logger
}

// NewVacationHandler creates a VacationHandler. pebble and applier are used to
// delete the reply tracking records when vacation is switched off.
func NewVacationHandler(prefs *store.PreferenceStore, pebble *store.PebbleStore, applier store.RaftApplier, logger *slog.Logger) *VacationHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &VacationHandler{prefs: prefs, pebble: pebble, applier: applier, logger: logger}
}

// Show renders the vacation form.
func (h *VacationHandler) Show(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	prefs := h.prefs.Get(userID)
	loc := store.ResolveZone(prefs.GetTimezone())
	view := templates.VacationView{
		Enabled:  prefs.GetVacationEnabled(),
		Subject:  prefs.GetVacationSubject(),
		Body:     prefs.GetVacationBody(),
		Start:    vacationDate(prefs.GetVacationStart(), loc),
		End:      vacationDate(prefs.GetVacationEnd(), loc),
		Interval: store.VacationIntervalDays(prefs),
		Flash:    r.URL.Query().Get("flash"),
	}
	templ.Handler(templates.VacationPage(view, email)).ServeHTTP(w, r)
}

// Save validates the form completely, then changes only the vacation fields
// of the stored preferences (read, modify, write) and redirects back.
//
// A concurrent save of /settings can lose one of the two updates, because both
// write the whole preferences record; that already holds for every preference
// and is not widened here.
func (h *VacationHandler) Save(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		fwdError(w, r, "settings.vacationErrForm", http.StatusBadRequest)
		return
	}

	prefs := h.prefs.Get(userID)
	loc := store.ResolveZone(prefs.GetTimezone())

	enabled := r.FormValue("vacation_enabled") == "1"
	subject := strings.TrimSpace(r.FormValue("vacation_subject"))
	body := r.FormValue("vacation_body")

	if err := store.ValidateVacationSubject(subject); err != nil {
		fwdError(w, r, "settings.vacationErrSubject", http.StatusBadRequest)
		return
	}
	if err := store.ValidateVacationBody(body); err != nil {
		fwdError(w, r, "settings.vacationErrBody", http.StatusBadRequest)
		return
	}
	if enabled && strings.TrimSpace(body) == "" {
		fwdError(w, r, "settings.vacationErrBodyRequired", http.StatusBadRequest)
		return
	}

	interval := store.VacationDefaultIntervalDays
	if v := strings.TrimSpace(r.FormValue("vacation_interval_days")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || store.ValidateVacationInterval(n) != nil {
			fwdError(w, r, "settings.vacationErrInterval", http.StatusBadRequest)
			return
		}
		interval = n
	}

	start, err := parseVacationDay(r.FormValue("vacation_start"), loc, false)
	if err != nil {
		fwdError(w, r, "settings.vacationErrDate", http.StatusBadRequest)
		return
	}
	end, err := parseVacationDay(r.FormValue("vacation_end"), loc, true)
	if err != nil {
		fwdError(w, r, "settings.vacationErrDate", http.StatusBadRequest)
		return
	}
	if err := store.ValidateVacationWindow(start, end); err != nil {
		fwdError(w, r, "settings.vacationErrWindow", http.StatusBadRequest)
		return
	}

	prefs.VacationEnabled = enabled
	prefs.VacationSubject = subject
	prefs.VacationBody = body
	prefs.VacationStart = start
	prefs.VacationEnd = end
	prefs.VacationIntervalDays = int32(interval) // #nosec G115 -- validated to 1..30 above

	if err := h.prefs.Set(userID, prefs); err != nil {
		h.logger.Error("vacation: saving preferences failed", "user", userID, "error", err)
		fwdError(w, r, "settings.vacationErrSave", http.StatusInternalServerError)
		return
	}

	if !enabled {
		h.clearTracking(userID)
	}

	http.Redirect(w, r, "/settings/vacation?flash="+url.QueryEscape(fwdText(r, "settings.vacationSaved")), http.StatusSeeOther)
}

// clearTracking deletes the user's reply tracking records after vacation is
// switched off, so a later vacation answers every sender afresh.
//
// This is best-effort and runs after the preferences were saved: it is one
// Raft DELETE per record, bounded by the 100 replies per hour cap. A failure is
// logged with context and the request still succeeds, because the setting did
// change. The leader's 30 day sweep reclaims anything left; the worst case is
// that a sender answered shortly before is not answered again at the next
// vacation until its interval has passed.
func (h *VacationHandler) clearTracking(userID string) {
	if h.pebble == nil || h.applier == nil {
		return
	}
	iter, err := h.pebble.PrefixScan(store.VacationSentPrefix(userID))
	if err != nil {
		h.logger.Warn("vacation: listing tracking records to delete failed", "user", userID, "error", err)
		return
	}
	var keys [][]byte
	for iter.Next() {
		keys = append(keys, append([]byte(nil), iter.Key()...))
	}
	if err := iter.Close(); err != nil {
		h.logger.Warn("vacation: closing tracking iterator failed", "user", userID, "error", err)
	}
	for _, k := range keys {
		cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_DELETE, Key: k}
		if err := h.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
			h.logger.Warn("vacation: deleting tracking record failed", "user", userID, "error", err)
			return
		}
	}
}

// vacationDate formats unix seconds as a date input value in loc; 0 is "".
func vacationDate(secs int64, loc *time.Location) string {
	if secs == 0 {
		return ""
	}
	return time.Unix(secs, 0).In(loc).Format(vacationDateLayout)
}

// parseVacationDay reads a date input in the user's zone. The start of the
// window is 00:00 of the day and the end is 23:59:59 of it, so a window "from
// the 1st to the 5th" covers both whole days. An empty value is 0 (open).
func parseVacationDay(v string, loc *time.Location, endOfDay bool) (int64, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseInLocation(vacationDateLayout, v, loc)
	if err != nil {
		return 0, errors.New("invalid date")
	}
	if endOfDay {
		d = time.Date(d.Year(), d.Month(), d.Day(), 23, 59, 59, 0, loc)
	}
	return d.Unix(), nil
}
