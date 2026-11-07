package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MaverickHQ/herdr-pager/tools/release/internal/fixture"
	"github.com/MaverickHQ/herdr-pager/tools/release/internal/release"
)

const (
	archive = "herdr-pager_9.8.7_darwin_arm64.tar.gz"
	libssh  = "libssh-0.12.2-source.tar.gz"
)

func runVerify(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// complete is a complete set of a release's files, made by the packer.
func complete(t *testing.T) string {
	t.Helper()
	in := fixture.New(t, "9.8.7")
	out := filepath.Join(t.TempDir(), "release")
	_, err := release.Pack(release.PackOptions{
		Version: "9.8.7", Bridge: in.Bridge, Firmware: in.Firmware, Libssh: in.Libssh, Repo: in.Repo, Out: out,
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// resum rewrites dir's SHA256SUMS over names, as a release whose files were
// built wrong but summed right would have it.
func resum(t *testing.T, dir string, names ...string) {
	t.Helper()
	slices.Sort(names)
	fixture.Write(t, filepath.Join(dir, "SHA256SUMS"), fixture.SHA256SUMS(t, dir, names...))
}

// rewrite rewrites the gzipped tar dir/name without the entries drop says,
// and sums the set again.
func rewrite(t *testing.T, dir, name string, drop func(string) bool) {
	t.Helper()
	var kept []fixture.Entry
	for _, e := range fixture.ReadTarGz(t, filepath.Join(dir, name)) {
		if !drop(e.Name) {
			kept = append(kept, e)
		}
	}
	fixture.WriteTarGz(t, filepath.Join(dir, name), kept)
	resum(t, dir, archive, libssh, "THIRD_PARTY.md")
}

// edit rewrites the gzipped tar dir/name with change applied to each of its
// entries, and sums the set again.
func edit(t *testing.T, dir, name string, change func(e *fixture.Entry)) {
	t.Helper()
	es := fixture.ReadTarGz(t, filepath.Join(dir, name))
	for i := range es {
		change(&es[i])
	}
	fixture.WriteTarGz(t, filepath.Join(dir, name), es)
	resum(t, dir, archive, libssh, "THIRD_PARTY.md")
}

func remove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyReleaseArtefacts(t *testing.T) {
	t.Run("a complete set passes", func(t *testing.T) {
		dir := complete(t)
		code, stdout, stderr := runVerify(t, dir)
		if code != 0 || stderr != "" {
			t.Fatalf("verify: exit %d, stderr %q; want 0", code, stderr)
		}
		if !strings.Contains(stdout, "herdr-pager 9.8.7") {
			t.Errorf("verify printed %q; want it to name the version it checked", stdout)
		}
	})

	cases := []struct {
		name   string
		change func(t *testing.T, dir string)
		say    []string // each in stderr
	}{
		{
			name:   "no archive",
			change: func(t *testing.T, dir string) { remove(t, filepath.Join(dir, archive)) },
			say:    []string{archive, "missing"},
		},
		{
			name:   "no THIRD_PARTY.md",
			change: func(t *testing.T, dir string) { remove(t, filepath.Join(dir, "THIRD_PARTY.md")) },
			say:    []string{"THIRD_PARTY.md", "missing"},
		},
		{
			name:   "no SHA256SUMS",
			change: func(t *testing.T, dir string) { remove(t, filepath.Join(dir, "SHA256SUMS")) },
			say:    []string{"SHA256SUMS"},
		},
		{
			name:   "no libssh source",
			change: func(t *testing.T, dir string) { remove(t, filepath.Join(dir, libssh)) },
			say:    []string{libssh, "missing"},
		},
		{
			name: "no libssh source, nor its line in SHA256SUMS",
			change: func(t *testing.T, dir string) {
				remove(t, filepath.Join(dir, libssh))
				resum(t, dir, archive, "THIRD_PARTY.md")
			},
			say: []string{"no libssh source"},
		},
		{
			name: "libssh's source without its licence",
			change: func(t *testing.T, dir string) {
				rewrite(t, dir, libssh, func(n string) bool { return strings.HasSuffix(n, "/libssh-mirror/COPYING") })
			},
			say: []string{libssh, "libssh-mirror/COPYING"},
		},
		{
			name: "a checksum that doesn't match",
			change: func(t *testing.T, dir string) {
				f, err := os.OpenFile(filepath.Join(dir, "THIRD_PARTY.md"), os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				f.WriteString("an edit after SHA256SUMS\n")
				f.Close()
			},
			say: []string{"THIRD_PARTY.md", "doesn't match SHA256SUMS"},
		},
		{
			name: "the archive's checksum doesn't match",
			change: func(t *testing.T, dir string) {
				sums := fixture.Read(t, filepath.Join(dir, "SHA256SUMS"))
				i := bytes.Index(sums, []byte("  "+archive))
				// One hex digit of the archive's sum, still a hex digit.
				if sums[i-1] == '0' {
					sums[i-1] = '1'
				} else {
					sums[i-1] = '0'
				}
				fixture.Write(t, filepath.Join(dir, "SHA256SUMS"), sums)
			},
			say: []string{archive, "doesn't match SHA256SUMS"},
		},
		{
			name: "an archive without its firmware",
			change: func(t *testing.T, dir string) {
				rewrite(t, dir, archive, func(n string) bool { return strings.Contains(n, "/share/") })
			},
			say: []string{archive, "firmware"},
		},
		{
			name: "an archive without its app",
			change: func(t *testing.T, dir string) {
				rewrite(t, dir, archive, func(n string) bool { return strings.HasSuffix(n, "/firmware/herdr-pager.bin") })
			},
			say: []string{archive, "herdr-pager.bin"},
		},
		{
			name: "an archive without the bridge",
			change: func(t *testing.T, dir string) {
				rewrite(t, dir, archive, func(n string) bool { return strings.HasSuffix(n, "/bin/pager-bridge") })
			},
			say: []string{archive, "bin/pager-bridge"},
		},
		{
			name: "an archive without its licences",
			change: func(t *testing.T, dir string) {
				rewrite(t, dir, archive, func(n string) bool { return strings.Contains(n, "/licenses/") })
			},
			say: []string{archive, "licenses"},
		},
		// Added by the lane's review: the rest of what verify checks in the
		// archive and libssh's source, each in a set that's summed right.
		{
			name: "an archive whose manifest isn't the pager's",
			change: func(t *testing.T, dir string) {
				edit(t, dir, archive, func(e *fixture.Entry) {
					if strings.HasSuffix(e.Name, "/firmware/manifest.json") {
						e.Data = bytes.Replace(e.Data, []byte(": 65536"), []byte(": 131072"), 1)
					}
				})
			},
			say: []string{archive, "manifest.json isn't the pager's"},
		},
		{
			name: "an archive whose app says another version",
			change: func(t *testing.T, dir string) {
				edit(t, dir, archive, func(e *fixture.Entry) {
					if strings.HasSuffix(e.Name, "/firmware/herdr-pager.bin") {
						e.Data = fixture.App(fixture.ChipESP32S3, "9.8.6", "herdr-pager")
					}
				})
			},
			say: []string{archive, `the app says version "9.8.6"`},
		},
		{
			name: "an archive whose bridge isn't executable",
			change: func(t *testing.T, dir string) {
				edit(t, dir, archive, func(e *fixture.Entry) {
					if strings.HasSuffix(e.Name, "/bin/pager-bridge") {
						e.Mode = 0o644
					}
				})
			},
			say: []string{archive, "bin/pager-bridge isn't executable"},
		},
		{
			name: "an archive whose THIRD_PARTY.md isn't the release's",
			change: func(t *testing.T, dir string) {
				edit(t, dir, archive, func(e *fixture.Entry) {
					if strings.HasSuffix(e.Name, "/THIRD_PARTY.md") {
						e.Data = append(bytes.Clone(e.Data), "an older list\n"...)
					}
				})
			},
			say: []string{archive, "THIRD_PARTY.md isn't the release's"},
		},
		{
			name: "an archive with an entry outside its folder",
			change: func(t *testing.T, dir string) {
				edit(t, dir, archive, func(e *fixture.Entry) {
					if strings.HasSuffix(e.Name, "/LICENSE") {
						e.Name = "elsewhere/LICENSE"
					}
				})
			},
			say: []string{archive, "elsewhere/LICENSE is outside its folder"},
		},
		{
			name: "libssh's source isn't the version its name says",
			change: func(t *testing.T, dir string) {
				edit(t, dir, libssh, func(e *fixture.Entry) {
					if e.Name == "libssh-0.12.2/idf_component.yml" || e.Name == "libssh-0.12.2/libssh-mirror/CMakeLists.txt" {
						e.Data = bytes.ReplaceAll(e.Data, []byte("0.12.2"), []byte("0.12.3"))
					}
				})
			},
			say: []string{libssh, "holds libssh 0.12.3, not 0.12.2"},
		},
		{
			name: "SHA256SUMS not in sha256sum's form",
			change: func(t *testing.T, dir string) {
				sums := fixture.Read(t, filepath.Join(dir, "SHA256SUMS"))
				fixture.Write(t, filepath.Join(dir, "SHA256SUMS"), append(bytes.Clone(sums), "not a checksum line\n"...))
			},
			say: []string{"SHA256SUMS line 4 isn't sha256sum's form"},
		},
		{
			name: "a file that isn't the release's",
			change: func(t *testing.T, dir string) {
				fixture.Write(t, filepath.Join(dir, "herdr-pager.rb"), []byte("class HerdrPager < Formula\nend\n"))
			},
			say: []string{"herdr-pager.rb", "isn't one of the release's files"},
		},
		{
			name: "two archives",
			change: func(t *testing.T, dir string) {
				fixture.Write(t, filepath.Join(dir, "herdr-pager_9.8.6_darwin_arm64.tar.gz"), fixture.Read(t, filepath.Join(dir, archive)))
				resum(t, dir, archive, "herdr-pager_9.8.6_darwin_arm64.tar.gz", libssh, "THIRD_PARTY.md")
			},
			say: []string{"herdr-pager_9.8.6_darwin_arm64.tar.gz"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := complete(t)
			c.change(t, dir)
			code, stdout, stderr := runVerify(t, dir)
			if code != 1 {
				t.Errorf("verify: exit %d, stdout %q, stderr %q; want 1", code, stdout, stderr)
			}
			for _, s := range c.say {
				if !strings.Contains(stderr, s) {
					t.Errorf("verify's stderr %q doesn't say %q", stderr, s)
				}
			}
		})
	}
}

func TestVerifyUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"a", "b"}} {
		if code, _, stderr := runVerify(t, args...); code != 2 || !strings.Contains(stderr, "usage: verify <dir>") {
			t.Errorf("verify %q: exit %d, stderr %q; want 2 and the usage", args, code, stderr)
		}
	}
}
