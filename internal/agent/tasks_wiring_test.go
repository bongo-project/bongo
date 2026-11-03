package agent

import (
	"os"
	"strings"
	"testing"
)

// The task routes, their storage gate and the agenda's task store are wired in
// agent.go. Deleting any of these lines turns this test red.
func TestTaskRoutesWired(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, want := range []string{
		`r.Get("/tasks", tasksH.List)`,
		`r.With(refuseLowStorage).Post("/tasks", tasksH.Create)`,
		`r.Get("/tasks/{guid}", tasksH.Edit)`,
		`r.Post("/tasks/{guid}", tasksH.Update)`,
		`r.Post("/tasks/{guid}/complete", tasksH.Complete)`,
		`r.Post("/tasks/{guid}/reopen", tasksH.Reopen)`,
		`r.Post("/tasks/{guid}/delete", tasksH.Delete)`,
		`calendarH.SetTaskStore(a.Tasks)`,
		`tasksH.SetQuotaChecker(a.DomainQuota)`,
		`tasksH.SetZoneSource(a.Preferences)`,
		// Without the ACL store no shared task or calendar is shown at all.
		`tasksH.SetACL(a.ACL)`,
		`calendarH.SetACL(a.ACL)`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("agent.go does not contain %s", want)
		}
	}
	// The mutating routes must not be registered as DELETE or PUT.
	for _, bad := range []string{`Delete("/tasks`, `Put("/tasks`} {
		if strings.Contains(text, bad) {
			t.Errorf("agent.go registers %s", bad)
		}
	}
}
