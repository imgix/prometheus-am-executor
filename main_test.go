package main

import (
	"bytes"
	"encoding/json"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/juju/testing/checkers"
	"github.com/prometheus/alertmanager/template"
	"github.com/prometheus/client_golang/prometheus"
	pm "github.com/prometheus/client_model/go"
)

var (
	// A sample alert from prometheus alert manager
	alertManagerData = template.Data{
		Receiver: "default", Status: "firing", Alerts: template.Alerts{
			template.Alert{Status: "firing", Labels: template.KV{
				"job":       "broken",
				"monitor":   "codelab-monitor",
				"alertname": "InstanceDown",
				"instance":  "localhost:1234",
			},
				Annotations:  template.KV{},
				StartsAt:     time.Unix(1460045332, 0),
				EndsAt:       time.Time{},
				GeneratorURL: "http://oldpad:9090/graph#%5B%7B%22expr%22%3A%22up%20%3D%3D%200%22%2C%22tab%22%3A0%7D%5D",
			},
			template.Alert{Status: "firing", Labels: template.KV{
				"job":       "broken",
				"monitor":   "codelab-monitor",
				"alertname": "InstanceDown",
				"instance":  "localhost:5678",
			},
				Annotations:  template.KV{},
				StartsAt:     time.Unix(1460045332, 0),
				EndsAt:       time.Time{},
				GeneratorURL: "http://oldpad:9090/graph#%5B%7B%22expr%22%3A%22up%20%3D%3D%200%22%2C%22tab%22%3A0%7D%5D",
				Fingerprint:  "boop",
			},
		},
		GroupLabels: template.KV{"alertname": "InstanceDown"},
		CommonLabels: template.KV{
			"alertname": "InstanceDown",
			"instance":  "localhost:1234",
			"job":       "broken",
			"monitor":   "codelab-monitor",
		},
		CommonAnnotations: template.KV{},
		ExternalURL:       "http://oldpad:9093",
	}

	// A mapping of sample prometheus alert manager data, to the output we expect from the amDataToEnv function.
	amDataToEnvMap = map[*template.Data][]string{
		&alertManagerData: {
			"AMX_ALERT_1_END=0",
			"AMX_ALERT_1_LABEL_alertname=InstanceDown",
			"AMX_ALERT_1_LABEL_instance=localhost:1234",
			"AMX_ALERT_1_LABEL_job=broken",
			"AMX_ALERT_1_LABEL_monitor=codelab-monitor",
			"AMX_ALERT_1_START=1460045332",
			"AMX_ALERT_1_STATUS=firing",
			"AMX_ALERT_1_URL=http://oldpad:9090/graph#%5B%7B%22expr%22%3A%22up%20%3D%3D%200%22%2C%22tab%22%3A0%7D%5D",
			"AMX_ALERT_1_FINGERPRINT=",

			"AMX_ALERT_2_END=0",
			"AMX_ALERT_2_LABEL_alertname=InstanceDown",
			"AMX_ALERT_2_LABEL_instance=localhost:5678",
			"AMX_ALERT_2_LABEL_job=broken",
			"AMX_ALERT_2_LABEL_monitor=codelab-monitor",
			"AMX_ALERT_2_START=1460045332",
			"AMX_ALERT_2_STATUS=firing",
			"AMX_ALERT_2_URL=http://oldpad:9090/graph#%5B%7B%22expr%22%3A%22up%20%3D%3D%200%22%2C%22tab%22%3A0%7D%5D",
			"AMX_ALERT_2_FINGERPRINT=boop",

			"AMX_ALERT_LEN=2",
			"AMX_EXTERNAL_URL=http://oldpad:9093",
			"AMX_GLABEL_alertname=InstanceDown",
			"AMX_LABEL_alertname=InstanceDown",
			"AMX_LABEL_instance=localhost:1234",
			"AMX_LABEL_job=broken",
			"AMX_LABEL_monitor=codelab-monitor",
			"AMX_RECEIVER=default",
			"AMX_STATUS=firing"},
	}
)

// genServer returns a test server for the prometheus-am-executor.
func genServer() (*Server, error) {
	addr, err := RandLoopAddr()
	c := Config{
		ListenAddr: addr,
		Verbose:    false,
		Commands: []*Command{
			{Cmd: "echo"},
		},
	}
	s := NewServer(&c)
	return s, err
}

// getCounterValue digs through nested prometheus structs to retrieve a metric's value
func getCounterValue(cv *prometheus.CounterVec, labels ...string) (float64, error) {
	c, err := cv.GetMetricWithLabelValues(labels...)
	if err != nil {
		return 0, err
	}

	var metric pm.Metric
	err = c.Write(&metric)
	if err != nil {
		return 0, err
	}

	return metric.GetCounter().GetValue(), nil
}

// RandLoopAddr returns an available loopback address and TCP port
func RandLoopAddr() (string, error) {
	// When port 0 is specified, net.ListenTCP will automatically choose a port
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return "", err
	}

	ln, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return "", err
	}

	defer func() {
		_ = ln.Close()
	}()

	return ln.Addr().String(), nil
}

func Test_amDataToEnv(t *testing.T) {
	t.Parallel()
	for td, expectedEnv := range amDataToEnvMap {
		env := amDataToEnv(td)
		sort.Strings(env)
		sort.Strings(expectedEnv)

		if ok, err := checkers.DeepEqual(env, expectedEnv); !ok {
			log.Fatal(err)
		}
	}
}

func Test_handleHealth(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest("GET", "/_health", nil)
	w := httptest.NewRecorder()

	handleHealth(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Wrong response from handleHealth; got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Errorf("Failed to read response from handleHealth: %v", err)
	}
	expected := "All systems are functioning within normal specifications.\n"
	if string(body) != expected {
		t.Errorf("Unexpected response body; got %s, want %s", string(body), expected)
	}
}

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

func TestCommand_Matches(t *testing.T) {
	t.Parallel()
	noMatching := make(map[string]string)
	noMatching["banana"] = "ok"

	allMatching := make(map[string]string)
	// Randomly pick between 1 and all labels to include in allMatching
	available := alertManagerData.CommonLabels.Names()
	rand.Shuffle(len(available), func(i, j int) {
		available[i], available[j] = available[j], available[i]
	})
	for _, l := range available[0 : rand.Intn(len(available))+1] {
		allMatching[l] = alertManagerData.CommonLabels[l]
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
		if tc.cmd.Matches(&alertManagerData) != tc.want {
			t.Errorf("Case %d command %s matched alert; command labels %#v, alert labels %#v",
				i, condition_word, tc.cmd.MatchLabels, alertManagerData.CommonLabels)
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

func TestServer_handleWebhook(t *testing.T) {
	if runtime.GOOS == "aix" || runtime.GOOS == "android" || runtime.GOOS == "illumos" || runtime.GOOS == "js" ||
		runtime.GOOS == "plan9" || runtime.GOOS == "windows" {
		t.Skip("Skip on platforms without 'false' command available")
	}

	payload, err := json.Marshal(&alertManagerData)
	if err != nil {
		t.Errorf("Failed to encode alertManagerData as JSON")
	}

	srv, err := genServer()
	if err != nil {
		t.Errorf("Failed to generate server")
	}
	httpSrv, _ := srv.Start()
	defer func() {
		_ = stopServer(httpSrv)
	}()

	srvWithErrCmds, err := genServer()
	if err != nil {
		t.Errorf("Failed to generate server")
	}
	httpSrvWithErrCmds, _ := srvWithErrCmds.Start()
	defer func() {
		_ = stopServer(httpSrvWithErrCmds)
	}()

	// We'll expect 2 errors based on these commands
	srvWithErrCmds.config.Commands = append(srvWithErrCmds.config.Commands, &Command{Cmd: "false"})
	srvWithErrCmds.config.Commands = append(srvWithErrCmds.config.Commands, &Command{Cmd: "false"})

	cases := []struct {
		name           string
		server         *Server
		req            *http.Request
		w              *httptest.ResponseRecorder
		wantStatusCode int
		wantErrors     int
	}{
		{
			name:   "good",
			server: srv,
			// Send a request to handleWebhook
			req:            httptest.NewRequest("GET", "/", bytes.NewReader(payload)),
			w:              httptest.NewRecorder(),
			wantStatusCode: http.StatusOK,
			wantErrors:     0,
		},
		{
			name:           "cmd_errors",
			server:         srvWithErrCmds,
			req:            httptest.NewRequest("GET", "/", bytes.NewReader(payload)),
			w:              httptest.NewRecorder(),
			wantStatusCode: http.StatusInternalServerError,
			wantErrors:     2,
		},
	}

	for _, tc := range cases {
		tc := tc // Capture range variable, for use in anonymous function
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.server.handleWebhook(tc.w, tc.req)

			// Check response of request
			resp := tc.w.Result()
			if resp.StatusCode != tc.wantStatusCode {
				t.Errorf("Wrong response from handleWebhook; got %d, want %d", resp.StatusCode, tc.wantStatusCode)
			}

			// Check the process duration metric
			var pdMetric pm.Metric
			err = tc.server.processDuration.Write(&pdMetric)
			if err != nil {
				t.Errorf("Failed to retrieve processDuration metric from handleWebhook: %v", err)
			}
			durationCount := pdMetric.GetHistogram().GetSampleCount()
			if durationCount == 0 {
				t.Errorf("handleWebhook didn't observe processDuration metric samples")
			}

			// Check the process count metric
			var pcMetric pm.Metric
			err = tc.server.processCurrent.Write(&pcMetric)
			if err != nil {
				t.Errorf("Failed to retrieve processCurrent metric from handleWebhook: %v", err)
			}
			current := pcMetric.GetGauge().GetValue()
			if current > 0 {
				t.Errorf("handleWebhook metric says process is still running; got %f, want %d", current, 0)
			}

			// Check the error metrics
			for _, label := range []string{"read", "unmarshal", "start"} {
				count, err := getCounterValue(tc.server.errCounter, label)
				if err != nil {
					t.Errorf("Failed to retrieve '%s' count from handleWebhook: %v", label, err)
				} else if count > float64(tc.wantErrors) {
					t.Errorf("handleWebhook registered '%s' errors; got %f, want %d", label, count, tc.wantErrors)
				}
			}
		})
	}
}
