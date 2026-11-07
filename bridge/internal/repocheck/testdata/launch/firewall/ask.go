// The doctor's one launch with an argument that isn't a literal (the owner's
// checkpoint 7.2, 2026-11-07): socketfilterfw --getappblocked and the direct
// result of ownPath, the doctor's own function, which returns the bridge's
// own resolved path. The check allows the unmarked launches in
// internal/doctor, and refuses the marked ones there; in any other package it
// refuses every launch here that uses ownPath.
package doctor

import (
	"context"
	"os/exec"
)

type Env struct{ Executable string }

type runner func(argv ...string) ([]byte, error)

func ownPath(env Env) string { return env.Executable }

// Run is the doctor's form: a runner whose calls pass constants, and
// ownPath's result last after socketfilterfw --getappblocked.
func Run(env Env) {
	run(env, func(argv ...string) ([]byte, error) {
		return exec.CommandContext(context.Background(), argv[0], argv[1:]...).Output()
	})
}

func run(env Env, cmd runner) {
	cmd("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getglobalstate")
	cmd("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getappblocked", ownPath(env))
}

func direct(env Env) error {
	return exec.Command("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getappblocked", ownPath(env)).Run()
}

func literal() error {
	return exec.Command("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getappblocked", "\x00ownPath(…)").Run() // refused: the placeholder as a literal
}

func field(env Env) error {
	return exec.Command("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getappblocked", env.Executable).Run() // refused: not ownPath's result
}

func otherQuestion(env Env) error {
	return exec.Command("/usr/libexec/ApplicationFirewall/socketfilterfw", "--unblockapp", ownPath(env)).Run() // refused: another question
}

func otherProgram(env Env) error {
	return exec.Command("/bin/rm", ownPath(env)).Run() // refused: another program
}

func notLast(env Env) error {
	return exec.Command("/usr/libexec/ApplicationFirewall/socketfilterfw", ownPath(env), "--getappblocked").Run() // refused: not last
}

func value(env Env) error {
	p := ownPath
	return exec.Command("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getappblocked", p(env)).Run() // refused: a function value
}

func shadowed(ownPath func(Env) string, env Env) error {
	return exec.Command("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getappblocked", ownPath(env)).Run() // refused: a parameter named ownPath
}
