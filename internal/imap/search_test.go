package imap

import (
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/emersion/go-imap/v2"
)

func TestHasTextCriteria(t *testing.T) {
	tests := []struct {
		name     string
		criteria *imap.SearchCriteria
		want     bool
	}{
		{
			name:     "empty criteria",
			criteria: &imap.SearchCriteria{},
			want:     false,
		},
		{
			name: "flags only",
			criteria: &imap.SearchCriteria{
				Flag: []imap.Flag{imap.FlagSeen},
			},
			want: false,
		},
		{
			name: "header criteria",
			criteria: &imap.SearchCriteria{
				Header: []imap.SearchCriteriaHeaderField{
					{Key: "Subject", Value: "test"},
				},
			},
			want: true,
		},
		{
			name: "body criteria",
			criteria: &imap.SearchCriteria{
				Body: []string{"hello"},
			},
			want: true,
		},
		{
			name: "text criteria",
			criteria: &imap.SearchCriteria{
				Text: []string{"world"},
			},
			want: true,
		},
		{
			name: "since date",
			criteria: &imap.SearchCriteria{
				Since: time.Now().Add(-24 * time.Hour),
			},
			want: true,
		},
		{
			name: "before date",
			criteria: &imap.SearchCriteria{
				Before: time.Now(),
			},
			want: true,
		},
		{
			name: "nested NOT with text",
			criteria: &imap.SearchCriteria{
				Not: []imap.SearchCriteria{
					{Body: []string{"spam"}},
				},
			},
			want: true,
		},
		{
			name: "nested OR with text",
			criteria: &imap.SearchCriteria{
				Or: [][2]imap.SearchCriteria{
					{
						{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: "a"}}},
						{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: "b"}}},
					},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasTextCriteria(tt.criteria)
			if got != tt.want {
				t.Errorf("hasTextCriteria() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildBleveQuery(t *testing.T) {
	tests := []struct {
		name     string
		criteria *imap.SearchCriteria
		wantNil  bool
	}{
		{
			name:     "empty criteria returns nil",
			criteria: &imap.SearchCriteria{},
			wantNil:  true,
		},
		{
			name: "subject search",
			criteria: &imap.SearchCriteria{
				Header: []imap.SearchCriteriaHeaderField{
					{Key: "Subject", Value: "meeting"},
				},
			},
			wantNil: false,
		},
		{
			name: "from search",
			criteria: &imap.SearchCriteria{
				Header: []imap.SearchCriteriaHeaderField{
					{Key: "From", Value: "alice@example.com"},
				},
			},
			wantNil: false,
		},
		{
			name: "body search",
			criteria: &imap.SearchCriteria{
				Body: []string{"important"},
			},
			wantNil: false,
		},
		{
			name: "text search",
			criteria: &imap.SearchCriteria{
				Text: []string{"project"},
			},
			wantNil: false,
		},
		{
			name: "date range",
			criteria: &imap.SearchCriteria{
				Since:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				Before: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
			},
			wantNil: false,
		},
		{
			name: "NOT criteria",
			criteria: &imap.SearchCriteria{
				Not: []imap.SearchCriteria{
					{Body: []string{"spam"}},
				},
			},
			wantNil: false,
		},
		{
			name: "OR criteria",
			criteria: &imap.SearchCriteria{
				Or: [][2]imap.SearchCriteria{
					{
						{Body: []string{"urgent"}},
						{Body: []string{"important"}},
					},
				},
			},
			wantNil: false,
		},
		{
			name: "combined subject and body",
			criteria: &imap.SearchCriteria{
				Header: []imap.SearchCriteriaHeaderField{
					{Key: "Subject", Value: "meeting"},
				},
				Body: []string{"agenda"},
			},
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := buildBleveQuery(tt.criteria)
			if tt.wantNil && q != nil {
				t.Errorf("buildBleveQuery() returned non-nil, want nil")
			}
			if !tt.wantNil && q == nil {
				t.Errorf("buildBleveQuery() returned nil, want non-nil")
			}
		})
	}
}

func TestBuildBleveQueryTypes(t *testing.T) {
	t.Run("single subject returns match query", func(t *testing.T) {
		criteria := &imap.SearchCriteria{
			Header: []imap.SearchCriteriaHeaderField{
				{Key: "Subject", Value: "test"},
			},
		}
		q := buildBleveQuery(criteria)
		if q == nil {
			t.Fatal("expected non-nil query")
		}
		if _, ok := q.(*query.MatchQuery); !ok {
			t.Errorf("expected *query.MatchQuery, got %T", q)
		}
	})

	t.Run("multiple criteria returns boolean query", func(t *testing.T) {
		criteria := &imap.SearchCriteria{
			Header: []imap.SearchCriteriaHeaderField{
				{Key: "Subject", Value: "test"},
			},
			Body: []string{"hello"},
		}
		q := buildBleveQuery(criteria)
		if q == nil {
			t.Fatal("expected non-nil query")
		}
		if _, ok := q.(*query.BooleanQuery); !ok {
			t.Errorf("expected *query.BooleanQuery, got %T", q)
		}
	})

	t.Run("NOT-only criteria returns boolean with match all", func(t *testing.T) {
		criteria := &imap.SearchCriteria{
			Not: []imap.SearchCriteria{
				{Body: []string{"spam"}},
			},
		}
		q := buildBleveQuery(criteria)
		if q == nil {
			t.Fatal("expected non-nil query")
		}
		if _, ok := q.(*query.BooleanQuery); !ok {
			t.Errorf("expected *query.BooleanQuery, got %T", q)
		}
	})
}

func TestMatchesFlagCriteria(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name     string
		flags    uint32
		date     int64
		criteria *imap.SearchCriteria
		want     bool
	}{
		{
			name:     "empty criteria matches all",
			flags:    0,
			date:     now,
			criteria: &imap.SearchCriteria{},
			want:     true,
		},
		{
			name:  "seen flag matches",
			flags: 1, // FlagSeen
			date:  now,
			criteria: &imap.SearchCriteria{
				Flag: []imap.Flag{imap.FlagSeen},
			},
			want: true,
		},
		{
			name:  "seen flag does not match unseen",
			flags: 0,
			date:  now,
			criteria: &imap.SearchCriteria{
				Flag: []imap.Flag{imap.FlagSeen},
			},
			want: false,
		},
		{
			name:  "not-seen matches unseen message",
			flags: 0,
			date:  now,
			criteria: &imap.SearchCriteria{
				NotFlag: []imap.Flag{imap.FlagSeen},
			},
			want: true,
		},
		{
			name:  "not-seen does not match seen message",
			flags: 1, // FlagSeen
			date:  now,
			criteria: &imap.SearchCriteria{
				NotFlag: []imap.Flag{imap.FlagSeen},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesFlagCriteria(tt.flags, tt.date, tt.criteria)
			if got != tt.want {
				t.Errorf("matchesFlagCriteria() = %v, want %v", got, tt.want)
			}
		})
	}
}
