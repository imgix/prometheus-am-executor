package main

import (
	"fmt"
	"github.com/prometheus/alertmanager/template"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
)

const (
	// Enum mask for kinds of results
	CmdOk       Result = 1 << iota
	CmdFail     Result = 1 << iota
	CmdKillOk   Result = 1 << iota
	CmdKillFail Result = 1 << iota
	CmdSkipKill Result = 1 << iota
)

var (
	ResultStrings = map[Result]string{
		CmdOk:       "Ok",
		CmdFail:     "Fail",
		CmdKillOk:   "KillOk",
		CmdKillFail: "KillFail",
		CmdSkipKill: "SkipKill",
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
	IgnoreResolved *bool `yaml:"ignore_resolved,omitempty"`
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

// Run executes the command, killing it if asked to quit early
// out channel is used to indicate the result of running or killing the program. May indicate errors.
// quit channel is used to determine if execution should quit early
// done channel is used to indicate to caller when execution has completed
func (c Command) Run(out chan<- CommandResult, quit chan struct{}, done chan struct{}, env ...string) {
	defer close(out)
	defer close(done)
	var wg sync.WaitGroup
	cmd := c.WithEnv(env...)
	cmdOut := make(chan CommandResult)
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
			out <- CommandResult{Kind: CmdSkipKill, Err: nil}
		} else {
			err := cmd.Process.Kill()
			if err == nil {
				out <- CommandResult{Kind: CmdKillOk, Err: nil}
			} else {
				errMsg := fmt.Errorf("Failed to kill pid %d for command %s: %w", cmd.Process.Pid, c, err)
				out <- CommandResult{Kind: CmdKillFail, Err: errMsg}
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
