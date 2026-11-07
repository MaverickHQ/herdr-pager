package provision

import (
	"os/exec"
	"slices"
	"strings"
)

// A NearMiss is a --wifi-ssid the Mac hasn't saved, and the saved name it
// nearly is.
type NearMiss struct{ Given, Known string }

// NearMisses returns, in the order given, each of ssids that isn't one of
// known but is one after folding (fold): a name typed with a straight
// apostrophe for an iPhone's curly one never matches, and the pager joins
// only the exact name (the acceptance day, 2026-10-25).
func NearMisses(ssids, known []string) []NearMiss {
	var misses []NearMiss
	for _, s := range ssids {
		if slices.Contains(known, s) {
			continue
		}
		for _, k := range known {
			if fold(k) == fold(s) {
				misses = append(misses, NearMiss{s, k})
				break
			}
		}
	}
	return misses
}

// folds turns each apostrophe or single quote into ', each double quote into
// ", each dash into - and a no-break space into a space.
var folds = strings.NewReplacer(
	"’", "'", "‘", "'", "ʼ", "'", "′", "'", "`", "'", "´", "'",
	"“", `"`, "”", `"`, "″", `"`,
	"‐", "-", "‑", "-", "‒", "-", "–", "-", "—", "-", "−", "-",
	" ", " ",
)

// fold is s with its apostrophes, quotes and dashes folded, its spaces
// collapsed and trimmed, in lower case.
func fold(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(folds.Replace(s)), " "))
}

// KnownNetworks returns the Wi-Fi networks this Mac has saved, for
// provision's near-miss warning: `networksetup
// -listpreferredwirelessnetworks en0`, or en1 when en0 isn't the Wi-Fi
// interface, each a fixed argument vector (parent 13.3). None when neither
// answers.
func KnownNetworks() []string {
	return knownNetworks(func(argv ...string) ([]byte, error) {
		return exec.Command(argv[0], argv[1:]...).Output()
	})
}

// knownNetworks is KnownNetworks, given how to run a command.
func knownNetworks(run func(argv ...string) ([]byte, error)) []string {
	if names, ok := preferred(run("/usr/sbin/networksetup", "-listpreferredwirelessnetworks", "en0")); ok {
		return names
	}
	names, _ := preferred(run("/usr/sbin/networksetup", "-listpreferredwirelessnetworks", "en1"))
	return names
}

// preferred reads networksetup's list: "Preferred networks on <device>:",
// then a name a line, each after a tab. ok is false when the command failed
// or the device isn't Wi-Fi (no such header).
func preferred(out []byte, err error) (names []string, ok bool) {
	if err != nil {
		return nil, false
	}
	lines := strings.Split(string(out), "\n")
	if !strings.HasPrefix(lines[0], "Preferred networks on ") {
		return nil, false
	}
	for _, l := range lines[1:] {
		if name, ok := strings.CutPrefix(l, "\t"); ok && name != "" {
			names = append(names, name)
		}
	}
	return names, true
}
