package main

import (
	"bytes"
	"encoding/json"
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

func TestServer_handleWebhook(t *testing.T) {
	if runtime.GOOS == "aix" || runtime.GOOS == "android" || runtime.GOOS == "illumos" || runtime.GOOS == "js" ||
		runtime.GOOS == "plan9" || runtime.GOOS == "windows" {
		t.Skip("Skip on platforms without 'false' command available")
	}

	payload, err := json.Marshal(&amData)
	if err != nil {
		t.Fatal("Failed to encode amData as JSON")
	}

	srv, err := genServer()
	if err != nil {
		t.Fatal("Failed to generate server")
	}
	httpSrv, _ := srv.Start()
	defer func() {
		_ = stopServer(httpSrv)
	}()

	srvWithErrCmds, err := genServer()
	if err != nil {
		t.Fatal("Failed to generate server")
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
		want    bool
		before  func()
		after   func()
	}{
		// Can run because no labels are defined
		{
			name:    "no_labels",
			command: Command{Cmd: "echo"},
			data:    &amData,
			want:    true,
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
			data:    &amData,
			want:   false,
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
			data: &amDataFinger,
			want:   true,
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
			data:    &amData,
			want:   true,
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
			data: &amDataFinger,
			want:   true,
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
			data: &amDataFinger,
			want:   false,
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
			if ok != tc.want {
				t.Errorf("Wrong result with reason '%s'; got %v, want %v", reason, ok, tc.want)
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