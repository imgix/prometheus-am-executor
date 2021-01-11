package main

import (
	"math/rand"
	"os"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

// containsString returns true if the string is in the collection
func containsString(want string, coll []string) bool {
	for _, v := range coll {
		if v == want {
			return true
		}
	}

	return false
}

func TestResult_Has(t *testing.T) {
	t.Parallel()
	rand.Seed(time.Now().Unix())

	// Randomly pick between 1 and all flags to include in state being checked
	available := make([]Result, 0)
	for r, _ := range ResultStrings {
		available = append(available, r)
	}

	var chosen = make([]Result, 0)
	var missing = make([]Result, 0)
	end := rand.Intn(len(available)) + 1
	rand.Shuffle(len(available), func(i, j int) {
		available[i], available[j] = available[j], available[i]
	})
	for i, r := range available {
		if i < end {
			chosen = append(chosen, r)
		} else {
			missing = append(missing, r)
		}
	}

	var state Result
	for _, r := range chosen {
		state = state | r
	}

	// Check that the flags we've chosen exist in the state
	for _, r := range chosen {
		if !state.Has(r) {
			t.Errorf("missing flag %s in state %s", ResultStrings[r], state)
		}
	}

	// Check that flags we haven't chose aren't
	for _, r := range missing {
		if state.Has(r) {
			t.Errorf("flag %s shouldn't exist in state %s", ResultStrings[r], state)
		}
	}
}

func TestResult_String(t *testing.T) {
	t.Parallel()
	rand.Seed(time.Now().Unix())

	// Randomly pick between 1 and all flags to include in state being checked
	available := make([]Result, 0)
	for r, _ := range ResultStrings {
		available = append(available, r)
	}

	end := rand.Intn(len(available)) + 1
	rand.Shuffle(len(available), func(i, j int) {
		available[i], available[j] = available[j], available[i]
	})
	var chosen = make([]Result, 0)
	for _, r := range available[0:end] {
		chosen = append(chosen, r)
	}

	var state Result
	for _, r := range chosen {
		state = state | r
	}

	// We expect that the string representation will be sorted by result value
	sort.Slice(chosen, func(i, j int) bool { return chosen[i] < chosen[j] })
	names := make([]string, len(chosen))
	for i, r := range chosen {
		names[i] = ResultStrings[r]
	}
	want := strings.Join(names, "|")
	if state.String() != want {
		t.Errorf("wrong string representation of state; got %s, want %s", state, want)
	}
}

func TestCommand_Equal(t *testing.T) {
	cases := []struct {
		name string
		a    *Command
		b    *Command
		want bool
	}{
		{
			name: "same",
			a: &Command{
				Cmd:         "echo",
				Args:        []string{"banana", "lemon"},
				MatchLabels: map[string]string{"env": "test", "owner": "me"},
			},
			b: &Command{
				Cmd:         "echo",
				Args:        []string{"banana", "lemon"},
				MatchLabels: map[string]string{"env": "test", "owner": "me"},
			},
			want: true,
		},
		{
			name: "different_cmd",
			a: &Command{
				Cmd:         "echo",
				Args:        []string{"banana", "lemon"},
				MatchLabels: map[string]string{"env": "test", "owner": "me"},
			},
			b: &Command{
				Cmd:         "/bin/echo",
				Args:        []string{"banana", "lemon"},
				MatchLabels: map[string]string{"env": "test", "owner": "me"},
			},
			want: false,
		},
		{
			name: "different_arg_len",
			a: &Command{
				Cmd:         "echo",
				Args:        []string{},
				MatchLabels: map[string]string{"env": "test", "owner": "me"},
			},
			b: &Command{
				Cmd:         "echo",
				Args:        []string{"banana", "lemon"},
				MatchLabels: map[string]string{"env": "test", "owner": "me"},
			},
			want: false,
		},
		{
			name: "different_args",
			a: &Command{
				Cmd:         "echo",
				Args:        []string{"banana", "pineapple"},
				MatchLabels: map[string]string{"env": "test", "owner": "me"},
			},
			b: &Command{
				Cmd:         "echo",
				Args:        []string{"banana", "lemon"},
				MatchLabels: map[string]string{"env": "test", "owner": "me"},
			},
			want: false,
		},
		{
			name: "different_labels_len",
			a: &Command{
				Cmd:         "echo",
				Args:        []string{"banana", "lemon"},
				MatchLabels: map[string]string{"env": "test"},
			},
			b: &Command{
				Cmd:         "echo",
				Args:        []string{"banana", "lemon"},
				MatchLabels: map[string]string{"env": "test", "owner": "me"},
			},
			want: false,
		},
		{
			name: "different_labels",
			a: &Command{
				Cmd:         "echo",
				Args:        []string{"banana", "lemon"},
				MatchLabels: map[string]string{"env": "test", "owner": "me"},
			},
			b: &Command{
				Cmd:         "echo",
				Args:        []string{"banana", "lemon"},
				MatchLabels: map[string]string{"owner": "me"},
			},
			want: false,
		},
	}

	for _, tc := range cases {
		tc := tc // Capture range variable, for use in anonymous function
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var condition_word string
			if tc.want {
				condition_word = "should have been equal"
			} else {
				condition_word = "should not have been equal"
			}

			if tc.a.Equal(tc.b) != tc.want {
				t.Errorf("Commands %s", condition_word)
			}
		})
	}
}

func TestCommand_Fingerprint(t *testing.T) {
	cases := []struct {
		name        string
		cmd         *Command
		fingerprint string
		ok          bool
	}{
		// A matching command should have the same fingerprint
		{
			name: "match",
			cmd: &Command{
				Cmd: "echo",
				MatchLabels: map[string]string{
					"job":      "broken",
					"instance": "localhost:5678",
				}},
			fingerprint: "boop",
			ok:          true,
		},
		// The fingerprint of the _first_ matching alarm would be used though,
		// so if the first alert has no fingerprint but the second matching one does, the fingerprint is expected
		// to be empty.
		{
			name: "first_match",
			cmd: &Command{
				Cmd: "echo",
				MatchLabels: map[string]string{
					"job": "broken",
				}},
			fingerprint: "",
			ok:          true,
		},
		// A command without any MatchLabels should return the fingerprint of the first alert.
		{
			name:        "any",
			cmd:         &Command{Cmd: "echo"},
			fingerprint: "",
			ok:          true,
		},
		// A non-matching alarm should have an empty fingerprint and a false condition
		{
			name: "no_match",
			cmd: &Command{
				Cmd: "echo",
				MatchLabels: map[string]string{
					"job": "banana",
				}},
			fingerprint: "",
			ok:          false,
		},
	}

	for _, tc := range cases {
		tc := tc // Capture range variable, for use in anonymous function
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, ok := tc.cmd.Fingerprint(&amData)
			if f != tc.fingerprint {
				t.Errorf("wrong fingerprint; got '%s', want '%s'", f, tc.fingerprint)
			}
			if ok != tc.ok {
				t.Errorf("wrong found boolean; got %v, want %v", ok, tc.ok)
			}
		})
	}
}

func TestCommand_Matches(t *testing.T) {
	t.Parallel()
	noMatching := make(map[string]string)
	noMatching["banana"] = "ok"

	allMatching := make(map[string]string)
	// Randomly pick between 1 and all labels to include in allMatching
	available := amData.CommonLabels.Names()
	rand.Shuffle(len(available), func(i, j int) {
		available[i], available[j] = available[j], available[i]
	})
	for _, l := range available[0 : rand.Intn(len(available))+1] {
		allMatching[l] = amData.CommonLabels[l]
	}

	someMatching := make(map[string]string)
	for k, v := range noMatching {
		someMatching[k] = v
	}
	for k, v := range allMatching {
		someMatching[k] = v
	}

	cases := []struct {
		cmd  *Command
		want bool
	}{
		// No labels defined should have command match all alerts
		{
			cmd:  &Command{Cmd: "echo"},
			want: true,
		},
		// Labels that don't match means command should not match the alert
		{
			cmd:  &Command{Cmd: "echo", MatchLabels: noMatching},
			want: false,
		},
		// When all labels match, the command should match the alert
		{
			cmd:  &Command{Cmd: "echo", MatchLabels: allMatching},
			want: true,
		},
		// All labels need to match, for the command to match the alert
		{
			cmd:  &Command{Cmd: "echo", MatchLabels: someMatching},
			want: false,
		},
	}

	for i, tc := range cases {
		var condition_word string
		if tc.want {
			condition_word = "should have"
		} else {
			condition_word = "should not have"
		}
		if tc.cmd.Matches(&amData) != tc.want {
			t.Errorf("Case %d command %s matched alert; command labels %#v, alert labels %#v",
				i, condition_word, tc.cmd.MatchLabels, amData.CommonLabels)
		}
	}
}

func TestCommand_ParseSignal(t *testing.T) {
	cases := []struct {
		name    string
		cmd     Command
		sig     os.Signal
		wantErr bool
	}{
		{
			name:    "default",
			cmd:     Command{},
			sig:     os.Kill,
			wantErr: false,
		},
		{
			name:    "signum",
			cmd:     Command{ResolvedSig: "10999"},
			sig:     os.Signal(syscall.Signal(10999)),
			wantErr: false,
		},
		{
			name:    "invalid_signame",
			cmd:     Command{ResolvedSig: "banana"},
			sig:     os.Signal(syscall.Signal(-1)),
			wantErr: true,
		},
		{
			name:    "signame_lower",
			cmd:     Command{ResolvedSig: "sigusr2"},
			sig:     syscall.SIGUSR2,
			wantErr: false,
		},
		{
			name:    "signame_upper",
			cmd:     Command{ResolvedSig: "SIGSTOP"},
			sig:     syscall.SIGSTOP,
			wantErr: false,
		},
	}

	for _, tc := range cases {
		tc := tc // Capture range variable, for use in anonymous function
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sig, err := tc.cmd.ParseSignal()
			if (err != nil) != tc.wantErr {
				if tc.wantErr {
					t.Errorf("Missing error when one was expected; got %v", err)
				} else {
					t.Errorf("Got unexpected error: %v", err)
				}
			}
			if sig != tc.sig {
				t.Errorf("Wrong signal value; got %s, want %s", sig, tc.sig)
			}
		})
	}
}

func TestCommand_Run(t *testing.T) {
	t.Skip("TODO")
}

func TestCommand_ShouldIgnoreResolved(t *testing.T) {
	// We can create pointers to variables, but not to primitive values like true/false directly.
	var alsoTrue = true
	var alsoFalse = false

	cases := []struct {
		name string
		cmd  *Command
		ok   bool
	}{
		// We shouldn't ignore a 'resolved' notification if the IgnoreResolved field of a Command isn't set.
		{
			name: "default_value",
			cmd:  &Command{Cmd: "echo"},
			ok:   false,
		},
		// We should ignore a 'resolved' notification if IgnoreResolved is true.
		{
			name: "ignore",
			cmd:  &Command{Cmd: "echo", IgnoreResolved: &alsoTrue},
			ok:   true,
		},
		// We shouldn't ignore a 'resolved' notification if IgnoreResolved is false.
		{
			name: "dont_ignore",
			cmd:  &Command{Cmd: "echo", IgnoreResolved: &alsoFalse},
			ok:   false,
		},
	}

	for _, tc := range cases {
		tc := tc // Capture range variable, for use in anonymous function
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ok := tc.cmd.ShouldIgnoreResolved()
			if ok != tc.ok {
				t.Errorf("wrong ShouldIgnoreResolved boolean; got %v, want %v", ok, tc.ok)
			}
		})
	}
}

func TestCommand_ShouldNotify(t *testing.T) {
	// We can create pointers to variables, but not to primitive values like true/false directly.
	var alsoTrue = true
	var alsoFalse = false

	cases := []struct {
		name string
		cmd  *Command
		ok   bool
	}{
		// We should let the caller know if a command failed, if the NotifyOnFailure field of a Command isn't set.
		{
			name: "default_value",
			cmd:  &Command{Cmd: "echo"},
			ok:   true,
		},
		// We should let the caller know if a command failed, if NotifyOnFailure is true.
		{
			name: "notify",
			cmd:  &Command{Cmd: "echo", NotifyOnFailure: &alsoTrue},
			ok:   true,
		},
		// We shouldn't let the caller know if a command failed, if NotifyOnFailure is false.
		{
			name: "dont_notify",
			cmd:  &Command{Cmd: "echo", NotifyOnFailure: &alsoFalse},
			ok:   false,
		},
	}

	for _, tc := range cases {
		tc := tc // Capture range variable, for use in anonymous function
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ok := tc.cmd.ShouldNotify()
			if ok != tc.ok {
				t.Errorf("wrong NotifyOnFailure boolean; got %v, want %v", ok, tc.ok)
			}
		})
	}
}

func TestCommand_String(t *testing.T) {
	t.Parallel()
	cmdNoArgs := Command{Cmd: "echo"}
	if cmdNoArgs.String() != "echo" {
		t.Errorf("wrong command string; got '%s', want '%s'", cmdNoArgs, "echo")
	}

	cmdWithArgs := Command{Cmd: "echo", Args: []string{"a", "b", "c"}}
	if cmdWithArgs.String() != "echo a b c" {
		t.Errorf("wrong command string; got '%s', want '%s'", cmdWithArgs, "echo a b c")
	}
}

func TestCommand_WithEnv(t *testing.T) {
	t.Parallel()
	env := []string{"BANANAS=3", "PRIORITY=TOP"}
	cmd := Command{Cmd: "echo"}.WithEnv(env...)

	for _, v := range os.Environ() {
		if !containsString(v, cmd.Env) {
			t.Errorf("missing os env var %s", v)
		}
	}

	for _, v := range env {
		if !containsString(v, cmd.Env) {
			t.Errorf("missing extra env var %s", v)
		}
	}
}

func TestIsDigit(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{
			name: "digits",
			in:   "000103",
			want: true,
		},
		{
			name: "decimal",
			in:   "3.14",
			want: false,
		},
		{
			name: "negative",
			in:   "-1",
			want: false,
		},
		{
			name: "positive",
			in:   "+9",
			want: false,
		},
		{
			name: "alpha",
			in:   "123a5",
			want: false,
		},
		{
			name: "empty",
			in:   "",
			want: false,
		},
		{
			name: "space",
			in:   "123 45",
			want: false,
		},
		{
			name: "separator",
			in:   "11,000",
			want: false,
		},
	}

	for _, tc := range cases {
		tc := tc // Capture range variable, for use in anonymous function scope
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := IsDigit(tc.in)
			if got != tc.want {
				t.Errorf("Wrong result; got %v, want %v", got, tc.want)
			}
		})
	}
}
