package main

import (
	"fmt"
	"github.com/prometheus/alertmanager/template"
	"log"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unicode"
)

const (
	// Enum mask for kinds of results
	CmdOk      Result = 1 << iota
	CmdFail    Result = 1 << iota
	CmdSigOk   Result = 1 << iota
	CmdSigFail Result = 1 << iota
	CmdSkipSig Result = 1 << iota
)

var (
	ResultStrings = map[Result]string{
		CmdOk:      "Ok",
		CmdFail:    "Fail",
		CmdSigOk:   "SigOk",
		CmdSigFail: "SigFail",
		CmdSkipSig: "SkipSig",
	}

	signals = map[string]syscall.Signal{
		"SIGABRT":   syscall.SIGABRT,
		"SIGALRM":   syscall.SIGALRM,
		"SIGBUS":    syscall.SIGBUS,
		"SIGCHLD":   syscall.SIGCHLD,
		"SIGCONT":   syscall.SIGCONT,
		"SIGFPE":    syscall.SIGFPE,
		"SIGHUP":    syscall.SIGHUP,
		"SIGILL":    syscall.SIGILL,
		"SIGINT":    syscall.SIGINT,
		"SIGIO":     syscall.SIGIO,
		"SIGIOT":    syscall.SIGIOT,
		"SIGKILL":   syscall.SIGKILL,
		"SIGPIPE":   syscall.SIGPIPE,
		"SIGPROF":   syscall.SIGPROF,
		"SIGQUIT":   syscall.SIGQUIT,
		"SIGSEGV":   syscall.SIGSEGV,
		"SIGSTOP":   syscall.SIGSTOP,
		"SIGSYS":    syscall.SIGSYS,
		"SIGTERM":   syscall.SIGTERM,
		"SIGTRAP":   syscall.SIGTRAP,
		"SIGTSTP":   syscall.SIGTSTP,
		"SIGTTIN":   syscall.SIGTTIN,
		"SIGTTOU":   syscall.SIGTTOU,
		"SIGURG":    syscall.SIGURG,
		"SIGUSR1":   syscall.SIGUSR1,
		"SIGUSR2":   syscall.SIGUSR2,
		"SIGVTALRM": syscall.SIGVTALRM,
		"SIGWINCH":  syscall.SIGWINCH,
		"SIGXCPU":   syscall.SIGXCPU,
		"SIGXFSZ":   syscall.SIGXFSZ,
	}
)

type Result int

type CommandResult struct {
	Kind Result
	Err  error
}

// Command represents a command that could be run based on what labels match
type Command struct {
	Cmd  string   `yaml:"cmd"`
	Args []string `yaml:"args"`
	// Only execute this command when all of the given labels match.
	// The CommonLabels field of prometheus alert data is used for comparison.
	MatchLabels map[string]string `yaml:"match_labels"`
	// How many instances of this command can run at the same time.
	// A zero or negative value is interpreted as 'no limit'.
	Max int `yaml:"max"`
	// Whether we should let the caller know if a command failed.
	// Defaults to true.
	// The value is a pointer to bool with the 'omitempty' tag,
	// so we can tell when the value was not defined,
	// meaning we'll provide the default value.
	NotifyOnFailure *bool `yaml:"notify_on_failure,omitempty"`
	// Whether command will ignore a 'resolved' notification for a matching command,
	// and continue running to completion.
	// Defaults to false.
	IgnoreResolved *bool  `yaml:"ignore_resolved,omitempty"`
	ResolvedSig    string `yaml:"resolved_signal"`
}

// Return a string representing the result state
func (r Result) String() string {
	var has = make([]string, 0)

	// To keep the string's content consistent, we'll sort the flags by the enum value, lowest to highest.
	var index = make(map[string]Result)
	for f, n := range ResultStrings {
		index[n] = f
	}

	less := func(i, j int) bool {
		iKey := has[i]
		jKey := has[j]
		if index[iKey] < index[jKey] {
			return true
		} else {
			return false
		}
	}

	for f, n := range ResultStrings {
		if r.Has(f) {
			has = append(has, n)
		}
	}

	sort.Slice(has, less)
	return strings.Join(has, "|")
}

// Has returns true if the result has the given flag set
func (r Result) Has(flag Result) bool {
	return r&flag != 0
}

// Equal returns true if the Command is identical to another Command
func (c Command) Equal(other *Command) bool {
	if c.Cmd != other.Cmd {
		return false
	}

	if len(c.Args) != len(other.Args) {
		return false
	}

	if len(c.MatchLabels) != len(other.MatchLabels) {
		return false
	}

	for i, arg := range c.Args {
		if arg != other.Args[i] {
			return false
		}
	}

	for k, v := range c.MatchLabels {
		otherValue, ok := other.MatchLabels[k]
		if !ok {
			return false
		}

		if v != otherValue {
			return false
		}
	}

	return true
}

// Fingerprint returns the fingerprint of the first alarm that matches the command's labels.
// The first fingerprint found is returned if we have no MatchLabels defined.
func (c Command) Fingerprint(msg *template.Data) (string, bool) {
	for _, alert := range msg.Alerts {
		matched := 0
		for k, v := range c.MatchLabels {
			other, ok := alert.Labels[k]
			if ok && v == other {
				matched += 1
			}
		}
		if matched == len(c.MatchLabels) {
			return alert.Fingerprint, true
		}
	}

	return "", false
}

// Matches returns true if all of its labels match against the given prometheus alert message.
// If we have no MatchLabels defined, we also return true.
func (c Command) Matches(msg *template.Data) bool {
	if len(c.MatchLabels) == 0 {
		return true
	}

	for k, v := range c.MatchLabels {
		other, ok := msg.CommonLabels[k]
		if !ok || v != other {
			return false
		}
	}

	return true
}

// Run executes the command, potentially signalling it if alarm that triggered command resolves.
// out channel is used to indicate the result of running or killing the program. May indicate errors.
// quit channel is used to determine if execution should quit early
// done channel is used to indicate to caller when execution has completed
func (c Command) Run(out chan<- CommandResult, quit chan struct{}, done chan struct{}, env ...string) {
	defer close(out)
	defer close(done)
	var wg sync.WaitGroup
	cmd := c.WithEnv(env...)
	// We use a buffer of one, so that if the command is killed before it finishes,
	// we will still be able to close the channel and end the Command.Run method;
	// There won't be a channel reader left, because the select statement ended when quit was read from.
	cmdOut := make(chan CommandResult, 1)
	wg.Add(1)
	go func() {
		defer close(cmdOut)
		defer wg.Done()
		err := cmd.Run()
		if err == nil {
			cmdOut <- CommandResult{Kind: CmdOk, Err: nil}
		} else {
			cmdOut <- CommandResult{Kind: CmdFail, Err: err}
		}
	}()

	select {
	case r := <-cmdOut:
		out <- r
	case <-quit:
		if c.ShouldIgnoreResolved() {
			out <- CommandResult{Kind: CmdSkipSig, Err: nil}
		} else {
			sig, err := c.ParseSignal()
			if err != nil {
				errMsg := fmt.Errorf("Can't use signal %s to notify pid %d for command %s: %w", c.ResolvedSig, cmd.Process.Pid, c, err)
				out <- CommandResult{Kind: CmdSigFail, Err: errMsg}
			}
			err = cmd.Process.Signal(sig)
			if err == nil {
				out <- CommandResult{Kind: CmdSigOk, Err: nil}
			} else {
				errMsg := fmt.Errorf("Failed sending %s to pid %d for command %s: %w", sig, cmd.Process.Pid, c, err)
				out <- CommandResult{Kind: CmdSigFail, Err: errMsg}
			}
		}
	}
	wg.Wait()
}

// ShouldIgnoreResolved returns the interpreted value of c.IgnoreResolved.
// This method is used to work around ambiguity of unmarshalling yaml boolean values,
// due to the default value of a bool being false.
func (c Command) ShouldIgnoreResolved() bool {
	if c.IgnoreResolved == nil {
		// Default to false when value is not defined
		return false
	}
	return *c.IgnoreResolved
}

// ShouldNotify returns the interpreted value of c.NotifyOnFailure.
// This method is used to work around ambiguity of unmarshalling yaml boolean values,
// due to the default value of a bool being false.
func (c Command) ShouldNotify() bool {
	if c.NotifyOnFailure == nil {
		// Default to true when value is not defined
		return true
	}
	return *c.NotifyOnFailure
}

// ParseSignal returns the signal that is meant to be used for notifying the command that its triggering condition has resolved,
// and any error encountered while parsing.
func (c Command) ParseSignal() (os.Signal, error) {
	if len(c.ResolvedSig) == 0 {
		return os.Kill, nil
	}

	var notFound = os.Signal(syscall.Signal(-1))
	if IsDigit(c.ResolvedSig) {
		n, err := strconv.Atoi(c.ResolvedSig)
		if err != nil {
			return notFound, err
		}
		return os.Signal(syscall.Signal(n)), nil
	}

	want := strings.ToUpper(c.ResolvedSig)
	sig, ok := signals[strings.ToUpper(c.ResolvedSig)]
	if !ok {
		return notFound, fmt.Errorf("Unknown signal %s", want)
	}

	return sig, nil
}

// String returns a string representation of the command
func (c Command) String() string {
	if len(c.Args) == 0 {
		return c.Cmd
	}
	return fmt.Sprintf("%s %s", c.Cmd, strings.Join(c.Args, " "))
}

// WithEnv returns a runnable command with the given environment variables added.
// Command STDOUT and STDERR is attached to the logger.
func (c Command) WithEnv(env ...string) *exec.Cmd {
	lw := log.Writer()
	cmd := exec.Command(c.Cmd, c.Args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = lw
	cmd.Stderr = lw

	return cmd
}

// IsDigit returns true if all of the string consists of digits
func IsDigit(s string) bool {
	if len(s) == 0 {
		return false
	}
	val := []rune(s)
	var count = 0
	for _, r := range val {
		if unicode.IsDigit(r) {
			count++
		}
	}
	return count == len(val)
}
