//go:build linux

package runner

import "testing"

func TestParseStatPPID(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stat    string
		want    int
		wantErr bool
	}{
		{"plain", "123 (bash) S 45 123 123 0", 45, false},
		{"spaces in comm", "123 (my prog) S 46 123 123 0", 46, false},
		{"parens in comm", "123 (a) (b)) R 47 1 1 0", 47, false},
		{"no comm", "123 bash S 45", 0, true},
		{"truncated", "123 (bash) S", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseStatPPID([]byte(tc.stat))
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseStatPPID(%q) error = %v, wantErr %v", tc.stat, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("parseStatPPID(%q) = %d, want %d", tc.stat, got, tc.want)
			}
		})
	}
}
