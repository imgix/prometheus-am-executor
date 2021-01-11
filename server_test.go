package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/juju/testing/checkers"
	"github.com/prometheus/alertmanager/template"
	"github.com/prometheus/client_golang/prometheus"
	pm "github.com/prometheus/client_model/go"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	// A sample alert from prometheus alert manager
	amData = template.Data{
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
		&amData: {
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

	amDataFinger = template.Data{
		Receiver: "default", Status: "firing", Alerts: template.Alerts{
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
			"instance":  "localhost:5678",
			"job":       "broken",
			"monitor":   "codelab-monitor",
		},
		CommonAnnotations: template.KV{},
		ExternalURL:       "http://oldpad:9093",
	}

	amDataFingerResolved = template.Data{
		Receiver: "default", Status: "resolved", Alerts: template.Alerts{
			template.Alert{Status: "resolved", Labels: template.KV{
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
			"instance":  "localhost:5678",
			"job":       "broken",
			"monitor":   "codelab-monitor",
		},
		CommonAnnotations: template.KV{},
		ExternalURL:       "http://oldpad:9093",
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

// getCounterValue returns a metric's value
func getCounterValue(cv *prometheus.CounterVec, label string) (float64, error) {
	var m = &pm.Metric{}
	err := cv.WithLabelValues(label).Write(m)
	if err != nil {
		return -1, err
	}
	return m.Counter.GetValue(), nil
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

// WaitForGetSuccess retries a request occasionally until it either succeeds or times-out.
func WaitForGetSuccess(url string) (*http.Response, error) {
	var wg sync.WaitGroup
	expiry := time.NewTimer(time.Duration(4) * time.Second)
	interval := time.NewTicker(time.Duration(200) * time.Millisecond)
	defer wg.Wait()
	defer expiry.Stop()
	defer interval.Stop()
	out := make(chan *http.Response, 1)

	wg.Add(1)
	go func() {
		defer close(out)
		defer wg.Done()
		for {
			select {
			case <-expiry.C:
				return
			case <-interval.C:
				resp, err := http.Get(url)
				if err == nil {
					out <- resp
					return
				}
			}
		}
	}()

	select {
	case <-expiry.C:
		return nil, fmt.Errorf("Timed-out while waiting for successful request to %s", url)
	case r := <-out:
		return r, nil
	}
}

func Test_amDataToEnv(t *testing.T) {
	t.Parallel()
	for td, expectedEnv := range amDataToEnvMap {
		env := amDataToEnv(td)
		sort.Strings(env)
		sort.Strings(expectedEnv)

		if ok, err := checkers.DeepEqual(env, expectedEnv); !ok {
			t.Fatal(err)
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

func Test_handleMetrics(t *testing.T) {
	t.Parallel()
	srv, err := genServer()
	if err != nil {
		t.Fatal("Failed to generate server")
	}
	httpSrv, _ := srv.Start()
	defer func() {
		_ = stopServer(httpSrv)
	}()

	time.Sleep(time.Duration(1) * time.Second)
	var path = "/metrics"
	resp, err := WaitForGetSuccess("http://" + srv.config.ListenAddr + path)
	if err != nil {
		t.Fatalf("Failed to get metrics: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	var sep = "_"
	var found = map[string]bool{
		strings.Join([]string{
			metricNamespace,
			procDurationOpts.Subsystem,
			procDurationOpts.Name}, sep): false,
		strings.Join([]string{
			metricNamespace,
			procCurrentOpts.Subsystem,
			procCurrentOpts.Name}, sep): false,
		strings.Join([]string{
			metricNamespace,
			errCountOpts.Subsystem,
			errCountOpts.Name}, sep): false,
		strings.Join([]string{
			metricNamespace,
			sigCountOpts.Subsystem,
			sigCountOpts.Name}, sep): false,
		strings.Join([]string{
			metricNamespace,
			skipCountOpts.Subsystem,
			skipCountOpts.Name}, sep): false,
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		for want, ok := range found {
			if ok {
				continue
			}
			if strings.Contains(line, want) {
				found[want] = true
			}
		}
	}

	missing := make([]string, 0)
	for want, ok := range found {
		if !ok {
			missing = append(missing, want)
		}
	}

	if len(missing) > 0 {
		t.Errorf("Missing in '%s' output: %s", path, strings.Join(missing, ", "))
	}
}

func Test_handleWebhook(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping due to -test.short flag")
	}
	if runtime.GOOS == "aix" || runtime.GOOS == "android" || runtime.GOOS == "illumos" || runtime.GOOS == "js" ||
		runtime.GOOS == "plan9" || runtime.GOOS == "windows" {
		t.Skip("Skip on platforms without 'false' or 'sleep' commands available")
	}

	// We can create pointers to variables, but not to primitive values like true/false directly.
	var alsoTrue = true
	var alsoFalse = false

	trigger, err := json.Marshal(&amDataFinger)
	if err != nil {
		t.Fatal("Failed to encode amDataFinger as JSON")
	}

	resolve, err := json.Marshal(&amDataFingerResolved)
	if err != nil {
		t.Fatal("Failed to encode amDataFingerResolve as JSON")
	}

	cases := []struct {
		name           string
		verbose        bool
		commands       []*Command
		reqs           []*http.Request
		statusCode     int
		errors         int
		signalled      int
		skipped        int
		stillRunningOk bool
	}{
		// The httptest.NewRequest() call sends a request to handleWebhook
		{
			name:       "good",
			commands:   []*Command{{Cmd: "echo"}},
			reqs:       []*http.Request{httptest.NewRequest("GET", "/", bytes.NewReader(trigger))},
			statusCode: http.StatusOK,
			errors:     0,
		},
		// We'll expect 2 errors based on these commands
		{
			name: "cmd_errors",
			commands: []*Command{
				{Cmd: "false"},
				{Cmd: "false", Args: []string{"banana"}},
			},
			reqs:       []*http.Request{httptest.NewRequest("GET", "/", bytes.NewReader(trigger))},
			statusCode: http.StatusInternalServerError,
			errors:     2,
		},
		// We'll expect 0 errors due to NotifyOnFailure being False
		{
			name: "no_error_notify",
			commands: []*Command{
				{Cmd: "false", NotifyOnFailure: &alsoFalse},
				{Cmd: "false", Args: []string{"banana"}, NotifyOnFailure: &alsoFalse},
			},
			reqs:       []*http.Request{httptest.NewRequest("GET", "/", bytes.NewReader(trigger))},
			statusCode: http.StatusOK,
			errors:     0,
		},
		// We'll expect 0 errors due to command being killed by being resolved
		{
			name:     "resolved",
			commands: []*Command{{Cmd: "sleep", Args: []string{"4s"}}},
			reqs: []*http.Request{
				httptest.NewRequest("GET", "/", bytes.NewReader(trigger)),
				httptest.NewRequest("GET", "/", bytes.NewReader(resolve)),
			},
			statusCode: http.StatusOK,
			errors:     0,
			signalled:  1,
		},
		// Expect no error due to command not being killed by being resolved, because IgnoreResolved is true
		{
			name:     "ignore_resolved",
			commands: []*Command{{Cmd: "sleep", Args: []string{"4s"}, IgnoreResolved: &alsoTrue}},
			reqs: []*http.Request{
				httptest.NewRequest("GET", "/", bytes.NewReader(trigger)),
				httptest.NewRequest("GET", "/", bytes.NewReader(resolve)),
			},
			statusCode:     http.StatusOK,
			errors:         0,
			signalled:      0,
			stillRunningOk: true,
		},

		// Expect 0 skipped due to no Max
		{
			name: "no_max",
			commands: []*Command{
				{Cmd: "sleep", Args: []string{"4s"}},
			},
			reqs: []*http.Request{
				httptest.NewRequest("GET", "/", bytes.NewReader(trigger)),
				httptest.NewRequest("GET", "/", bytes.NewReader(trigger)),
			},
			statusCode:     http.StatusOK,
			errors:         0,
			signalled:      0,
			skipped:        0,
			stillRunningOk: true,
		},
		// Expect 1 skipped due to Max being exceeded
		{
			name: "max",
			commands: []*Command{
				{Cmd: "sleep", Args: []string{"4s"}, Max: 1},
			},
			reqs: []*http.Request{
				httptest.NewRequest("GET", "/", bytes.NewReader(trigger)),
				httptest.NewRequest("GET", "/", bytes.NewReader(trigger)),
				httptest.NewRequest("GET", "/", bytes.NewReader(trigger)),
			},
			statusCode:     http.StatusOK,
			errors:         0,
			signalled:      0,
			skipped:        2,
			stillRunningOk: true,
		},
	}

	for _, tc := range cases {
		tc := tc // Capture range variable, for use in anonymous function
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, err := genServer()
			if err != nil {
				t.Fatal("Failed to generate server")
			}

			srv.config.Verbose = tc.verbose
			srv.config.Commands = tc.commands

			httpSrv, _ := srv.Start()
			defer func() {
				_ = stopServer(httpSrv)
			}()

			// We'll make a second request, if it is defined
			var req *http.Request
			var resp *http.Response
			for i, r := range tc.reqs {
				req = r
				w := httptest.NewRecorder()
				if len(tc.reqs) > 1 && i != len(tc.reqs)-1 {
					// If we're not the last command, we just issue the request and keep going
					go srv.handleWebhook(w, req)
					// Give some time for effects of request to take place (process starting, being killed, etc)
					time.Sleep(time.Duration(500) * time.Millisecond)
					continue
				}
				srv.handleWebhook(w, req)
				resp = w.Result()
			}
			time.Sleep(time.Duration(500) * time.Millisecond)

			// Check response of request
			if resp.StatusCode != tc.statusCode {
				t.Errorf("Wrong response from handleWebhook; got %d, want %d", resp.StatusCode, tc.statusCode)
			}

			// Check the process duration metric
			var pdMetric pm.Metric
			err = srv.processDuration.Write(&pdMetric)
			if err != nil {
				t.Fatalf("Failed to retrieve processDuration metric from handleWebhook: %v", err)
			}
			durationCount := pdMetric.GetHistogram().GetSampleCount()
			if !tc.stillRunningOk && durationCount == 0 {
				t.Errorf("handleWebhook didn't observe processDuration metric samples")
			}

			// Check the process count metric
			var pcMetric pm.Metric
			err = srv.processCurrent.Write(&pcMetric)
			if err != nil {
				t.Fatalf("Failed to retrieve processCurrent metric from handleWebhook: %v", err)
			}
			current := pcMetric.GetGauge().GetValue()
			if !tc.stillRunningOk && current > 0 {
				t.Errorf("handleWebhook metric says process is still running; got %f, want %d", current, 0)
			}

			// Check the error metrics
			count, err := getCounterValue(srv.errCounter, ErrLabelStart)
			if err != nil {
				t.Fatalf("Failed to retrieve %q error count: %v", ErrLabelStart, err)
			} else if count != float64(tc.errors) {
				t.Errorf("Wrong error count for %q; got %f, want %d", ErrLabelStart, count, tc.errors)
			}

			// Check signalled metrics
			count, err = getCounterValue(srv.sigCounter, SigLabelOk)
			if err != nil {
				t.Fatalf("Failed to retrieve %q signalled count: %v", "ok", err)
			} else if count != float64(tc.signalled) {
				t.Errorf("Wrong signalled count for %q; got %f, want %d", "ok", count, tc.signalled)
			}

			// Check skipped metrics
			skipLabels := make([]string, 0)
			for _, v := range CmdRunLabel {
				skipLabels = append(skipLabels, v)
			}

			var skipped float64
			for _, label := range skipLabels {
				count, err := getCounterValue(srv.skipCounter, label)
				if err != nil {
					t.Fatalf("Failed to retrieve %q skip count: %v", label, err)
				}
				skipped += count
			}
			if skipped != float64(tc.skipped) {
				t.Errorf("Wrong skipped count; got %f, want %d", skipped, tc.skipped)
			}
		})
	}
}

func TestServer_CanRun(t *testing.T) {
	t.Parallel()
	srv, err := genServer()
	if err != nil {
		t.Fatal("Failed to generate server")
	}
	httpSrv, _ := srv.Start()
	defer func() {
		_ = stopServer(httpSrv)
	}()

	var pass = func() {}
	var boop10 = func() { srv.fingerCount.IncBy("boop", 10) }
	var reset = func() { srv.fingerCount.Reset("boop") }
	cases := []struct {
		name    string
		command Command
		data    *template.Data
		ok      bool
		reason  CmdRunReason
		before  func()
		after   func()
	}{
		// Can run because no labels are defined
		{
			name:    "no_labels",
			command: Command{Cmd: "echo", Max: 99},
			data:    &amData,
			ok:      true,
			reason:  CmdRunNoFinger,
			before:  pass,
			after:   pass,
		},
		// Can't run because the command doesn't match any alert labels
		{
			name: "no_match",
			command: Command{
				Cmd: "echo",
				MatchLabels: map[string]string{
					"env":   "testing",
					"owner": "me",
				},
			},
			data:   &amData,
			ok:     false,
			reason: CmdRunNoLabelMatch,
			before: pass,
			after:  pass,
		},
		// Can run if there's no limit to instances of the command
		{
			name: "no_max",
			command: Command{
				Cmd: "echo",
				MatchLabels: map[string]string{
					"job":      "broken",
					"instance": "localhost:5678",
				},
				Max: -1,
			},
			data:   &amDataFinger,
			ok:     true,
			reason: CmdRunNoMax,
			before: boop10,
			after:  reset,
		},
		// Can run if there's no fingerprint
		{
			name: "no_fingerprint",
			command: Command{
				Cmd: "echo",
				MatchLabels: map[string]string{
					"job":      "broken",
					"instance": "localhost:1234",
				},
				Max: 2,
			},
			data:   &amData,
			ok:     true,
			reason: CmdRunNoFinger,
			before: boop10,
			after:  reset,
		},
		// Can run if fingerprint is under the limit
		{
			name: "fingerprint_under_limit",
			command: Command{
				Cmd: "echo",
				MatchLabels: map[string]string{
					"job":      "broken",
					"instance": "localhost:5678",
				},
				Max: 11,
			},
			data:   &amDataFinger,
			ok:     true,
			reason: CmdRunFingerUnder,
			before: boop10,
			after:  reset,
		},
		// Can't run if fingerprint is over the limit
		{
			name: "fingerprint_over_limit",
			command: Command{
				Cmd: "echo",
				MatchLabels: map[string]string{
					"job":      "broken",
					"instance": "localhost:5678",
				},
				Max: 2,
			},
			data:   &amDataFinger,
			ok:     false,
			reason: CmdRunFingerOver,
			before: boop10,
			after:  reset,
		},
	}

	for _, tc := range cases {
		tc := tc // Capture range variable, for use in anonymous function
		t.Run(tc.name, func(t *testing.T) {
			srv.config.Commands = []*Command{&tc.command}
			tc.before()
			defer tc.after()
			ok, reason := srv.CanRun(&tc.command, tc.data)
			if ok != tc.ok {
				t.Errorf("Wrong answer with reason '%s'; got %v, want %v", reason, ok, tc.ok)
			}
			if reason != tc.reason {
				t.Errorf("Wrong reason; got '%s', want '%s'", reason, tc.reason)
			}
		})
	}
}

func TestServer_Start(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	var expired = time.NewTimer(serverShutdownTime)
	defer expired.Stop()

	srv, err := genServer()
	if err != nil {
		t.Fatal("Failed to generate server")
	}
	httpSrv, srvResult := srv.Start()
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case err := <-srvResult:
			// Calling *http.Server.Shutdown() results in 'http: Server closed' error.
			// We want to ignore that in this case; We are intentionally shutting down the server.
			if err != nil && err.Error() != "http: Server closed" {
				t.Errorf("Failed to serve for %s: %v", srv.config.ListenAddr, err)
			}
		case <-expired.C:
			t.Errorf("Timed-out while waiting for server to stop")
		}
	}()

	err = stopServer(httpSrv)
	if err != nil {
		t.Errorf("Failed to shut down HTTP server: %v", err)
	}
	wg.Wait()
}

func TestNewServer(t *testing.T) {
	t.Parallel()
	addr, err := RandLoopAddr()
	if err != nil {
		t.Fatal(err)
	}
	c := Config{
		ListenAddr: addr,
		Verbose:    false,
		Commands: []*Command{
			{Cmd: "echo"},
		},
	}

	s := NewServer(&c)
	if s.config == nil {
		t.Error("Server missing 'config' field")
	}

	if s.tellFingers == nil {
		t.Error("Server missing 'tellFingers' field")
	}

	if s.fingerCount == nil {
		t.Error("Server missing 'fingerCount' field")
	}

	if s.registry == nil {
		t.Error("Server missing 'registry' field")
	}

	if s.errCounter == nil {
		t.Error("Server missing 'errCounter' field")
	}
}
