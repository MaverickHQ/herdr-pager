package e2e

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/devsim"
)

// measure runs the measurements, as PAGER_MEASURE=1 does: `go test ./e2e/
// -run TestN4BridgeFootprint -args -measure`, where setting an environment
// variable isn't possible.
var measure = flag.Bool("measure", false, "run the measurements, as PAGER_MEASURE=1 does")

// The N4 budget (parent N4): RSS at most 40 MiB, and on average at most 1 %
// of one core, with 10 agents polled at 1 Hz; `ps -o rss=,%cpu=` every 10 s
// for 5 minutes.
const (
	n4MaxRSSKiB = 40 * 1024
	n4MaxCPU    = 1.0
	n4Sample    = 10 * time.Second
	n4Samples   = 30
)

// TestN4BridgeFootprint measures N4: pager-bridge serve, built as it ships,
// with fakeherdr serving 10 agents (a scenario, churning every 10 s) and one
// paired device connected that reads every line and pings every minute, as a
// pager does. It samples `ps -o rss=,%cpu=` every 10 s for 5 minutes, and
// wants the largest RSS within 40 MiB and the average %cpu within 1 %. It
// also logs the CPU time over the run. It runs only with PAGER_MEASURE=1 (or
// -measure): task 4.29 runs it once and records the result in
// docs/measurements.md. The binary measured is pager-bridge itself, built
// for the run.
func TestN4BridgeFootprint(t *testing.T) {
	if os.Getenv("PAGER_MEASURE") != "1" && !*measure {
		t.Skip("N4 takes 5 minutes: PAGER_MEASURE=1 (or -args -measure) runs it")
	}
	bin := t.TempDir()
	home, err := os.MkdirTemp("", "hp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	gobuild := func(args ...string) {
		t.Helper()
		cmd := exec.Command("go", args...)
		cmd.Dir = ".."
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	gobuild("build", "-o", filepath.Join(bin, "fakeherdr"), "./cmd/fakeherdr")
	gobuild("build", "-o", filepath.Join(bin, "pager-bridge"), "./cmd/pager-bridge")
	bridge := exec.Command(filepath.Join(bin, "pager-bridge"), "serve")

	// The bridge's directory, as init and provision leave it.
	dir := filepath.Join(home, ".config", "herdr-pager")
	socket := filepath.Join(home, "herdr.sock")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	hostKey, _ := ssh.NewSignerFromKey(priv)
	device := newKey(t)
	for name, content := range map[string]string{
		"config.toml":  fmt.Sprintf("listen = \"127.0.0.1:0\"\nherdr_socket = %q\n", socket),
		"host_ed25519": string(pem.EncodeToMemory(block)),
		"devices.toml": fmt.Sprintf("[[device]]\nname  = \"pager-01\"\nkey   = %q\nadded = 2026-10-22T10:00:00Z\n",
			strings.TrimSpace(string(ssh.MarshalAuthorizedKey(device.PublicKey())))),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Ten agents, each with a detection capture, churning every 10 s.
	capture, err := filepath.Abs("../testdata/herdr/claude/detection/shell-0s.txt")
	if err != nil {
		t.Fatal(err)
	}
	var scenario strings.Builder
	for i := range 10 {
		fmt.Fprintf(&scenario, "[[agent]]\npane = \"w%d:p%d\"\nkind = \"claude\"\nname = \"agent-%02d\"\ntitle = \"Bash command\"\nstatus = %q\ndetection = %q\n\n",
			i/4+1, i%4+1, i+1, []string{"working", "blocked", "done", "idle"}[i%4], capture)
	}
	scenarioFile := filepath.Join(home, "scenario.toml")
	if err := os.WriteFile(scenarioFile, []byte(scenario.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := exec.Command(filepath.Join(bin, "fakeherdr"), "--socket", socket, "--scenario", scenarioFile, "--churn", "10s")
	if err := fake.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fake.Process.Kill(); fake.Wait() })
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fakeherdr made no socket within 5 s")
		}
	}

	stderr := captureLog(t)
	bridge.Stderr = stderr
	if bridge.Env == nil {
		bridge.Env = os.Environ()
	}
	bridge.Env = append(bridge.Env, "HOME="+home)
	if err := bridge.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bridge.Process.Kill(); bridge.Wait() })
	listening := regexp.MustCompile(`msg="serve: listening" addr=(\S+)`)
	var addr string
	for deadline := time.Now().Add(10 * time.Second); addr == ""; time.Sleep(10 * time.Millisecond) {
		if m := listening.FindStringSubmatch(stderr.String()); m != nil {
			addr = m[1]
		} else if time.Now().After(deadline) {
			t.Fatalf("serve didn't listen within 10 s; stderr:\n%s", stderr)
		}
	}
	dev, err := devsim.Dial(addr, device, hostKey.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	dev.Send(`{"t":"hello","v":1,"fw":"0.1.0","name":"pager-01"}`)
	var snaps atomic.Int64
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if line, err := dev.Recv(time.Second); err == nil && strings.HasPrefix(line, `{"t":"snap"`) {
				snaps.Add(1)
			}
		}
	}()
	go func() {
		for id := 1; ; id++ {
			select {
			case <-stop:
				return
			case <-time.After(time.Minute):
				dev.Send(fmt.Sprintf(`{"t":"ping","id":%d}`, id))
			}
		}
	}()

	pid := strconv.Itoa(bridge.Process.Pid)
	ps := func(fields string) string {
		t.Helper()
		out, err := exec.Command("ps", "-o", fields, "-p", pid).Output()
		if err != nil {
			t.Fatalf("ps -o %s -p %s: %v; serve's stderr:\n%s", fields, pid, err, stderr)
		}
		return strings.TrimSpace(string(out))
	}
	cpuStart, wallStart := cpuTime(t, ps("time=")), time.Now()
	maxRSS, sumCPU, maxCPU := 0, 0.0, 0.0
	for i := 1; i <= n4Samples; i++ {
		time.Sleep(n4Sample)
		f := strings.Fields(ps("rss=,%cpu="))
		if len(f) != 2 {
			t.Fatalf("ps printed %q; want the RSS and %%cpu", f)
		}
		rss, err1 := strconv.Atoi(f[0])
		cpu, err2 := strconv.ParseFloat(f[1], 64)
		if err1 != nil || err2 != nil {
			t.Fatalf("ps printed %q", f)
		}
		maxRSS, sumCPU, maxCPU = max(maxRSS, rss), sumCPU+cpu, max(maxCPU, cpu)
		t.Logf("sample %2d: RSS %5.1f MiB, %%cpu %.1f", i, float64(rss)/1024, cpu)
	}
	cpuUsed, wall := cpuTime(t, ps("time="))-cpuStart, time.Since(wallStart)
	avgCPU := sumCPU / n4Samples
	t.Logf("N4 over %v: RSS max %.1f MiB (budget %d MiB); %%cpu average %.2f %%, max %.1f %% (budget %.0f %%); CPU time %v, %.2f %% of one core; %d snapshots to the device",
		wall.Round(time.Second), float64(maxRSS)/1024, n4MaxRSSKiB/1024, avgCPU, maxCPU, n4MaxCPU, cpuUsed, 100*cpuUsed.Seconds()/wall.Seconds(), snaps.Load())
	if maxRSS > n4MaxRSSKiB || avgCPU > n4MaxCPU {
		t.Errorf("N4 FAIL: RSS max %.1f MiB, average %%cpu %.2f %%; want at most %d MiB and %.0f %%", float64(maxRSS)/1024, avgCPU, n4MaxRSSKiB/1024, n4MaxCPU)
	}
}

// cpuTime parses ps's cumulative CPU time, [[dd-]hh:]mm:ss.cc.
func cpuTime(t *testing.T, s string) time.Duration {
	t.Helper()
	var d time.Duration
	days, rest, ok := strings.Cut(s, "-")
	if !ok {
		days, rest = "0", s
	}
	n, err := strconv.Atoi(days)
	if err != nil {
		t.Fatalf("ps time %q", s)
	}
	d += time.Duration(n) * 24 * time.Hour
	parts := strings.Split(rest, ":")
	units := []time.Duration{time.Second, time.Minute, time.Hour}
	for i := range parts {
		v, err := strconv.ParseFloat(parts[len(parts)-1-i], 64)
		if err != nil || i >= len(units) {
			t.Fatalf("ps time %q", s)
		}
		d += time.Duration(v * float64(units[i]))
	}
	return d
}
