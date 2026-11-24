package cli

import (
	"bufio"
	"io"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// isYes reports whether an answer to a [y/N] question is yes; anything else,
// including Enter on its own, is no.
func isYes(s string) bool {
	a := strings.ToLower(strings.TrimSpace(s))
	return a == "y" || a == "yes"
}

// askYes reads the answer to a [y/N] question from r: a single key press when
// r is a terminal, otherwise a line (so `echo y | airs …` works). The key is
// echoed to w.
func askYes(r io.Reader, w io.Writer) bool {
	if f, ok := r.(*os.File); ok {
		if k, err := readKey(f); err == nil {
			if k >= ' ' && k < 0x7f {
				w.Write([]byte{k})
			}
			io.WriteString(w, "\n")
			return isYes(string(k))
		}
	}
	line, _ := bufio.NewReader(r).ReadString('\n')
	io.WriteString(w, "\n")
	return isYes(line)
}

// readKey reads one key press from terminal f without waiting for Enter. It
// fails when f isn't a terminal. Signal keys are turned off while it waits, so
// Ctrl-C arrives as a key (and counts as no) and the terminal is always
// restored.
func readKey(f *os.File) (byte, error) {
	fd := f.Fd()
	var old syscall.Termios
	if err := termios(fd, syscall.TIOCGETA, &old); err != nil {
		return 0, err
	}
	raw := old
	raw.Lflag &^= syscall.ICANON | syscall.ECHO | syscall.ISIG
	raw.Cc[syscall.VMIN], raw.Cc[syscall.VTIME] = 1, 0
	if err := termios(fd, syscall.TIOCSETA, &raw); err != nil {
		return 0, err
	}
	defer termios(fd, syscall.TIOCSETA, &old)
	var b [1]byte
	if _, err := f.Read(b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

func termios(fd uintptr, req uint64, t *syscall.Termios) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(req), uintptr(unsafe.Pointer(t))); e != 0 {
		return e
	}
	return nil
}
