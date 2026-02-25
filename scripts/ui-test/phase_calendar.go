package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
)

func phase5Calendar(c *checker, page *rod.Page) {
	fmt.Println("\n--- Phase 5: Calendar ---")

	// --- Month view ---
	if err := nav(page, "/calendar"); err != nil {
		c.check("Calendar", "Navigate to calendar", err)
		return
	}

	c.check("Calendar", "Month grid renders",
		boolErr(hasAny(page, ".calendar-grid", ".cal-grid")))

	nHeaders := countEls(page, ".calendar-header")
	c.check("Calendar", fmt.Sprintf("Day headers render (%d)", nHeaders), boolErr(nHeaders == 7))

	nCells := countEls(page, ".day-cell")
	c.check("Calendar", fmt.Sprintf("Day cells render (%d)", nCells), boolErr(nCells > 0))

	c.check("Calendar", "Mini calendar sidebar",
		boolErr(has(page, ".mini-calendar")))

	// Navigate to next month.
	if has(page, "a[href*='/calendar/']") {
		now := time.Now()
		nextMonth := now.AddDate(0, 1, 0)
		nextURL := fmt.Sprintf("/calendar/%d/%d", nextMonth.Year(), int(nextMonth.Month()))
		if err := nav(page, nextURL); err == nil {
			c.check("Calendar", "Next month loads",
				boolErr(hasAny(page, ".calendar-grid", ".cal-grid")))
		}
	}

	// Navigate to previous month.
	now := time.Now()
	prevMonth := now.AddDate(0, -1, 0)
	prevURL := fmt.Sprintf("/calendar/%d/%d", prevMonth.Year(), int(prevMonth.Month()))
	if err := nav(page, prevURL); err == nil {
		c.check("Calendar", "Previous month loads",
			boolErr(hasAny(page, ".calendar-grid", ".cal-grid")))
	}

	// --- Week view ---
	_, week := now.ISOWeek()
	weekURL := fmt.Sprintf("/calendar/week/%d/%d", now.Year(), week)
	if err := nav(page, weekURL); err != nil {
		c.check("Calendar", "Navigate to week view", err)
		return
	}

	c.check("Calendar", "Week view grid renders",
		boolErr(has(page, ".week-grid")))
	c.check("Calendar", "Week view has time gutter",
		boolErr(has(page, ".time-gutter")))
	c.check("Calendar", "View switcher on week view",
		boolErr(has(page, ".view-switcher")))

	// --- Day view ---
	if has(page, "a[href*='/calendar/day']") {
		if err := click(page, "a[href*='/calendar/day']"); err == nil {
			waitStable(page)
			c.check("Calendar", "Day view grid renders",
				boolErr(has(page, ".day-grid")))
		}
	} else {
		dayURL := fmt.Sprintf("/calendar/day/%s", now.Format("2006-01-02"))
		if err := nav(page, dayURL); err == nil {
			c.check("Calendar", "Day view grid renders",
				boolErr(has(page, ".day-grid")))
		}
	}

	// --- View switcher back to month ---
	monthSel := fmt.Sprintf("a[href='/calendar/%d/%d']", now.Year(), int(now.Month()))
	if has(page, monthSel) {
		_ = click(page, monthSel)
		waitStable(page)
		c.check("Calendar", "Return to month view via switcher",
			boolErr(hasAny(page, ".calendar-grid", ".cal-grid")))
	}

	// --- Create event ---
	if err := nav(page, "/calendar/event/new"); err == nil {
		c.check("Calendar", "New event form loads",
			boolErr(strings.Contains(currentURL(page), "/event/new") || has(page, "form")))

		// Check form fields.
		c.check("Calendar", "Event title field exists",
			boolErr(hasAny(page, "input[name='title']", "input[name='summary']")))
		c.check("Calendar", "Event start date field exists",
			boolErr(hasAny(page, "input[name='start_date']", "input[name='start']", "input[type='date']")))
	}

	// --- Search ---
	if err := nav(page, "/calendar"); err == nil {
		c.check("Calendar", "Calendar search/filter exists",
			boolErr(hasAny(page, "input[name='q']", ".search-box", "input[type='search']")))
	}
}
