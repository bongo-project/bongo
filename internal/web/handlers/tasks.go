package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	taskStatusCompleted   = "COMPLETED"
	taskStatusCancelled   = "CANCELLED"
	taskStatusNeedsAction = "NEEDS-ACTION"

	// maxTaskSummary and maxTaskDescription cap the text of a task, in
	// characters and bytes. Both are checked at the cap and one past it.
	maxTaskSummary     = 255
	maxTaskDescription = 16 << 10

	defaultTaskColour = "#4A90D9"
)

// TasksHandler serves the task list and the task mutations. Every handler
// reads the user from the session and touches that user's own tasks only: no
// route names an owner.
//
// Limitation: tasks in a VTODO calendar shared by another user are reachable
// over CalDAV but not here; the list and the agenda show the actor's own
// tasks.
//
// Limitation: completing a recurring task completes the whole series. DUE is
// not advanced to the next occurrence the way CalDAV clients do.
type TasksHandler struct {
	tasks     *store.TaskStore
	calendars *store.CalendarStore
	quota     QuotaChecker // nil = unlimited
	zones     zoneSource   // nil = process zone
	logger    *slog.Logger
	// now is the clock; nil means time.Now.
	now func() time.Time
}

// NewTasksHandler creates a TasksHandler.
func NewTasksHandler(tasks *store.TaskStore, calendars *store.CalendarStore) *TasksHandler {
	return &TasksHandler{tasks: tasks, calendars: calendars, logger: slog.Default()}
}

// SetQuotaChecker enables the domain storage-quota check on task writes.
func (h *TasksHandler) SetQuotaChecker(qc QuotaChecker) { h.quota = qc }

// SetZoneSource wires the user time-zone preference.
func (h *TasksHandler) SetZoneSource(z zoneSource) { h.zones = z }

func (h *TasksHandler) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

func (h *TasksHandler) location(userID string) *time.Location {
	if h.zones != nil {
		return h.zones.Location(userID)
	}
	return time.Local
}

// fail answers with a translated message and never echoes request input.
func fail(w http.ResponseWriter, r *http.Request, status int, key string) {
	http.Error(w, i18n.T(middleware.LocaleFromContext(r.Context()), key), status)
}

func isOpenTask(t *bongov1.Task) bool {
	return t.Status != taskStatusCompleted && t.Status != taskStatusCancelled
}

// taskDueDay returns the calendar date (yyyy-mm-dd) a task is due. A DATE
// value keeps its UTC date, as an all-day event does; a DATE-TIME is read in
// loc.
func taskDueDay(due int64, dateOnly bool, loc *time.Location) string {
	if dateOnly {
		return time.Unix(due, 0).UTC().Format("2006-01-02")
	}
	return time.Unix(due, 0).In(loc).Format("2006-01-02")
}

// taskOverdue reports whether an open task with a due date is past it at now.
// A date-only task is overdue from the day after its date, in the user's zone.
func taskOverdue(t *bongov1.Task, dateOnly bool, now time.Time) bool {
	if t.Due == 0 || !isOpenTask(t) {
		return false
	}
	if dateOnly {
		return taskDueDay(t.Due, true, now.Location()) < now.Format("2006-01-02")
	}
	return t.Due < now.Unix()
}

// taskDueInstant is the instant a task is ordered by: a date-only DUE is the
// start of its date in loc.
func taskDueInstant(t *bongov1.Task, dateOnly bool, loc *time.Location) int64 {
	if !dateOnly {
		return t.Due
	}
	d := time.Unix(t.Due, 0).UTC()
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc).Unix()
}

// priorityRank orders priorities: 1 is the highest and 0 (undefined) comes
// after 9.
func priorityRank(p int32) int32 {
	if p == 0 {
		return 10
	}
	return p
}

type sortableTask struct {
	task     *bongov1.Task
	dateOnly bool
	recurs   bool
}

func readTasks(tasks []*bongov1.Task) []sortableTask {
	out := make([]sortableTask, len(tasks))
	for i, t := range tasks {
		d, r := icalevent.TodoTraits(t.IcalRaw)
		out[i] = sortableTask{task: t, dateOnly: d, recurs: r}
	}
	return out
}

// sortOpenTasks orders open tasks: overdue first, then upcoming by due date,
// then undated. Ties go by priority (1 first, undefined last), summary and
// guid, so the order is deterministic.
func sortOpenTasks(ts []sortableTask, now time.Time) {
	loc := now.Location()
	group := func(s sortableTask) int {
		switch {
		case s.task.Due == 0:
			return 2
		case taskOverdue(s.task, s.dateOnly, now):
			return 0
		}
		return 1
	}
	sort.SliceStable(ts, func(a, b int) bool {
		x, y := ts[a], ts[b]
		if gx, gy := group(x), group(y); gx != gy {
			return gx < gy
		}
		if x.task.Due != 0 {
			if ix, iy := taskDueInstant(x.task, x.dateOnly, loc), taskDueInstant(y.task, y.dateOnly, loc); ix != iy {
				return ix < iy
			}
		}
		if px, py := priorityRank(x.task.Priority), priorityRank(y.task.Priority); px != py {
			return px < py
		}
		if sx, sy := strings.ToLower(x.task.Summary), strings.ToLower(y.task.Summary); sx != sy {
			return sx < sy
		}
		return x.task.Guid < y.task.Guid
	})
}

// sortDoneTasks orders completed and cancelled tasks by completion time, latest
// first, then summary and guid.
func sortDoneTasks(ts []sortableTask) {
	sort.SliceStable(ts, func(a, b int) bool {
		x, y := ts[a].task, ts[b].task
		if x.Completed != y.Completed {
			return x.Completed > y.Completed
		}
		if sx, sy := strings.ToLower(x.Summary), strings.ToLower(y.Summary); sx != sy {
			return sx < sy
		}
		return x.Guid < y.Guid
	})
}

// vtodoCalendars returns the user's calendars a task can be filed into, and
// the IDs of the subscribed (read-only) calendars, which hold tasks the user
// cannot change.
func (h *TasksHandler) vtodoCalendars(userID string) (writable []*bongov1.Calendar, subscribed map[string]bool, err error) {
	all, err := h.calendars.List(userID)
	if err != nil {
		return nil, nil, fmt.Errorf("listing calendars: %w", err)
	}
	subscribed = map[string]bool{}
	for _, c := range all {
		switch {
		case store.IsSubscribed(c):
			subscribed[c.Guid] = true
		case store.CalendarAllows(c, store.ComponentTodo):
			writable = append(writable, c)
		}
	}
	return writable, subscribed, nil
}

// List renders /tasks: the open tasks, or with ?show=completed the completed
// and cancelled ones. Cancelled tasks sit under Completed so they stay
// reachable, and can be reopened or deleted.
func (h *TasksHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	loc := h.location(userID)
	now := h.clock().In(loc)
	r = clockRequest(r, now)
	showCompleted := r.URL.Query().Get("show") == "completed"

	all, err := h.tasks.ListAll(userID)
	if err != nil {
		h.logger.Error("tasks: listing failed", "user", userID, "error", err)
		fail(w, r, http.StatusInternalServerError, "tasks.errLoadFailed")
		return
	}
	calendars, subscribed, err := h.vtodoCalendars(userID)
	if err != nil {
		h.logger.Error("tasks: listing calendars failed", "user", userID, "error", err)
		fail(w, r, http.StatusInternalServerError, "tasks.errLoadFailed")
		return
	}
	colours := buildCalColorMap(calendars)

	var picked []sortableTask
	for _, s := range readTasks(all) {
		if isOpenTask(s.task) != showCompleted {
			picked = append(picked, s)
		}
	}
	if showCompleted {
		sortDoneTasks(picked)
	} else {
		sortOpenTasks(picked, now)
	}

	rows := make([]templates.TaskRow, 0, len(picked))
	for _, s := range picked {
		colour := defaultTaskColour
		if c := colours[s.task.CalendarId]; c != "" {
			colour = c
		}
		row := templates.TaskRow{
			Task:        s.task,
			Overdue:     taskOverdue(s.task, s.dateOnly, now),
			Recurring:   s.recurs,
			ColourClass: templates.CalendarColourClass(colour),
			// The rule writable applies to the mutations.
			ReadOnly: subscribed[s.task.CalendarId],
		}
		switch {
		case s.task.Due == 0:
		case s.dateOnly:
			row.DueLabel = time.Unix(s.task.Due, 0).UTC().Format("2006-01-02")
		default:
			row.DueLabel = time.Unix(s.task.Due, 0).In(loc).Format("2006-01-02 15:04")
		}
		rows = append(rows, row)
	}

	templ.Handler(templates.TasksPage(templates.TasksData{
		Rows:      rows,
		Completed: showCompleted,
		Calendars: calendars,
		Email:     email,
	})).ServeHTTP(w, r)
}

// loadTask reads the user's own task named by the URL. It answers 404 for a
// guid the user does not hold, which is the same for another user's task.
func (h *TasksHandler) loadTask(w http.ResponseWriter, r *http.Request) (userID string, task *bongov1.Task, ok bool) {
	userID, _ = middleware.UserFromContext(r.Context())
	guid := chi.URLParam(r, "guid")
	if guid == "" || len(guid) > 255 {
		fail(w, r, http.StatusNotFound, "tasks.errNotFound")
		return "", nil, false
	}
	task, err := h.tasks.Get(userID, guid)
	if err != nil {
		if errors.Is(err, store.ErrTaskNotFound) {
			fail(w, r, http.StatusNotFound, "tasks.errNotFound")
			return "", nil, false
		}
		h.logger.Error("tasks: reading task failed", "user", userID, "error", err)
		fail(w, r, http.StatusInternalServerError, "tasks.errLoadFailed")
		return "", nil, false
	}
	return userID, task, true
}

// writable refuses a task kept in a read-only subscription (403) and reports
// true when the write may go ahead.
func (h *TasksHandler) writable(w http.ResponseWriter, r *http.Request, userID string, task *bongov1.Task) bool {
	subscribed, err := h.calendars.CalendarIsSubscribed(userID, task.CalendarId)
	if err != nil {
		h.logger.Error("tasks: reading calendar failed", "user", userID, "error", err)
		fail(w, r, http.StatusInternalServerError, "tasks.errSaveFailed")
		return false
	}
	if subscribed {
		fail(w, r, http.StatusForbidden, "tasks.errReadOnly")
		return false
	}
	return true
}

// Edit renders the edit form of one task.
func (h *TasksHandler) Edit(w http.ResponseWriter, r *http.Request) {
	userID, task, ok := h.loadTask(w, r)
	if !ok {
		return
	}
	_, email := middleware.UserFromContext(r.Context())
	loc := h.location(userID)
	dateOnly, recurs := icalevent.TodoTraits(task.IcalRaw)
	date, clock := dueFormValues(task.Due, dateOnly, loc)
	templ.Handler(templates.TaskEditPage(templates.TaskFormData{
		Task: task, DueDate: date, DueTime: clock, Recurring: recurs, Email: email,
	})).ServeHTTP(w, r)
}

// dueFormValues renders a due instant as the date and time input values.
func dueFormValues(due int64, dateOnly bool, loc *time.Location) (date, clock string) {
	switch {
	case due == 0:
		return "", ""
	case dateOnly:
		return time.Unix(due, 0).UTC().Format("2006-01-02"), ""
	}
	t := time.Unix(due, 0).In(loc)
	return t.Format("2006-01-02"), t.Format("15:04")
}

// taskFormError is a refused form; its text is the translation key.
type taskFormError string

func (e taskFormError) Error() string { return string(e) }

// cleanTaskText removes control characters other than newline and tab.
func cleanTaskText(s string) string {
	s = normaliseNewlines(s)
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

type taskForm struct {
	summary, description string
	priority             int32
	due                  int64
	dueDate              bool
	dateStr, timeStr     string
}

// parseTaskForm reads and validates the task form. A date with no time is a
// DATE (stored at UTC midnight); a date with a time is read in loc. current is
// the stored task being edited, nil for a new one: the summary length cap
// applies only to a summary that differs from it, so a task a CalDAV client
// stored with a longer summary can still have its due date changed.
func parseTaskForm(r *http.Request, loc *time.Location, current *bongov1.Task) (taskForm, error) {
	var f taskForm
	f.summary = strings.TrimSpace(cleanTaskText(r.FormValue("summary")))
	f.description = cleanTaskText(r.FormValue("description"))
	summaryUnchanged := current != nil && f.summary == strings.TrimSpace(cleanTaskText(current.Summary))
	switch {
	case f.summary == "":
		return f, taskFormError("tasks.errSummaryRequired")
	case !summaryUnchanged && utf8.RuneCountInString(f.summary) > maxTaskSummary, len(f.description) > maxTaskDescription:
		return f, taskFormError("tasks.errTooLong")
	}
	if v := strings.TrimSpace(r.FormValue("priority")); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 0 || n > 9 {
			return f, taskFormError("tasks.errInvalid")
		}
		f.priority = int32(n)
	}
	f.dateStr = strings.TrimSpace(r.FormValue("due_date"))
	f.timeStr = strings.TrimSpace(r.FormValue("due_time"))
	switch {
	case f.dateStr == "" && f.timeStr == "":
	case f.dateStr == "":
		return f, taskFormError("tasks.errInvalid")
	case f.timeStr == "":
		d, err := time.ParseInLocation("2006-01-02", f.dateStr, time.UTC)
		if err != nil {
			return f, taskFormError("tasks.errInvalid")
		}
		f.due, f.dueDate = d.Unix(), true
	default:
		d, err := time.ParseInLocation("2006-01-02 15:04", f.dateStr+" "+f.timeStr, loc)
		if err != nil {
			return f, taskFormError("tasks.errInvalid")
		}
		f.due = d.Unix()
	}
	return f, nil
}

func (h *TasksHandler) refuseForm(w http.ResponseWriter, r *http.Request, err error) {
	var fe taskFormError
	if errors.As(err, &fe) {
		fail(w, r, http.StatusBadRequest, string(fe))
		return
	}
	fail(w, r, http.StatusBadRequest, "tasks.errInvalid")
}

// rewriteRaw returns the ical_raw for updated, keeping every property the web
// does not model. A stored raw that cannot be parsed is dropped (nil), so
// CalDAV synthesises the task from the structured fields.
func (h *TasksHandler) rewriteRaw(existing, updated *bongov1.Task, dueDate bool, now time.Time) ([]byte, error) {
	if len(existing.IcalRaw) == 0 {
		return icalevent.BuildTodo(updated, dueDate, now)
	}
	raw, err := icalevent.ApplyTodoEdit(existing.IcalRaw, existing, updated, dueDate, now)
	if errors.Is(err, icalevent.ErrTodoDecode) {
		h.logger.Warn("tasks: stored ical unreadable, dropping it", "user_task", existing.Guid)
		return nil, nil
	}
	return raw, err
}

// saveChange rewrites ical_raw for updated and stores it. Quota refuses only
// growth. It answers the request itself on failure and reports true on success.
func (h *TasksHandler) saveChange(w http.ResponseWriter, r *http.Request, userID string, existing, updated *bongov1.Task, dueDate bool) bool {
	raw, err := h.rewriteRaw(existing, updated, dueDate, h.clock())
	if err != nil {
		h.logger.Error("tasks: encoding ical failed", "user", userID, "error", err)
		fail(w, r, http.StatusInternalServerError, "tasks.errSaveFailed")
		return false
	}
	updated.IcalRaw = raw
	if grow := store.TaskRecordSize(updated) - store.TaskRecordSize(existing); grow > 0 && quotaExceeded(h.quota, h.logger, userID, grow) {
		refuseQuota(w, r)
		return false
	}
	if err := h.tasks.Update(userID, existing.Guid, updated); err != nil {
		h.logger.Error("tasks: update failed", "user", userID, "error", err)
		fail(w, r, http.StatusInternalServerError, "tasks.errSaveFailed")
		return false
	}
	return true
}

func redirectTasks(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/tasks", http.StatusSeeOther)
}

// Update handles POST /tasks/{guid}: edit summary, description, priority and
// due date.
func (h *TasksHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID, existing, ok := h.loadTask(w, r)
	if !ok {
		return
	}
	if !h.writable(w, r, userID, existing) {
		return
	}
	loc := h.location(userID)
	f, err := parseTaskForm(r, loc, existing)
	if err != nil {
		h.refuseForm(w, r, err)
		return
	}
	updated := proto.Clone(existing).(*bongov1.Task)
	updated.Summary, updated.Description, updated.Priority = f.summary, f.description, f.priority

	oldDateOnly, _ := icalevent.TodoTraits(existing.IcalRaw)
	// A due date the form shows unchanged is left exactly as stored, so the
	// seconds and TZID the form cannot show survive an edit.
	curDate, curTime := dueFormValues(existing.Due, oldDateOnly, loc)
	dueDate := f.dueDate
	if f.dateStr == curDate && f.timeStr == curTime {
		updated.Due, dueDate = existing.Due, oldDateOnly
	} else {
		updated.Due = f.due
	}
	if !h.saveChange(w, r, userID, existing, updated, dueDate) {
		return
	}
	redirectTasks(w, r)
}

// Complete handles POST /tasks/{guid}/complete. It records COMPLETED and
// STATUS:COMPLETED, and writes nothing when the task is already completed.
func (h *TasksHandler) Complete(w http.ResponseWriter, r *http.Request) {
	userID, existing, ok := h.loadTask(w, r)
	if !ok {
		return
	}
	if !h.writable(w, r, userID, existing) {
		return
	}
	if existing.Status == taskStatusCompleted {
		redirectTasks(w, r)
		return
	}
	updated := proto.Clone(existing).(*bongov1.Task)
	updated.Status = taskStatusCompleted
	updated.Completed = h.clock().Unix()
	updated.Percent = 100
	dateOnly, _ := icalevent.TodoTraits(existing.IcalRaw)
	if !h.saveChange(w, r, userID, existing, updated, dateOnly) {
		return
	}
	redirectTasks(w, r)
}

// Reopen handles POST /tasks/{guid}/reopen: COMPLETED is cleared and the
// status is NEEDS-ACTION. It writes nothing for a task that is already open.
func (h *TasksHandler) Reopen(w http.ResponseWriter, r *http.Request) {
	userID, existing, ok := h.loadTask(w, r)
	if !ok {
		return
	}
	if !h.writable(w, r, userID, existing) {
		return
	}
	if isOpenTask(existing) && existing.Completed == 0 {
		redirectTasks(w, r)
		return
	}
	updated := proto.Clone(existing).(*bongov1.Task)
	updated.Status = taskStatusNeedsAction
	updated.Completed = 0
	if updated.Percent == 100 {
		updated.Percent = 0
	}
	dateOnly, _ := icalevent.TodoTraits(existing.IcalRaw)
	if !h.saveChange(w, r, userID, existing, updated, dateOnly) {
		return
	}
	redirectTasks(w, r)
}

// Delete handles POST /tasks/{guid}/delete.
func (h *TasksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, existing, ok := h.loadTask(w, r)
	if !ok {
		return
	}
	if !h.writable(w, r, userID, existing) {
		return
	}
	if err := h.tasks.Delete(userID, existing.Guid); err != nil {
		h.logger.Error("tasks: delete failed", "user", userID, "error", err)
		fail(w, r, http.StatusInternalServerError, "tasks.errSaveFailed")
		return
	}
	redirectTasks(w, r)
}

// targetCalendar picks the calendar a new task is filed into. A submitted ID
// must be one of the user's own writable VTODO calendars. Without one the
// Tasks calendar is used, or the first VTODO calendar when Tasks was deleted,
// and the Tasks calendar is created when the user has none. The fixed GUID
// makes that creation idempotent.
func (h *TasksHandler) targetCalendar(userID, submitted string) (id string, status int, key string) {
	calendars, _, err := h.vtodoCalendars(userID)
	if err != nil {
		h.logger.Error("tasks: listing calendars failed", "user", userID, "error", err)
		return "", http.StatusInternalServerError, "tasks.errSaveFailed"
	}
	if submitted != "" {
		for _, c := range calendars {
			if c.Guid == submitted {
				return c.Guid, 0, ""
			}
		}
		return "", http.StatusBadRequest, "tasks.errNoCalendar"
	}
	for _, c := range calendars {
		if c.Guid == store.TasksCalendarGUID {
			return c.Guid, 0, ""
		}
	}
	if len(calendars) > 0 {
		return calendars[0].Guid, 0, ""
	}
	if _, err := h.calendars.Create(userID, store.NewTasksCalendar()); err != nil {
		h.logger.Error("tasks: creating tasks calendar failed", "user", userID, "error", err)
		return "", http.StatusInternalServerError, "tasks.errSaveFailed"
	}
	return store.TasksCalendarGUID, 0, ""
}

// Create handles POST /tasks.
func (h *TasksHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	f, err := parseTaskForm(r, h.location(userID), nil)
	if err != nil {
		h.refuseForm(w, r, err)
		return
	}
	task := &bongov1.Task{
		Guid:        store.NewTaskGUID(),
		Summary:     f.summary,
		Description: f.description,
		Priority:    f.priority,
		Due:         f.due,
		Status:      taskStatusNeedsAction,
	}
	raw, err := icalevent.BuildTodo(task, f.dueDate, h.clock())
	if err != nil {
		h.logger.Error("tasks: encoding ical failed", "user", userID, "error", err)
		fail(w, r, http.StatusInternalServerError, "tasks.errSaveFailed")
		return
	}
	task.IcalRaw = raw
	if quotaExceeded(h.quota, h.logger, userID, store.TaskRecordSize(task)) {
		refuseQuota(w, r)
		return
	}
	calID, status, key := h.targetCalendar(userID, r.FormValue("calendar_id"))
	if status != 0 {
		fail(w, r, status, key)
		return
	}
	task.CalendarId = calID
	if _, err := h.tasks.Create(userID, task); err != nil {
		h.logger.Error("tasks: create failed", "user", userID, "error", err)
		fail(w, r, http.StatusInternalServerError, "tasks.errSaveFailed")
		return
	}
	redirectTasks(w, r)
}
