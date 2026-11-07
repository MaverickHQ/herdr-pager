package provision

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// TestProvisionDefaultHost checks prov_offer's host (parent 2.9, phase 2
// R4.3): with no --host it's the Mac's LocalHostName plus ".local", from the
// lookup; --host 127.0.0.1 overrides it, as the emulator harness provisions.
// The offer and the prov_set that follows are the fixtures', byte for byte.
// The real lookup runs exactly `scutil --get LocalHostName`, and falls back
// to the host name's first label.
func TestProvisionDefaultHost(t *testing.T) {
	for _, c := range []struct {
		name     string
		fixtures []string
		set      func(*Options)
	}{
		{"no --host", []string{"prov_offer.txt", "prov_set.txt"}, func(o *Options) { o.Host = "" }},
		{"--host 127.0.0.1", []string{"prov_offer-ip-host.txt", "prov_set-emu.txt"}, func(o *Options) {
			o.Host, o.Name, o.BridgePort = "127.0.0.1", "emu-01", 52817
			o.Networks = []Network{{SSID: "lcd349-emu", Pass: "emulator-pass"}}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			peer, tty := openPeer(t)
			sent := device{hello: "to-tool/valid/prov_hello.txt", result: "to-tool/valid/prov_result-ok.txt"}.play(t, peer)
			hostKey, path := bridge(t)
			o := options(t, tty, hostKey, path, &bytes.Buffer{}) // LocalHostName gives studio-mac
			c.set(&o)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := Run(ctx, o); err != nil {
				t.Fatalf("Run: %v", err)
			}
			for _, fixture := range c.fixtures {
				select {
				case line := <-sent:
					if want := readLine(t, "to-device/valid/"+fixture); !bytes.Equal(line, want) {
						t.Errorf("the tool sent\n%s\nwant the fixture %s\n%s", line, fixture, want)
					}
				case <-time.After(time.Second):
					t.Errorf("the tool sent no line like %s", fixture)
				}
			}
		})
	}

	t.Run("the lookup", func(t *testing.T) {
		var argv []string
		scutil := func(out string, err error) func(...string) ([]byte, error) {
			return func(a ...string) ([]byte, error) {
				argv = a
				return []byte(out), err
			}
		}
		hostname := func(name string, err error) func() (string, error) {
			return func() (string, error) { return name, err }
		}
		failed := errors.New("scutil: no such key")
		for _, c := range []struct {
			name     string
			run      func(...string) ([]byte, error)
			hostname func() (string, error)
			want     string
			ok       bool
		}{
			{"scutil answers", scutil("studio-mac\n", nil), hostname("other.lan", nil), "studio-mac", true},
			{"scutil fails", scutil("", failed), hostname("Studio-Mac.lan", nil), "Studio-Mac", true},
			{"scutil prints nothing", scutil("\n", nil), hostname("studio-mac", nil), "studio-mac", true},
			{"both fail", scutil("", failed), hostname("", errors.New("no host name")), "", false},
		} {
			argv = nil
			got, err := localHostName(c.run, c.hostname)
			if got != c.want || (err == nil) != c.ok {
				t.Errorf("%s: %q, %v; want %q, ok %v", c.name, got, err, c.want, c.ok)
			}
			if want := []string{"/usr/sbin/scutil", "--get", "LocalHostName"}; !slices.Equal(argv, want) {
				t.Errorf("%s: ran %q; want exactly %q", c.name, argv, want)
			}
		}
	})
}
