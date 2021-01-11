package main

import (
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"testing"
)

func Test_mergeConfigs(t *testing.T) {
	t.Parallel()

	a := &Config{
		ListenAddr: "localhost:8080",
		Verbose:    false,
		Commands: []*Command{
			{Cmd: "echo"},
		},
	}

	b := &Config{
		ListenAddr: "localhost:8081",
		Verbose:    true,
		Commands: []*Command{
			{Cmd: "/bin/echo"},
		},
	}

	merged := mergeConfigs(a, b)
	if merged.ListenAddr != b.ListenAddr {
		t.Errorf("Wrong ListenAddr for merged config; got %s, want %s", merged.ListenAddr, b.ListenAddr)
	}
	if merged.Verbose != b.Verbose {
		t.Errorf("Wrong Verbose for merged config; got %v, want %v", merged.Verbose, b.Verbose)
	}

	allCmds := make([]*Command, 0)
	allCmds = append(allCmds, a.Commands...)
	allCmds = append(allCmds, b.Commands...)
	for _, cmd := range allCmds {
		if !merged.HasCommand(cmd) {
			t.Errorf("Missing command %#v", cmd)
		}
	}

	yamlFile := `---
listen_address: ":23222"
verbose: false
commands:
  - cmd: echo
    args: ["banana", "tomato"]
    match_labels:
      "env": "testing"
      "owner": "me"
  - cmd: /bin/true
    match_labels:
      "beep": "boop"
`
	yamlConf := &Config{}
	err := yaml.Unmarshal([]byte(yamlFile), yamlConf)
	if err != nil {
		t.Errorf("Failed to unmarshal yaml config file; %v", err)
	}

	mergedAgain := mergeConfigs(merged, yamlConf)

	if mergedAgain.ListenAddr != yamlConf.ListenAddr {
		t.Errorf("Wrong ListenAddr for merged config; got %s, want %s", mergedAgain.ListenAddr, yamlConf.ListenAddr)
	}
	if mergedAgain.Verbose != (merged.Verbose || yamlConf.Verbose) {
		t.Errorf("Wrong Verbose for merged config; got %v, want %v", mergedAgain.Verbose, (merged.Verbose || yamlConf.Verbose))
	}

	allCmds = append(allCmds, yamlConf.Commands...)
	for _, cmd := range allCmds {
		if !mergedAgain.HasCommand(cmd) {
			t.Errorf("Missing command %#v", cmd)
		}
	}
}

func Test_readConfigFile(t *testing.T) {
	tempfile, err := ioutil.TempFile("", "am-executor_readConfigFile-*.yml")
	if err != nil {
		t.Error(err)
	}
	defer func() {
		_ = os.Remove(tempfile.Name())
	}()

	yamlFile := `---
listen_address: ":23222"
verbose: true
commands:
  - cmd: echo
    args: ["banana", "tomato"]
    match_labels:
      "env": "testing"
      "owner": "me"
    notify_on_failure: false
    resolved_signal: sigusr2
  - cmd: /bin/true
    match_labels:
      "beep": "boop"
    ignore_resolved: true
`

	_, err = tempfile.Write([]byte(yamlFile))
	if err != nil {
		t.Fatal(err)
	}
	err = tempfile.Close()
	if err != nil {
		t.Fatal(err)
	}

	c, err := readConfigFile(tempfile.Name())
	if err != nil {
		t.Fatalf("Failed to read configuration file from %s: %v", tempfile.Name(), err)
	}

	if c.ListenAddr != ":23222" {
		t.Errorf("Wrong ListenAddr; got %s, want %s", c.ListenAddr, ":23222")
	}

	if c.Verbose != true {
		t.Errorf("Wrong Verbose value; got %v, want %v", c.Verbose, true)
	}

	if len(c.Commands) != 2 {
		t.Errorf("Wrong number of commands defined; got %d, want %d", len(c.Commands), 2)
	}

	// We can create pointers to variables, but not to primitive values like true/false directly.
	var alsoTrue = true
	var alsoFalse = false
	var cases = []struct {
		cmd                  *Command
		shouldNotify         bool
		shouldIgnoreResolved bool
	}{
		{
			cmd: &Command{
				Cmd:  "echo",
				Args: []string{"banana", "tomato"},
				MatchLabels: map[string]string{
					"env":   "testing",
					"owner": "me",
				},
				NotifyOnFailure: &alsoFalse,
				ResolvedSig:     "sigusr2",
			},
			shouldNotify:         false,
			shouldIgnoreResolved: false,
		},
		{
			cmd: &Command{
				Cmd: "/bin/true",
				MatchLabels: map[string]string{
					"beep": "boop",
				},
				IgnoreResolved: &alsoTrue,
			},
			shouldNotify:         true,
			shouldIgnoreResolved: true,
		},
	}

	for i, tc := range cases {
		if !c.Commands[i].Equal(tc.cmd) {
			t.Errorf("Commands not equal; %q and %q", c.Commands[i], tc.cmd.String())
		}
		if c.Commands[i].ShouldNotify() != tc.shouldNotify {
			t.Errorf("Wrong NotifyOnFailure value for %q; got %v, want %v", c.Commands[i].String(), c.Commands[i].ShouldNotify(), tc.shouldNotify)
		}
		if c.Commands[i].ShouldIgnoreResolved() != tc.shouldIgnoreResolved {
			t.Errorf("Wrong IgnoreResolved value for %q; got %v, want %v", c.Commands[i].String(), c.Commands[i].ShouldIgnoreResolved(), tc.shouldIgnoreResolved)
		}
		if c.Commands[i].ResolvedSig != tc.cmd.ResolvedSig {
			t.Errorf("Wrong ResolvedSig value for %q; got %s, want %s", c.Commands[i].String(), c.Commands[i].ResolvedSig, tc.cmd.ResolvedSig)
		}
		_, err := c.Commands[i].ParseSignal()
		if err != nil {
			t.Fatalf("Failed to convert command %q ResolvedSig value %s to signal: %v", c.Commands[i].String(), c.Commands[i].ResolvedSig, err)
		}
	}
}

func TestConfig_HasCommand(t *testing.T) {
	t.Parallel()
	a := &Command{
		Cmd:         "echo",
		Args:        []string{"banana", "lemon"},
		MatchLabels: map[string]string{"env": "test", "owner": "me"},
	}

	c := Config{}
	if c.HasCommand(a) {
		t.Errorf("Config should not have command")
	}
	c.Commands = append(c.Commands, a)
	if !c.HasCommand(a) {
		t.Errorf("Config should have command")
	}
}
