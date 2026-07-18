package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const defaultTerminalHeight = 24

// Page writes output directly or through an external pager. In auto mode a
// pager is used only for a terminal and only when the output is taller than
// the terminal. Custom pager commands follow the conventional BUNNY_PAGER,
// then PAGER, environment-variable order.
func Page(w io.Writer, output, mode string) error {
	if mode == "" {
		mode = "auto"
	}
	if mode != "auto" && mode != "always" && mode != "never" {
		return fmt.Errorf("invalid pager mode %q", mode)
	}
	// Piped/redirected output must remain machine-friendly. Even "always"
	// only forces paging on an interactive terminal.
	if mode == "never" || !isTerminal(w) || (mode == "auto" && !shouldPage(w, output)) {
		_, err := io.WriteString(w, output)
		return err
	}

	command, custom, ok := pagerCommand()
	if !ok {
		_, err := io.WriteString(w, output)
		return err
	}
	var cmd *exec.Cmd
	if custom {
		cmd = exec.Command("sh", "-c", command)
	} else if command == "less" {
		cmd = exec.Command(command, "-FRX")
	} else {
		cmd = exec.Command(command)
	}
	cmd.Stdin = strings.NewReader(output)
	cmd.Stdout = w
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil && !errors.Is(err, syscall.EPIPE) {
		return fmt.Errorf("pager: %w", err)
	}
	return nil
}

func shouldPage(w io.Writer, output string) bool {
	f := w.(*os.File) // Page verifies this with isTerminal first.
	height := defaultTerminalHeight
	if size, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ); err == nil && size.Row > 0 {
		height = int(size.Row)
	}
	lines := strings.Count(output, "\n")
	if output != "" && !strings.HasSuffix(output, "\n") {
		lines++
	}
	return lines >= height
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	return true
}

func pagerCommand() (command string, custom, ok bool) {
	if command = strings.TrimSpace(os.Getenv("BUNNY_PAGER")); command != "" {
		return command, true, true
	}
	if command = strings.TrimSpace(os.Getenv("PAGER")); command != "" {
		return command, true, true
	}
	if _, err := exec.LookPath("less"); err == nil {
		return "less", false, true
	}
	if _, err := exec.LookPath("more"); err == nil {
		return "more", false, true
	}
	return "", false, false
}
