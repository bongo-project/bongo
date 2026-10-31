package config

import (
	"testing"
	"time"
)

func TestQueueLimitAccessors(t *testing.T) {
	var nilCfg *QueueConfig
	tests := []struct {
		name     string
		c        *QueueConfig
		lifetime time.Duration
		retry    time.Duration
		conc     int
	}{
		{"nil", nilCfg, 120 * time.Hour, time.Hour, 5},
		{"zero value", &QueueConfig{}, 120 * time.Hour, time.Hour, 5},
		{"unparseable", &QueueConfig{MaxLifetime: "abc", MaxRetryInterval: "x", Concurrency: 99}, 120 * time.Hour, time.Hour, 5},
		{"set", &QueueConfig{MaxLifetime: "2d", MaxRetryInterval: "30m", Concurrency: 12}, 48 * time.Hour, 30 * time.Minute, 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.QueueMaxLifetime(); got != tt.lifetime {
				t.Errorf("lifetime = %v, want %v", got, tt.lifetime)
			}
			if got := tt.c.QueueMaxRetryInterval(); got != tt.retry {
				t.Errorf("retry = %v, want %v", got, tt.retry)
			}
			if got := tt.c.QueueConcurrency(); got != tt.conc {
				t.Errorf("concurrency = %d, want %d", got, tt.conc)
			}
		})
	}
}

func TestValidateQueueLimits(t *testing.T) {
	tests := []struct {
		name    string
		q       *QueueConfig
		wantErr bool
	}{
		{"no block", nil, false},
		{"poll only", &QueueConfig{PollInterval: "5s"}, false},
		{"5d", &QueueConfig{MaxLifetime: "5d"}, false},
		{"120h", &QueueConfig{MaxLifetime: "120h"}, false},
		{"1h retry", &QueueConfig{MaxRetryInterval: "1h"}, false},
		{"0d", &QueueConfig{MaxLifetime: "0d"}, true},
		{"negative lifetime", &QueueConfig{MaxLifetime: "-1h"}, true},
		{"junk lifetime", &QueueConfig{MaxLifetime: "abc"}, true},
		{"negative retry", &QueueConfig{MaxRetryInterval: "-1h"}, true},
		{"junk retry", &QueueConfig{MaxRetryInterval: "abc"}, true},
		{"concurrency -1", &QueueConfig{Concurrency: -1}, true},
		{"concurrency 0", &QueueConfig{Concurrency: 0}, false},
		{"concurrency 1", &QueueConfig{Concurrency: 1}, false},
		{"concurrency 64", &QueueConfig{Concurrency: 64}, false},
		{"concurrency 65", &QueueConfig{Concurrency: 65}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Queue = tt.q
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
