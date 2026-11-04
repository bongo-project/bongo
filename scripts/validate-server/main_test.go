package main

import (
	"testing"
	"time"
)

func TestSeedTasksFor(t *testing.T) {
	today := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	tasks := seedTasksFor(today)
	if len(tasks) < 5 {
		t.Fatalf("got %d tasks, want at least 5", len(tasks))
	}

	day := today.Format("2006-01-02")
	var overdue, dueToday, later, undated, complete int
	prios := map[int]bool{}
	summaries := map[string]bool{}
	for _, task := range tasks {
		if task.summary == "" || summaries[task.summary] {
			t.Errorf("summary %q empty or duplicated", task.summary)
		}
		summaries[task.summary] = true
		if task.priority != 0 {
			prios[task.priority] = true
		}
		if task.complete {
			complete++
		}
		switch {
		case task.dueDate == "":
			undated++
			if task.dueTime != "" {
				t.Errorf("%q has a time but no date", task.summary)
			}
		case task.dueDate < day:
			if !task.complete {
				overdue++
			}
		case task.dueDate == day:
			dueToday++
			if task.dueTime != "" {
				t.Errorf("%q due today must be date only", task.summary)
			}
		default:
			later++
		}
	}
	for name, n := range map[string]int{"overdue": overdue, "today": dueToday, "later": later, "undated": undated} {
		if n != 1 {
			t.Errorf("%s tasks = %d, want 1", name, n)
		}
	}
	if complete < 1 {
		t.Error("no completed task")
	}
	if len(prios) < 2 {
		t.Errorf("want at least two distinct priorities, got %v", prios)
	}
}

func TestTaskForm(t *testing.T) {
	tests := []struct {
		name     string
		task     seedTask
		wantDate string
		wantTime string
		hasDate  bool
		hasTime  bool
	}{
		{"undated", seedTask{summary: "A", priority: 3}, "", "", false, false},
		{"date only", seedTask{summary: "B", dueDate: "2026-11-04"}, "2026-11-04", "", true, false},
		{"timed", seedTask{summary: "C", dueDate: "2026-11-09", dueTime: "14:00"}, "2026-11-09", "14:00", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := taskForm(tt.task)
			if f.Get("summary") != tt.task.summary {
				t.Errorf("summary = %q", f.Get("summary"))
			}
			if _, ok := f["due_date"]; ok != tt.hasDate || f.Get("due_date") != tt.wantDate {
				t.Errorf("due_date = %v", f["due_date"])
			}
			if _, ok := f["due_time"]; ok != tt.hasTime || f.Get("due_time") != tt.wantTime {
				t.Errorf("due_time = %v", f["due_time"])
			}
		})
	}
	if got := taskForm(seedTask{summary: "P", priority: 5}).Get("priority"); got != "5" {
		t.Errorf("priority = %q", got)
	}
}

func TestTaskGUIDBySummary(t *testing.T) {
	page := `<a href="/tasks/aaaa1111" class="agenda-title">First</a>
<a href="/tasks/bbbb2222" class="agenda-title x">Tom &amp; Jerry&#39;s</a>
<a href="/tasks/cccc3333" class="agenda-title">Third</a>`
	tests := []struct {
		name, summary, want string
		ok                  bool
	}{
		{"plain", "Third", "cccc3333", true},
		{"first", "First", "aaaa1111", true},
		{"escaped", "Tom & Jerry's", "bbbb2222", true},
		{"missing", "Nope", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := taskGUIDBySummary(page, tt.summary)
			if got != tt.want || ok != tt.ok {
				t.Errorf("got %q,%v want %q,%v", got, ok, tt.want, tt.ok)
			}
		})
	}
}
