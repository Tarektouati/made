//go:build linux

package runner

import "testing"

func TestParseStat(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stat    string
		want    procStat
		wantErr bool
	}{
		{"plain", "123 (bash) S 45 123 123 0", procStat{'S', 45, 123}, false},
		{"zombie", "123 (sleep) Z 45 99 99 0", procStat{'Z', 45, 99}, false},
		{"spaces in comm", "123 (my prog) S 46 123 123 0", procStat{'S', 46, 123}, false},
		{"parens in comm", "123 (a) (b)) R 47 1 1 0", procStat{'R', 47, 1}, false},
		{"no comm", "123 bash S 45 1", procStat{}, true},
		{"truncated", "123 (bash) S 45", procStat{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseStat([]byte(tc.stat))
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseStat(%q) error = %v, wantErr %v", tc.stat, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("parseStat(%q) = %+v, want %+v", tc.stat, got, tc.want)
			}
		})
	}
}
