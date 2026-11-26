package e2e

import "testing"

func TestTimeoutKeepsTaskResults(t *testing.T) {
	requireBash(t)
	dir := writeTasks(t, "Madefile.md", "### done\n```bash\nprintf '%s\\n' \"$@\"\nexit 7\n```\n")
	for _, limit := range []string{"0", "1m"} {
		t.Run(limit, func(t *testing.T) {
			stdout, stderr, code := runMade(t, dir, "--timeout", limit, "done", "--", "--timeout", "task argument")
			if code != 7 || stdout != "--timeout\ntask argument\n" || stderr != "" {
				t.Errorf("exit = %d, stdout = %q, stderr = %q; want 7 and unchanged arguments", code, stdout, stderr)
			}
		})
	}
}
