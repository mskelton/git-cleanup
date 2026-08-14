package streamer

import (
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestAppendRollingKeepsLastLines(t *testing.T) {
	var lines []string
	for _, line := range []string{"a", "b", "c"} {
		lines = appendRolling(lines, line, 2)
	}

	if got := strings.Join(lines, ","); got != "b,c" {
		t.Fatalf("got %q, want %q", got, "b,c")
	}
}

func TestEmitLinesSplitsOnCRAndLF(t *testing.T) {
	input := "Receiving objects: 10%\rReceiving objects: 20%\rReceiving objects: 100%, done.\nResolving deltas: 100%\r\n"
	var got []string
	emitLines(strings.NewReader(input), func(line string) {
		got = append(got, line)
	})

	want := []string{
		"Receiving objects: 10%",
		"Receiving objects: 20%",
		"Receiving objects: 100%, done.",
		"Resolving deltas: 100%",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestSanitizeLineStripsANSI(t *testing.T) {
	got := sanitizeLine("\x1b[31m  hello  \x1b[0m")
	if got != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestRunCommandStreamsIncrementally(t *testing.T) {
	cmd := exec.Command("sh", "-c", `i=1; while [ "$i" -le 3 ]; do printf 'line %s\n' "$i"; sleep 0.2; i=$((i+1)); done`)
	ch := make(chan string, 8)
	times := make([]time.Time, 0, 3)
	done := make(chan error, 1)

	go func() {
		done <- RunCommand(cmd, ch)
		close(ch)
	}()

	for line := range ch {
		times = append(times, time.Now())
		if !strings.HasPrefix(line, "line ") {
			t.Fatalf("unexpected line %q", line)
		}
	}

	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(times) != 3 {
		t.Fatalf("got %d lines, want 3", len(times))
	}
	if times[2].Sub(times[0]) < 300*time.Millisecond {
		t.Fatalf("lines arrived too fast (%s), output was probably buffered", times[2].Sub(times[0]))
	}
}

func TestTTYShowsTwoLinesThenCollapses(t *testing.T) {
	var buf strings.Builder
	s := NewOutputStreamer("Pulling latest changes")
	s.out = &buf
	s.isTTY = true
	s.animate = false
	s.start()

	s.addOutput("From github.com:mskelton/git-cleanup")
	s.addOutput("Receiving objects:  50%")
	s.addOutput("Already up to date.")

	got := visibleLines(buf.String())
	want := []string{
		s.frames[0] + " Pulling latest changes",
		"  Receiving objects:  50%",
		"  Already up to date.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("mid-run screen:\n%s\n\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	s.pass()

	got = visibleLines(buf.String())
	want = []string{"✔ Pulling latest changes"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("collapsed screen:\n%s\n\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func visibleLines(s string) []string {
	var lines [][]rune
	row, col := 0, 0

	ensure := func() {
		for len(lines) <= row {
			lines = append(lines, []rune{})
		}
	}

	write := func(r rune) {
		ensure()
		for len(lines[row]) < col {
			lines[row] = append(lines[row], ' ')
		}
		if col < len(lines[row]) {
			lines[row][col] = r
		} else {
			lines[row] = append(lines[row], r)
		}
		col++
	}

	clearToEnd := func() {
		ensure()
		if col < len(lines[row]) {
			lines[row] = lines[row][:col]
		}
	}

	clearLine := func() {
		ensure()
		lines[row] = nil
		col = 0
	}

	i := 0
	for i < len(s) {
		if strings.HasPrefix(s[i:], "\033[") {
			j := i + 2
			for j < len(s) && !((s[j] >= '@' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			if j >= len(s) {
				break
			}
			seq := s[i+2 : j]
			switch s[j] {
			case 'A':
				n := 1
				if seq != "" {
					n = atoiDefault(seq, 1)
				}
				row -= n
				if row < 0 {
					row = 0
				}
			case 'K':
				if seq == "2" {
					clearLine()
				} else {
					clearToEnd()
				}
			}
			i = j + 1
			continue
		}

		switch s[i] {
		case '\r':
			col = 0
			i++
		case '\n':
			row++
			col = 0
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			write(r)
			i += size
		}
	}

	out := make([]string, 0, len(lines))
	for _, line := range lines {
		text := strings.TrimRight(string(line), " ")
		if text != "" {
			out = append(out, text)
		}
	}
	return out
}

func atoiDefault(s string, fallback int) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
	}
	if n == 0 {
		return fallback
	}
	return n
}
