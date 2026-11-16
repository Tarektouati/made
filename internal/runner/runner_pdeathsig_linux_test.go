package runner

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestSetParentDeathSignal(t *testing.T) {
	tests := []struct {
		name        string
		attr        *syscall.SysProcAttr
		wantSetpgid bool
	}{
		{name: "nil attr", attr: nil},
		{name: "keeps Setpgid", attr: &syscall.SysProcAttr{Setpgid: true}, wantSetpgid: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "true")
			cmd.SysProcAttr = tt.attr
			setParentDeathSignal(cmd)
			if cmd.SysProcAttr == nil {
				t.Fatal("SysProcAttr = nil, want it set")
			}
			if cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
				t.Errorf("Pdeathsig = %v, want SIGKILL", cmd.SysProcAttr.Pdeathsig)
			}
			if cmd.SysProcAttr.Setpgid != tt.wantSetpgid {
				t.Errorf("Setpgid = %v, want %v", cmd.SysProcAttr.Setpgid, tt.wantSetpgid)
			}
		})
	}
}
