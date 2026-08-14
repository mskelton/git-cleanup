package streamer

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/briandowns/spinner"
	"github.com/creack/pty"
	"github.com/fatih/color"
	"github.com/mattn/go-isatty"
	"golang.org/x/term"
)

const (
	charSet         = 14
	maxDisplayLines = 2
	spinInterval    = 100 * time.Millisecond
)

var (
	ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	dim    = color.New(color.Faint)
)

type OutputStreamer struct {
	title   string
	out     io.Writer
	frames  []string
	frame   int
	lines   []string
	height  int
	isTTY   bool
	animate bool
	mu      sync.Mutex
	stop    chan struct{}
	done    chan struct{}
	stopped bool
}

func NewOutputStreamer(title string) *OutputStreamer {
	tty := isatty.IsTerminal(os.Stdout.Fd())
	return &OutputStreamer{
		title:   title,
		out:     os.Stdout,
		frames:  spinner.CharSets[charSet],
		lines:   make([]string, 0, maxDisplayLines),
		isTTY:   tty,
		animate: tty,
	}
}

func (o *OutputStreamer) start() {
	if !o.isTTY {
		fmt.Fprintln(o.out, o.title+"...")
		return
	}

	fmt.Fprint(o.out, "\033[?25l")
	o.mu.Lock()
	o.drawLocked()
	o.mu.Unlock()

	if !o.animate {
		return
	}

	o.stop = make(chan struct{})
	o.done = make(chan struct{})
	go o.spin()
}

func (o *OutputStreamer) spin() {
	defer close(o.done)

	ticker := time.NewTicker(spinInterval)
	defer ticker.Stop()

	for {
		select {
		case <-o.stop:
			return
		case <-ticker.C:
			o.mu.Lock()
			if !o.stopped {
				o.frame = (o.frame + 1) % len(o.frames)
				o.drawLocked()
			}
			o.mu.Unlock()
		}
	}
}

func (o *OutputStreamer) pass() {
	o.finish("\u2714 "+o.title, false)
}

func (o *OutputStreamer) fail() {
	o.finish(color.RedString("\u2716 "+o.title), true)
}

func (o *OutputStreamer) finish(msg string, _ bool) {
	if o.stop != nil {
		o.mu.Lock()
		o.stopped = true
		o.mu.Unlock()
		close(o.stop)
		<-o.done
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	if o.isTTY {
		o.clearLocked()
		fmt.Fprint(o.out, "\033[?25h")
	}

	fmt.Fprintln(o.out, msg)
}

func (o *OutputStreamer) addOutput(line string) {
	line = sanitizeLine(line)
	if line == "" {
		return
	}

	if !o.isTTY {
		fmt.Fprintln(o.out, "  "+dim.Sprint(line))
		return
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	o.lines = appendRolling(o.lines, line, maxDisplayLines)
	o.drawLocked()
}

func (o *OutputStreamer) drawLocked() {
	o.clearLocked()

	fmt.Fprintf(o.out, "%s %s\n", o.frames[o.frame], o.title)
	height := 1
	width := termWidth() - 2
	for _, line := range o.lines {
		fmt.Fprintf(o.out, "  %s\n", dim.Sprint(truncate(line, width)))
		height++
	}
	o.height = height
}

func (o *OutputStreamer) clearLocked() {
	for i := 0; i < o.height; i++ {
		fmt.Fprint(o.out, "\033[1A\033[2K")
	}
	o.height = 0
}

func handleCompletion(streamer *OutputStreamer, err error) {
	if err != nil {
		streamer.fail()
		for _, line := range strings.Split(err.Error(), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				fmt.Println(color.BlackString("  " + line))
			}
		}
		return
	}

	streamer.pass()
}

func Run(title string, operation func(chan<- string) error) {
	streamer := NewOutputStreamer(title)
	streamer.start()

	outputChan := make(chan string, 256)
	errChan := make(chan error, 1)

	go func() {
		errChan <- operation(outputChan)
		close(outputChan)
	}()

	for line := range outputChan {
		streamer.addOutput(line)
	}

	handleCompletion(streamer, <-errChan)
}

func RunCommand(cmd *exec.Cmd, outputChan chan<- string) error {
	cmd.Env = append(os.Environ(),
		"GIT_FLUSH=1",
		"GIT_PROGRESS_DELAY=0",
		"GIT_PAGER=cat",
		"PAGER=cat",
	)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return runCommandPipes(cmd, outputChan)
	}
	defer ptmx.Close()

	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: uint16(termWidth())})

	return collectCommandOutput(ptmx, cmd, outputChan)
}

func runCommandPipes(cmd *exec.Cmd, outputChan chan<- string) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var collected []string

	scan := func(r io.Reader) {
		emitLines(r, func(line string) {
			mu.Lock()
			collected = append(collected, line)
			mu.Unlock()
			outputChan <- line
		})
	}

	wg.Add(2)
	go func() {
		defer wg.Done()
		scan(stdout)
	}()
	go func() {
		defer wg.Done()
		scan(stderr)
	}()

	waitErr := cmd.Wait()
	wg.Wait()
	return commandResult(waitErr, collected)
}

func collectCommandOutput(r io.Reader, cmd *exec.Cmd, outputChan chan<- string) error {
	var collected []string
	done := make(chan struct{})

	go func() {
		defer close(done)
		emitLines(r, func(line string) {
			collected = append(collected, line)
			outputChan <- line
		})
	}()

	waitErr := cmd.Wait()
	if closer, ok := r.(io.Closer); ok {
		_ = closer.Close()
	}
	<-done

	return commandResult(waitErr, collected)
}

func commandResult(err error, collected []string) error {
	if err == nil {
		return nil
	}
	if msg := strings.TrimSpace(strings.Join(collected, "\n")); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	return err
}

func emitLines(r io.Reader, emit func(string)) {
	reader := bufio.NewReader(r)
	var buf strings.Builder

	flush := func() {
		line := sanitizeLine(buf.String())
		buf.Reset()
		if line != "" {
			emit(line)
		}
	}

	for {
		c, err := reader.ReadByte()
		if err != nil {
			flush()
			return
		}

		if c == '\n' || c == '\r' {
			flush()
			continue
		}

		buf.WriteByte(c)
	}
}

func appendRolling(lines []string, line string, limit int) []string {
	lines = append(lines, line)
	if len(lines) > limit {
		return append([]string{}, lines[len(lines)-limit:]...)
	}
	return lines
}

func sanitizeLine(line string) string {
	line = strings.ReplaceAll(line, "\t", " ")
	return strings.TrimSpace(ansiRE.ReplaceAllString(line, ""))
}

func truncate(line string, width int) string {
	if width <= 1 || utf8.RuneCountInString(line) <= width {
		return line
	}

	runes := []rune(line)
	return string(runes[:width-1]) + "…"
}

func termWidth() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		return 80
	}
	return width
}
