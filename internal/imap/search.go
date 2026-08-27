package imap

import (
	"fmt"
	"strings"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/emersion/go-imap/v2"

	"github.com/bongo-project/bongo/internal/store"
)

// hasTextCriteria returns true if the search criteria contains any text-based
// criteria that should be handled by Bleve (header, body, text, date range).
func hasTextCriteria(criteria *imap.SearchCriteria) bool {
	if len(criteria.Header) > 0 || len(criteria.Body) > 0 || len(criteria.Text) > 0 {
		return true
	}
	if !criteria.Since.IsZero() || !criteria.Before.IsZero() {
		return true
	}
	for _, notCrit := range criteria.Not {
		if hasTextCriteria(&notCrit) {
			return true
		}
	}
	for _, orPair := range criteria.Or {
		if hasTextCriteria(&orPair[0]) || hasTextCriteria(&orPair[1]) {
			return true
		}
	}
	return false
}

// buildBleveQuery translates IMAP search criteria into a Bleve query.
// Flag-based criteria are NOT included; they must be applied as a post-filter.
func buildBleveQuery(criteria *imap.SearchCriteria) query.Query {
	var must []query.Query

	// Header field searches.
	for _, h := range criteria.Header {
		field := strings.ToLower(h.Key)
		value := h.Value

		switch field {
		case "subject":
			q := bleve.NewMatchQuery(value)
			q.SetField("subject")
			must = append(must, q)
		case "from":
			q := bleve.NewMatchQuery(value)
			q.SetField("from")
			must = append(must, q)
		case "to", "cc", "bcc":
			q := bleve.NewMatchQuery(value)
			q.SetField("to")
			must = append(must, q)
		default:
			// Generic header search: search across subject and from.
			q1 := bleve.NewMatchQuery(value)
			q1.SetField("subject")
			q2 := bleve.NewMatchQuery(value)
			q2.SetField("from")
			dis := bleve.NewDisjunctionQuery(q1, q2)
			must = append(must, dis)
		}
	}

	// Body text search.
	for _, body := range criteria.Body {
		q := bleve.NewMatchQuery(body)
		q.SetField("body")
		must = append(must, q)
	}

	// Full text search (all fields).
	for _, text := range criteria.Text {
		q := bleve.NewQueryStringQuery(text)
		must = append(must, q)
	}

	// Date range filters.
	if !criteria.Since.IsZero() || !criteria.Before.IsZero() {
		var minDate, maxDate *float64
		if !criteria.Since.IsZero() {
			v := float64(criteria.Since.Unix())
			minDate = &v
		}
		if !criteria.Before.IsZero() {
			v := float64(criteria.Before.Unix())
			maxDate = &v
		}
		inclusive := true
		q := query.NewNumericRangeInclusiveQuery(minDate, maxDate, &inclusive, &inclusive)
		q.SetField("date")
		must = append(must, q)
	}

	// NOT criteria: each sub-criteria becomes a must_not clause.
	var mustNot []query.Query
	for _, notCrit := range criteria.Not {
		sub := buildBleveQuery(&notCrit)
		if sub != nil {
			mustNot = append(mustNot, sub)
		}
	}

	// OR criteria: each pair becomes a disjunction.
	for _, orPair := range criteria.Or {
		left := buildBleveQuery(&orPair[0])
		right := buildBleveQuery(&orPair[1])
		if left != nil && right != nil {
			dis := bleve.NewDisjunctionQuery(left, right)
			must = append(must, dis)
		} else if left != nil {
			must = append(must, left)
		} else if right != nil {
			must = append(must, right)
		}
	}

	if len(must) == 0 && len(mustNot) == 0 {
		return nil
	}

	if len(must) == 0 && len(mustNot) > 0 {
		// Must not without must: match all, then exclude.
		boolQ := bleve.NewBooleanQuery()
		boolQ.AddMust(bleve.NewMatchAllQuery())
		for _, q := range mustNot {
			boolQ.AddMustNot(q)
		}
		return boolQ
	}

	if len(must) == 1 && len(mustNot) == 0 {
		return must[0]
	}

	boolQ := bleve.NewBooleanQuery()
	for _, q := range must {
		boolQ.AddMust(q)
	}
	for _, q := range mustNot {
		boolQ.AddMustNot(q)
	}
	return boolQ
}

// searchBleve performs a Bleve search and returns the set of matching mail GUIDs.
func searchBleve(search *store.SearchManager, userID string, criteria *imap.SearchCriteria) (map[string]struct{}, error) {
	q := buildBleveQuery(criteria)
	if q == nil {
		return nil, fmt.Errorf("no text criteria to search")
	}

	req := bleve.NewSearchRequestOptions(q, 10000, 0, false)
	result, err := search.SearchWithRequest(userID, req)
	if err != nil {
		return nil, fmt.Errorf("bleve search: %w", err)
	}

	guids := make(map[string]struct{}, len(result.Hits))
	for _, hit := range result.Hits {
		guids[hit.ID] = struct{}{}
	}
	return guids, nil
}

// matchesFlagCriteria checks flag and date criteria that are NOT handled by Bleve.
func matchesFlagCriteria(flags uint32, internalDate int64, criteria *imap.SearchCriteria) bool {
	for _, flag := range criteria.Flag {
		if !hasFlag(flags, flag) {
			return false
		}
	}
	for _, flag := range criteria.NotFlag {
		if hasFlag(flags, flag) {
			return false
		}
	}

	// Date filters (also check here for messages not in Bleve).
	msgDate := time.Unix(internalDate, 0)
	if !criteria.Since.IsZero() && msgDate.Before(criteria.Since) {
		return false
	}
	if !criteria.Before.IsZero() && !msgDate.Before(criteria.Before) {
		return false
	}

	// Recurse into NOT criteria for flag-only parts.
	for _, notCrit := range criteria.Not {
		if matchesFlagCriteria(flags, internalDate, &notCrit) {
			return false
		}
	}

	// OR criteria for flag-only parts.
	for _, orPair := range criteria.Or {
		if !matchesFlagCriteria(flags, internalDate, &orPair[0]) &&
			!matchesFlagCriteria(flags, internalDate, &orPair[1]) {
			return false
		}
	}

	return true
}
