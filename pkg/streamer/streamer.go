package streamer

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/briandowns/spinner"
	"github.com/fatih/color"
)

const (
	charSet         = 14
	maxDisplayLines = 2
)

type OutputStreamer struct {
	spinner *spinner.Spinner
	lines   []string
}

func NewOutputStreamer(title string) *OutputStreamer {
	s := spinner.New(spinner.CharSets[charSet], 100*time.Millisecond)
	s.Suffix = " " + title
	return &OutputStreamer{
		spinner: s,
		lines:   make([]string, 0),
	}
}

func (o *OutputStreamer) start() {
	o.spinner.Start()
}

func (o *OutputStreamer) stop() {
	o.spinner.Stop()
	o.clearOutput()
}

func (o *OutputStreamer) pass() {
	o.spinner.FinalMSG = "\u2714" + o.spinner.Suffix + "\n"
	o.stop()
}

func (o *OutputStreamer) fail() {
	o.spinner.FinalMSG = color.RedString("\u2716" + o.spinner.Suffix + "\n")
	o.stop()
}

func (o *OutputStreamer) addOutput(line string) {
	if len(line) > 0 {
		o.lines = append(o.lines, line)
		o.updateDisplay()
	}
}

func (o *OutputStreamer) clearOutput() {
	if len(o.lines) > 0 {
		// Clear the output lines by moving cursor up and clearing lines
		for i := 0; i < len(o.lines); i++ {
			fmt.Print("\033[1A\033[K") // Move up and clear line
		}
		o.lines = make([]string, 0)
	}
}

func (o *OutputStreamer) updateDisplay() {
	// Clear previous output lines
	o.clearOutput()

	// Display current output lines
	displayLines := o.lines
	if len(displayLines) > maxDisplayLines {
		displayLines = displayLines[len(displayLines)-maxDisplayLines:]
	}

	for _, line := range displayLines {
		if len(line) > 0 {
			fmt.Println(line)
		}
	}
}

func handleCompletion(streamer *OutputStreamer, err error) {
	if err != nil {
		streamer.fail()
		for _, line := range strings.Split(err.Error(), "\n") {
			fmt.Println(color.BlackString("  " + line))
		}
	} else {
		streamer.pass()
	}
}

func Run(title string, operation func(chan<- string) error) {
	streamer := NewOutputStreamer(title)
	streamer.start()

	// Create a channel to receive output from the operation
	outputChan := make(chan string, 100)

	// Run the operation in a goroutine
	errChan := make(chan error, 1)
	go func() {
		err := operation(outputChan)
		errChan <- err
		close(outputChan)
	}()

	// Stream output as it comes in
	for {
		select {
		case output, ok := <-outputChan:
			if !ok {
				// Channel closed, operation finished
				err := <-errChan
				handleCompletion(streamer, err)
				return
			}

			streamer.addOutput(output)
		case err := <-errChan:
			handleCompletion(streamer, err)
			return
		}
	}
}

func RunCommand(cmd *exec.Cmd, outputChan chan<- string) error {
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(output)))
	}

	return nil
}

func RunCommandStreaming(cmd *exec.Cmd, outputChan chan<- string) error {
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

	// Stream stdout
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if len(line) > 0 {
				outputChan <- line
			}
		}
	}()

	// Stream stderr
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if len(line) > 0 {
				outputChan <- line
			}
		}
	}()

	return cmd.Wait()
}
