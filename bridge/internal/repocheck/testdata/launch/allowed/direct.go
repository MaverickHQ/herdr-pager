package allowed

import (
	"context"
	osexec "os/exec"
)

// The vector written out at the launch, under another import name.
func localHostNameDirect(ctx context.Context) ([]byte, error) {
	return osexec.CommandContext(ctx, "scutil", "--get", "LocalHostName").Output()
}

// The vector from a function's parameter, which every call fills with
// constants.
func scutil(args ...string) ([]byte, error) {
	return osexec.Command("/usr/sbin/scutil", args...).Output()
}

func localHostNameByName() ([]byte, error) {
	return scutil("--get", "LocalHostName")
}
