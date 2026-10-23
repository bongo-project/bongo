package config

import (
	"strings"
	"testing"
)

func TestSRSMaxAgeDaysDefaultAndValidation(t *testing.T) {
	if got := Defaults().SMTP.SRSMaxAgeDays; got != 21 {
		t.Fatalf("default smtp.srs_max_age_days = %d, want 21", got)
	}
	tests := []struct {
		days    int
		wantErr bool
	}{
		{0, false}, {1, false}, {17, false}, {365, false}, {366, true}, {-1, true},
	}
	for _, tt := range tests {
		c := Defaults()
		c.SMTP.SRSMaxAgeDays = tt.days
		err := c.Validate()
		if tt.wantErr && (err == nil || !strings.Contains(err.Error(), "smtp.srs_max_age_days")) {
			t.Errorf("days=%d: err = %v, want smtp.srs_max_age_days error", tt.days, err)
		}
		if !tt.wantErr && err != nil && strings.Contains(err.Error(), "srs_max_age_days") {
			t.Errorf("days=%d: unexpected error %v", tt.days, err)
		}
	}
}

func TestSRSBounceLimitDefaultsAndValidation(t *testing.T) {
	d := Defaults().SMTP
	if d.SRSBouncesPerAddressPerHour != 5 || d.SRSBouncesPerHour != 200 {
		t.Fatalf("defaults = %d, %d; want 5, 200", d.SRSBouncesPerAddressPerHour, d.SRSBouncesPerHour)
	}
	tests := []struct {
		name    string
		perAddr int
		global  int
		want    string // substring of the expected error, "" for none
	}{
		{"zero disables both", 0, 0, ""},
		{"non-round values", 7, 313, ""},
		{"negative per address", -1, 10, "smtp.srs_bounces_per_address_per_hour"},
		{"negative global", 3, -1, "smtp.srs_bounces_per_hour"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Defaults()
			c.SMTP.SRSBouncesPerAddressPerHour = tt.perAddr
			c.SMTP.SRSBouncesPerHour = tt.global
			err := c.Validate()
			if tt.want == "" && err != nil && strings.Contains(err.Error(), "srs_bounces") {
				t.Errorf("unexpected error %v", err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSRSMaxAgeErrorSaysZeroMeansDefault(t *testing.T) {
	c := Defaults()
	c.SMTP.SRSMaxAgeDays = 400
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "0 means the default") {
		t.Errorf("err = %v, want it to explain that 0 means the default", err)
	}
}
