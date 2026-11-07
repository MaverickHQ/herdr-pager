package measure

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// A Line is one line from the board, stamped on the Mac as it arrived.
type Line struct {
	At   time.Time
	Text string
}

// Serial is the board's port, open in raw mode. Opening it resets the board.
// When the board restarts its USB port goes away; Serial opens it again, for
// up to 30 s, and goes on. Every line is logged, stamped on the Mac, to
// OutDir/<name>.serial.log, with the Wi-Fi networks' names and passwords masked.
type Serial struct {
	port  string
	lines chan Line
	t     testing.TB

	mu     sync.Mutex
	f      *os.File
	tee    *os.File // a pty's master (Proxy): the board's bytes are copied to it
	slave  *os.File // the pty's other end, held open while the program comes and goes
	log    *os.File
	secret []string // longest first
	closed bool
}

// Open opens port and closes it when t ends.
func Open(t testing.TB, port, name string) *Serial {
	t.Helper()
	f, err := openRaw(port)
	if err != nil {
		t.Fatal(err)
	}
	logf, err := os.Create(filepath.Join(OutDir(t), name+".serial.log"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Serial{port: port, f: f, lines: make(chan Line, 1<<16), log: logf, t: t}
	for _, n := range readWifiIfAny() {
		s.secret = append(s.secret, n.SSID, n.Pass)
	}
	sort.Slice(s.secret, func(i, j int) bool { return len(s.secret[i]) > len(s.secret[j]) })
	go s.read()
	t.Cleanup(s.Close)
	return s
}

// Close closes the port; the reader stops.
func (s *Serial) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.f.Close()
	s.log.Close()
}

// Mask replaces the Wi-Fi networks' names and passwords in text.
func (s *Serial) Mask(text string) string {
	for _, sec := range s.secret {
		if len(sec) >= 3 {
			text = strings.ReplaceAll(text, sec, "[network]")
		}
	}
	return text
}

func (s *Serial) note(at time.Time, format string, a ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		fmt.Fprintf(s.log, "%s %s\n", at.Format("15:04:05.000"), s.Mask(fmt.Sprintf(format, a...)))
	}
}

// openRaw opens port in raw mode with DTR and RTS low: esptool's "run" state,
// as the kit's boardtest does (its internal/pty.OpenSerial).
func openRaw(port string) (*os.File, error) {
	fd, err := syscall.Open(port, syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", port, err)
	}
	fail := func(what string, err error) (*os.File, error) {
		syscall.Close(fd)
		return nil, fmt.Errorf("%s on %s: %w", what, port, err)
	}
	if err := makeRaw(fd); err != nil {
		return fail("raw mode", err)
	}
	if err := syscall.SetNonblock(fd, false); err != nil {
		return fail("blocking mode", err)
	}
	modem := int32(syscall.TIOCM_DTR | syscall.TIOCM_RTS)
	if err := ioctl(fd, syscall.TIOCMBIC, unsafe.Pointer(&modem)); err != nil && err != syscall.ENOTTY && err != syscall.EINVAL {
		return fail("lowering DTR and RTS", err)
	}
	return os.NewFile(uintptr(fd), port), nil
}

func makeRaw(fd int) error {
	var tio syscall.Termios
	if err := ioctl(fd, syscall.TIOCGETA, unsafe.Pointer(&tio)); err != nil {
		return err
	}
	tio.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	tio.Oflag &^= syscall.OPOST
	tio.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	tio.Cflag &^= syscall.CSIZE | syscall.PARENB
	tio.Cflag |= syscall.CS8 | syscall.CLOCAL | syscall.CREAD
	tio.Cc[syscall.VMIN] = 1
	tio.Cc[syscall.VTIME] = 0
	return ioctl(fd, syscall.TIOCSETA, unsafe.Pointer(&tio))
}

func ioctl(fd int, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

func (s *Serial) read() {
	var pending []byte
	chunk := make([]byte, 4096)
	for {
		s.mu.Lock()
		f := s.f
		s.mu.Unlock()
		n, err := f.Read(chunk)
		if n > 0 {
			s.mu.Lock()
			tee := s.tee
			s.mu.Unlock()
			if tee != nil {
				tee.Write(chunk[:n]) // EndProxy's close ends a write the pty's full buffer blocks
			}
			pending = append(pending, chunk[:n]...)
			for i := bytes.IndexByte(pending, '\n'); i >= 0; i = bytes.IndexByte(pending, '\n') {
				s.line(string(pending[:i]))
				pending = pending[i+1:]
			}
		}
		if err != nil && !s.reopen() {
			close(s.lines)
			return
		}
	}
}

func (s *Serial) line(text string) {
	if text = strings.TrimRight(text, "\r"); text == "" {
		return
	}
	l := Line{At: time.Now(), Text: text}
	s.note(l.At, "%s", text)
	select {
	case s.lines <- l:
	default:
		s.note(l.At, "(a line dropped: nobody read the last %d)", cap(s.lines))
	}
}

// reopen waits up to 30 s for the port to come back after the board restarted.
func (s *Serial) reopen() bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.f.Close()
	s.mu.Unlock()
	s.note(time.Now(), "(the USB link dropped; reopening %s)", s.port)
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		f, err := openRaw(s.port)
		if err != nil {
			continue
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			f.Close()
			return false
		}
		s.f = f
		s.mu.Unlock()
		s.note(time.Now(), "(reopened %s)", s.port)
		return true
	}
	s.note(time.Now(), "(%s didn't come back within 30 s)", s.port)
	return false
}

func (s *Serial) write(p []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.f.Write(p)
	return err
}

// Send writes line and a newline to the board, logs it, and returns when it was written.
func (s *Serial) Send(line string) time.Time {
	s.t.Helper()
	if err := s.write([]byte(line + "\n")); err != nil {
		s.t.Fatalf("sending %q: %v", line, err)
	}
	at := time.Now()
	s.note(at, "> %s", line)
	return at
}

// Proxy lends the port to another program through a pty, and returns the
// pty's name for it to open instead: the board's bytes go to the pty too,
// and the pty's to the board. Those are logged by size only (provision's
// carry the Wi-Fi passwords). Without the program opening the real port,
// nothing resets the board, and Send can still reach it.
func (s *Serial) Proxy() string {
	s.t.Helper()
	mfd, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		s.t.Fatalf("a pty: %v", err)
	}
	if err := ioctl(mfd, syscall.TIOCPTYGRANT, nil); err != nil {
		s.t.Fatalf("grant pty: %v", err)
	}
	if err := ioctl(mfd, syscall.TIOCPTYUNLK, nil); err != nil {
		s.t.Fatalf("unlock pty: %v", err)
	}
	var buf [128]byte
	if err := ioctl(mfd, syscall.TIOCPTYGNAME, unsafe.Pointer(&buf[0])); err != nil {
		s.t.Fatalf("pty name: %v", err)
	}
	name := string(buf[:bytes.IndexByte(buf[:], 0)])
	// Held open, raw, so the pty survives the program closing it.
	sfd, err := syscall.Open(name, syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		s.t.Fatalf("open %s: %v", name, err)
	}
	if err := makeRaw(sfd); err != nil {
		s.t.Fatalf("%s: raw mode: %v", name, err)
	}
	master, slave := os.NewFile(uintptr(mfd), "/dev/ptmx"), os.NewFile(uintptr(sfd), name)
	s.mu.Lock()
	s.tee, s.slave = master, slave
	s.mu.Unlock()
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := master.Read(b)
			if n > 0 {
				s.write(b[:n])
				s.note(time.Now(), "(%d bytes from %s to the board)", n, name)
			}
			if err != nil {
				return
			}
		}
	}()
	s.t.Cleanup(s.EndProxy)
	return name
}

// EndProxy closes the pty: call it once the program has finished with it,
// or the board's bytes fill the pty's buffer.
func (s *Serial) EndProxy() {
	s.mu.Lock()
	tee, slave := s.tee, s.slave
	s.tee, s.slave = nil, nil
	s.mu.Unlock()
	if tee != nil {
		slave.Close()
		tee.Close()
	}
}

// Reset resets the board as esptool's hard reset does (RTS high, so EN low,
// for 200 ms, with DTR low), and returns when it let go.
func (s *Serial) Reset() time.Time {
	s.t.Helper()
	s.mu.Lock()
	fd := int(s.f.Fd())
	s.mu.Unlock()
	rts := int32(syscall.TIOCM_RTS)
	if err := ioctl(fd, syscall.TIOCMBIS, unsafe.Pointer(&rts)); err != nil {
		s.t.Fatalf("raising RTS: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := ioctl(fd, syscall.TIOCMBIC, unsafe.Pointer(&rts)); err != nil {
		s.t.Fatalf("lowering RTS: %v", err)
	}
	at := time.Now()
	s.note(at, "(reset)")
	return at
}

// Wait returns the first line matching re within timeout, failing t if none came.
func (s *Serial) Wait(re string, timeout time.Duration) Line {
	s.t.Helper()
	l, ok := s.WaitFor(re, timeout)
	if !ok {
		s.t.Fatalf("no line matching /%s/ within %v (the serial log is %s)", re, timeout, s.log.Name())
	}
	return l
}

// WaitFor is Wait without failing: ok is false if no line matched in time.
func (s *Serial) WaitFor(re string, timeout time.Duration) (Line, bool) {
	rx := regexp.MustCompile(re)
	deadline := time.After(timeout)
	for {
		select {
		case l, ok := <-s.lines:
			if !ok {
				return Line{}, false
			}
			if rx.MatchString(l.Text) {
				return l, true
			}
		case <-deadline:
			return Line{}, false
		}
	}
}

// Collect returns every line that arrives within d.
func (s *Serial) Collect(d time.Duration) []Line {
	var got []Line
	deadline := time.After(d)
	for {
		select {
		case l, ok := <-s.lines:
			if !ok {
				return got
			}
			got = append(got, l)
		case <-deadline:
			return got
		}
	}
}
