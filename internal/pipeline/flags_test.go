package pipeline

import "testing"

func TestHasFlag(t *testing.T) {
	tests := []struct {
		name  string
		flags uint32
		flag  uint32
		want  bool
	}{
		{
			name:  "flag set",
			flags: MsgFlagSpamChecked,
			flag:  MsgFlagSpamChecked,
			want:  true,
		},
		{
			name:  "flag not set",
			flags: 0,
			flag:  MsgFlagSpamChecked,
			want:  false,
		},
		{
			name:  "multiple flags set, check one",
			flags: MsgFlagSpamChecked | MsgFlagVirus,
			flag:  MsgFlagVirus,
			want:  true,
		},
		{
			name:  "multiple flags set, check absent",
			flags: MsgFlagSpamChecked | MsgFlagVirus,
			flag:  MsgFlagQuarantined,
			want:  false,
		},
		{
			name:  "all flags set",
			flags: MsgFlagSpamChecked | MsgFlagVirusChecked | MsgFlagRulesChecked | MsgFlagSpam | MsgFlagVirus | MsgFlagQuarantined,
			flag:  MsgFlagRulesChecked,
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasFlag(tt.flags, tt.flag)
			if got != tt.want {
				t.Errorf("HasFlag(%b, %b) = %v, want %v", tt.flags, tt.flag, got, tt.want)
			}
		})
	}
}

func TestSetFlag(t *testing.T) {
	tests := []struct {
		name  string
		flags uint32
		flag  uint32
		want  uint32
	}{
		{
			name:  "set on zero",
			flags: 0,
			flag:  MsgFlagSpamChecked,
			want:  MsgFlagSpamChecked,
		},
		{
			name:  "set already set flag",
			flags: MsgFlagSpamChecked,
			flag:  MsgFlagSpamChecked,
			want:  MsgFlagSpamChecked,
		},
		{
			name:  "set second flag",
			flags: MsgFlagSpamChecked,
			flag:  MsgFlagVirusChecked,
			want:  MsgFlagSpamChecked | MsgFlagVirusChecked,
		},
		{
			name:  "set quarantine flag",
			flags: MsgFlagSpam | MsgFlagSpamChecked,
			flag:  MsgFlagQuarantined,
			want:  MsgFlagSpam | MsgFlagSpamChecked | MsgFlagQuarantined,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SetFlag(tt.flags, tt.flag)
			if got != tt.want {
				t.Errorf("SetFlag(%b, %b) = %b, want %b", tt.flags, tt.flag, got, tt.want)
			}
		})
	}
}

func TestClearFlag(t *testing.T) {
	tests := []struct {
		name  string
		flags uint32
		flag  uint32
		want  uint32
	}{
		{
			name:  "clear set flag",
			flags: MsgFlagSpamChecked,
			flag:  MsgFlagSpamChecked,
			want:  0,
		},
		{
			name:  "clear unset flag is no-op",
			flags: MsgFlagVirusChecked,
			flag:  MsgFlagSpamChecked,
			want:  MsgFlagVirusChecked,
		},
		{
			name:  "clear one of multiple flags",
			flags: MsgFlagSpamChecked | MsgFlagVirus | MsgFlagQuarantined,
			flag:  MsgFlagVirus,
			want:  MsgFlagSpamChecked | MsgFlagQuarantined,
		},
		{
			name:  "clear on zero is no-op",
			flags: 0,
			flag:  MsgFlagSpam,
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClearFlag(tt.flags, tt.flag)
			if got != tt.want {
				t.Errorf("ClearFlag(%b, %b) = %b, want %b", tt.flags, tt.flag, got, tt.want)
			}
		})
	}
}

func TestFlagConstants(t *testing.T) {
	// Verify each flag is a distinct power of two (no overlap).
	allFlags := []uint32{
		MsgFlagSpamChecked,
		MsgFlagVirusChecked,
		MsgFlagRulesChecked,
		MsgFlagSpam,
		MsgFlagVirus,
		MsgFlagQuarantined,
	}

	for i, a := range allFlags {
		// Each flag must be a non-zero power of two.
		if a == 0 || (a&(a-1)) != 0 {
			t.Errorf("flag[%d] = %b is not a power of two", i, a)
		}
		// No two flags may share bits.
		for j, b := range allFlags {
			if i != j && (a&b) != 0 {
				t.Errorf("flag[%d]=%b and flag[%d]=%b overlap", i, a, j, b)
			}
		}
	}
}
