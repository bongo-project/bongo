package sendmail

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want Options
	}{
		{"none", nil, Options{ConfigPath: DefaultConfigPath}},
		{"php", []string{"-t", "-i"}, Options{ReadRecipients: true, IgnoreDots: true, ConfigPath: DefaultConfigPath}},
		{"oi", []string{"-oi", "a@example.net"}, Options{IgnoreDots: true, Recipients: []string{"a@example.net"}, ConfigPath: DefaultConfigPath}},
		{"debian cron", []string{"-FCronDaemon", "-i", "-B8BITMIME", "-oem", "-oi", "a@example.net"},
			Options{FullName: "CronDaemon", IgnoreDots: true, Recipients: []string{"a@example.net"}, ConfigPath: DefaultConfigPath}},
		{"cronie", []string{"-f", "root", "-odi", "-odb", "-v", "-bm", "a@example.net"},
			Options{Sender: "root", Recipients: []string{"a@example.net"}, ConfigPath: DefaultConfigPath}},
		{"attached f and separate F", []string{"-fme@example.net", "-F", "Me Too", "a@example.net"},
			Options{Sender: "me@example.net", FullName: "Me Too", Recipients: []string{"a@example.net"}, ConfigPath: DefaultConfigPath}},
		{"angle brackets on -f", []string{"-f", "<me@example.net>"}, Options{Sender: "me@example.net", ConfigPath: DefaultConfigPath}},
		{"B with separate value", []string{"-B", "8BITMIME", "a@example.net"}, Options{Recipients: []string{"a@example.net"}, ConfigPath: DefaultConfigPath}},
		{"config separate", []string{"--config", "/x/y.conf"}, Options{ConfigPath: "/x/y.conf"}},
		{"config equals", []string{"--config=/x/y.conf"}, Options{ConfigPath: "/x/y.conf"}},
		{"double dash ends flags", []string{"--", "-t@example.net"}, Options{Recipients: []string{"-t@example.net"}, ConfigPath: DefaultConfigPath}},
		{"display-name recipient", []string{"Bob <bob@example.net>"}, Options{Recipients: []string{"bob@example.net"}, ConfigPath: DefaultConfigPath}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseArgs(tt.args)
			if err != nil {
				t.Fatalf("ParseArgs: %v", err)
			}
			if !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("got %+v, want %+v", *got, tt.want)
			}
		})
	}
}

func TestParseArgsUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"bs", []string{"-bs"}},
		{"unknown flag", []string{"-x"}},
		{"unknown o flag", []string{"-oX"}},
		{"lone dash", []string{"-"}},
		{"-f missing value", []string{"-f"}},
		{"-F missing value", []string{"-F"}},
		{"--config missing value", []string{"--config"}},
		{"empty config", []string{"--config="}},
		{"CR in -F", []string{"-FA\rB"}},
		{"LF in -F", []string{"-F", "A\nBcc: x@example.net"}},
		{"LF in -f", []string{"-f", "a@example.net\nX: y"}},
		{"NUL in -f", []string{"-f", "a@example.net\x00"}},
		{"unqualified recipient", []string{"root"}},
		{"CRLF in recipient", []string{"a@example.net\r\nBcc: b@example.net"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseArgs(tt.args)
			var ee *ExitError
			if !errors.As(err, &ee) || ee.Code != ExitUsage {
				t.Fatalf("err = %v, want EX_USAGE", err)
			}
		})
	}
}

func TestIsInvokedAsSendmail(t *testing.T) {
	tests := map[string]bool{
		"sendmail":                true,
		"/usr/sbin/sendmail":      true,
		"./sendmail":              true,
		"bongo":                   false,
		"/usr/local/bin/bongo":    false,
		"/usr/sbin/sendmail.real": false,
		"mysendmail":              false,
	}
	for argv0, want := range tests {
		if got := IsInvokedAsSendmail(argv0); got != want {
			t.Errorf("IsInvokedAsSendmail(%q) = %v, want %v", argv0, got, want)
		}
	}
}
