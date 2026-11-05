// SPDX-License-Identifier: GPL-3.0-or-later

package trace

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDisabledIsNoop(t *testing.T) {
	Disable()
	end := Begin("x")
	end()
	cmd := exec.Command("git", "--version")
	if err := Run(cmd); err != nil {
		t.Skip("git not available")
	}
	var buf bytes.Buffer
	Write(&buf, false)
	if buf.Len() != 0 {
		t.Fatalf("disabled trace wrote %q", buf.String())
	}
}

func TestStepsAndProcesses(t *testing.T) {
	Enable(time.Now())
	defer Disable()
	end := Begin("outer")
	if err := Run(exec.Command("git", "--version")); err != nil {
		t.Skip("git not available")
	}
	endInner := Begin("inner")
	_ = Run(exec.Command("git", "-C", ".", "-c", "core.autocrlf=false", "rev-parse", "--is-inside-work-tree"))
	_ = Run(exec.Command("git", "definitely-not-a-command"))
	endInner()
	end()

	rep, ok := Snapshot()
	if !ok {
		t.Fatal("no snapshot")
	}
	if len(rep.Steps) != 2 || rep.Steps[0].Name != "outer" || rep.Steps[1].Name != "inner" || rep.Steps[1].Depth != 1 {
		t.Fatalf("steps = %+v", rep.Steps)
	}
	if len(rep.Steps[0].Git) != 1 || len(rep.Steps[1].Git) != 2 || rep.GitProcesses != 3 {
		t.Fatalf("processes: outer %d inner %d total %d", len(rep.Steps[0].Git), len(rep.Steps[1].Git), rep.GitProcesses)
	}
	if got := rep.Steps[1].Git[0].Args; !reflect.DeepEqual(got, []string{"rev-parse", "--is-inside-work-tree"}) {
		t.Errorf("-C/-c noise not stripped: %v", got)
	}
	if rep.Steps[1].Git[1].ExitCode == 0 {
		t.Errorf("failed process must carry its exit code")
	}
	if rep.GitMillis > rep.TotalMillis+1 {
		t.Errorf("git %.1f ms exceeds total %.1f ms", rep.GitMillis, rep.TotalMillis)
	}
}

func TestTextAndJSONForm(t *testing.T) {
	Enable(time.Now())
	defer Disable()
	end := Begin("probe")
	if err := Run(exec.Command("git", "--version")); err != nil {
		t.Skip("git not available")
	}
	end()

	var text bytes.Buffer
	Write(&text, false)
	lines := strings.Split(strings.TrimRight(text.String(), "\n"), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "probe") || !strings.Contains(lines[1], "git --version") ||
		!strings.Contains(lines[2], "git: 1 processes, ") || !strings.Contains(lines[2], " ms of ") {
		t.Fatalf("text form:\n%s", text.String())
	}

	var js bytes.Buffer
	Write(&js, true)
	var rep Report
	if err := json.Unmarshal(js.Bytes(), &rep); err != nil {
		t.Fatalf("json: %v in %q", err, js.String())
	}
	if rep.SchemaVersion != SchemaVersion || rep.GitProcesses != 1 || len(rep.Steps) != 1 || rep.Steps[0].Name != "probe" {
		t.Fatalf("json form: %+v", rep)
	}
	if strings.Count(strings.TrimRight(js.String(), "\n"), "\n") != 0 {
		t.Errorf("json form must be one line")
	}
}

func TestProcessOutsideStepStillCounted(t *testing.T) {
	Enable(time.Now())
	defer Disable()
	if err := Run(exec.Command("git", "--version")); err != nil {
		t.Skip("git not available")
	}
	rep, _ := Snapshot()
	if rep.GitProcesses != 1 {
		t.Fatalf("processes = %d", rep.GitProcesses)
	}
}

func TestRedact(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{[]string{"-C", "/x", "-c", "a=b", "commit", "--quiet", "-m", "secret title"}, []string{"commit", "--quiet", "-m", "<message>"}},
		{[]string{"grep", "-I", "-n", "-F", "-i", "--", "my search", "HEAD", "--", "tickets/"}, []string{"grep", "-I", "-n", "-F", "-i", "--", "<pattern>", "HEAD", "--", "tickets/"}},
		{[]string{"remote", "add", "origin", "https://user:token@example.com/r.git"}, []string{"remote", "add", "origin", "https://example.com/r.git"}},
		{[]string{"show", "refs/heads/tickets:tickets/app/0000/app-1/ticket.md"}, []string{"show", "refs/heads/tickets:tickets/app/0000/app-1/ticket.md"}},
	}
	for _, c := range cases {
		if got := Redact(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Redact(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestEnvEnabled(t *testing.T) {
	for v, want := range map[string]bool{"": false, "0": false, "false": false, "off": false, "1": true, "true": true, "yes": true} {
		t.Setenv("DK_TRACE", v)
		if got := EnvEnabled(); got != want {
			t.Errorf("DK_TRACE=%q: %v, want %v", v, got, want)
		}
	}
}
