package main

import (
	"errors"
	"flag"
	"fmt"
	"time"
)

// smtpConfig holds the flags of the smtp subcommand. The names other than
// --rate and --ramp carry an smtp- prefix because all subcommands share one
// FlagSet and a duplicate name would panic for every subcommand.
type smtpConfig struct {
	Rate     float64
	Ramp     string
	Duration time.Duration
	Workers  int
	Timeout  time.Duration
	Summary  string
	Size     int
}

func registerSMTPFlags(fs *flag.FlagSet, c *smtpConfig) {
	fs.Float64Var(&c.Rate, "rate", 0, "smtp: messages per second to hold (mutually exclusive with --ramp)")
	fs.StringVar(&c.Ramp, "ramp", "", "smtp: start:step:interval:max, for example 50:50:30s:500; raises the rate by step every interval, then holds max until --smtp-duration ends")
	fs.DurationVar(&c.Duration, "smtp-duration", 10*time.Minute, "smtp: how long to send")
	fs.IntVar(&c.Workers, "smtp-workers", 32, "smtp: size of the bounded connection pool; each worker keeps one connection")
	fs.DurationVar(&c.Timeout, "smtp-timeout", 30*time.Second, "smtp: per-command timeout; also bounds dialling and the grace given to in-flight sends on shutdown")
	fs.StringVar(&c.Summary, "smtp-summary", "loadgen-smtp-summary.json", "smtp: path of the JSON summary, written on every exit including SIGINT and SIGTERM")
	fs.IntVar(&c.Size, "smtp-size", 1024, "smtp: message body size in bytes")
}

// validateSMTP checks what only smtp needs. Validate lets the target, sender
// and recipient lists be empty because check does not send; sending needs all
// three. It returns the schedule the flags describe.
func validateSMTP(cfg Config) (*schedule, error) {
	c := cfg.SMTP
	var sched *schedule
	switch {
	case c.Rate != 0 && c.Ramp != "":
		return nil, errors.New("--rate and --ramp are mutually exclusive")
	case c.Ramp != "":
		r, err := parseRamp(c.Ramp)
		if err != nil {
			return nil, err
		}
		sched = rampSchedule(r)
	case c.Rate != 0:
		if err := checkRate(c.Rate); err != nil {
			return nil, fmt.Errorf("--rate: %w", err)
		}
		sched = constSchedule(c.Rate)
	default:
		return nil, errors.New("one of --rate or --ramp is required")
	}
	if len(cfg.Targets) == 0 {
		return nil, errors.New("smtp needs at least one --target")
	}
	if len(cfg.Senders) == 0 {
		return nil, errors.New("smtp needs at least one --from")
	}
	if len(cfg.Recipients) == 0 {
		return nil, errors.New("smtp needs at least one --rcpt")
	}
	if c.Duration <= 0 {
		return nil, errors.New("--smtp-duration must be above 0")
	}
	if c.Workers < 1 || c.Workers > 4096 {
		return nil, errors.New("--smtp-workers must be 1-4096")
	}
	if c.Timeout <= 0 {
		return nil, errors.New("--smtp-timeout must be above 0")
	}
	if c.Size < 0 || c.Size > 8<<20 {
		return nil, errors.New("--smtp-size must be 0-8388608")
	}
	if c.Summary == "" {
		return nil, errors.New("--smtp-summary must not be empty")
	}
	return sched, nil
}
