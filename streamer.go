package main

import (
	"bufio"
	"fmt"
	"os/exec"
	"time"

	"github.com/briandowns/spinner"
)

const charSet = 14

type outputStreamer struct {
	spinner *spinner.Spinner
	lines   []string
}

func newOutputStreamer(suffix string) *outputStreamer {
	s := spinner.New(spinner.CharSets[charSet], 100*time.Millisecond)
	s.Suffix = " " + suffix
	return &outputStreamer{
		spinner: s,
		lines:   make([]string, 0),
	}
}

func (o *outputStreamer) start() {
	o.spinner.Start()
}

func (o *outputStreamer) stop() {
	o.spinner.Stop()
	o.clearOutput()
}

func (o *outputStreamer) addOutput(line string) {
	if len(line) > 0 {
		o.lines = append(o.lines, line)
		o.updateDisplay()
	}
}

func (o *outputStreamer) clearOutput() {
	if len(o.lines) > 0 {
		// Clear the output lines by moving cursor up and clearing lines
		for i := 0; i < len(o.lines); i++ {
			fmt.Print("\033[1A\033[K") // Move up and clear line
		}
		o.lines = make([]string, 0)
	}
}

func (o *outputStreamer) updateDisplay() {
	// Clear previous output lines
	o.clearOutput()

	// Display current output lines (max 2 lines)
	displayLines := o.lines
	if len(displayLines) > 2 {
		displayLines = displayLines[len(displayLines)-2:]
	}

	for _, line := range displayLines {
		if len(line) > 0 {
			fmt.Println(line)
		}
	}
}

func runWithSpinner(suffix string, operation func(chan<- string) error) error {
	streamer := newOutputStreamer(suffix)
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
				streamer.stop()
				return <-errChan
			}
			streamer.addOutput(output)
		case err := <-errChan:
			streamer.stop()
			return err
		}
	}
}

func runCommandStreaming(cmd *exec.Cmd, outputChan chan<- string) error {
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

func runCommand(cmd *exec.Cmd, outputChan chan<- string) error {
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("command failed: %s\nOutput: %s", err, string(output))
	}
	return nil
}
