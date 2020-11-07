package main

import (
	"encoding/json"
	"fmt"
	"github.com/imgix/prometheus-am-executor/chanmap"
	"github.com/imgix/prometheus-am-executor/countermap"
	"github.com/prometheus/alertmanager/template"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// Namespace for prometheus metrics produced by this program
	metricNamespace = "am_executor"
)

var (
	procDurationOpts = prometheus.HistogramOpts{
		Namespace: metricNamespace,
		Subsystem: "process",
		Name:      "duration_seconds",
		Help:      "Time the processes handling alerts ran.",
		Buckets:   []float64{1, 10, 60, 600, 900, 1800},
	}

	procCurrentOpts = prometheus.GaugeOpts{
		Namespace: metricNamespace,
		Subsystem: "processes",
		Name:      "current",
		Help:      "Current number of processes running.",
	}

	errCountOpts = prometheus.CounterOpts{
		Namespace: metricNamespace,
		Subsystem: "errors",
		Name:      "total",
		Help:      "Total number of errors while processing alerts.",
	}

	errCountLabels = []string{"stage"}
)

type Server struct {
	config *Config
	// A mapping of an alarm fingerprint to a channel that can be used to
	// trigger action on all executing commands matching that fingerprint.
	// In our case, we want the ability to kill a running process if the matching channel is closed.
	// Alarms without a fingerprint aren't tracked by the map.
	tellFingers *chanmap.ChannelMap
	// A mapping of an alarm fingerprint to the number of commands being executed for it.
	// This is used to determine if a command should execute.
	fingerCount *countermap.Counter
	// An instance of metrics registry.
	// We use this instead of the default, because it only allows one instance of metrics to be registered.
	registry        *prometheus.Registry
	processDuration prometheus.Histogram
	processCurrent  prometheus.Gauge
	errCounter      *prometheus.CounterVec
}

// amDataToEnv converts prometheus alert manager template data into key=value strings,
// which are meant to be set as environment variables of commands called by this program..
func amDataToEnv(td *template.Data) []string {
	env := []string{
		"AMX_RECEIVER=" + td.Receiver,
		"AMX_STATUS=" + td.Status,
		"AMX_EXTERNAL_URL=" + td.ExternalURL,
		"AMX_ALERT_LEN=" + strconv.Itoa(len(td.Alerts)),
	}
	for p, m := range map[string]map[string]string{
		"AMX_LABEL":      td.CommonLabels,
		"AMX_GLABEL":     td.GroupLabels,
		"AMX_ANNOTATION": td.CommonAnnotations,
	} {
		for k, v := range m {
			env = append(env, p+"_"+k+"="+v)
		}
	}

	for i, alert := range td.Alerts {
		key := "AMX_ALERT_" + strconv.Itoa(i+1)
		env = append(env,
			key+"_STATUS"+"="+alert.Status,
			key+"_START"+"="+timeToStr(alert.StartsAt),
			key+"_END"+"="+timeToStr(alert.EndsAt),
			key+"_URL"+"="+alert.GeneratorURL,
			key+"_FINGERPRINT"+"="+alert.Fingerprint,
		)
		for p, m := range map[string]map[string]string{
			"LABEL":      alert.Labels,
			"ANNOTATION": alert.Annotations,
		} {
			for k, v := range m {
				env = append(env, key+"_"+p+"_"+k+"="+v)
			}
		}
	}
	return env
}

// concatErrors returns an error representing all of the errors' strings
func concatErrors(errors ...error) error {
	var s = make([]string, 0)
	for _, err := range errors {
		if err != nil {
			s = append(s, err.Error())
		}
	}

	return fmt.Errorf(strings.Join(s, "\n"))
}

// timeToStr converts the Time struct into a string representing its Unix epoch.
func timeToStr(t time.Time) string {
	if t.IsZero() {
		return "0"
	}
	return strconv.Itoa(int(t.Unix()))
}

// handleError responds to an HTTP request with an error message and logs it
func handleError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	log.Println(err)
}

// handleHealth is meant to respond to health checks for this program
func handleHealth(w http.ResponseWriter, req *http.Request) {
	_, err := fmt.Fprint(w, "All systems are functioning within normal specifications.\n")
	if err != nil {
		handleError(w, err)
	}
}

// amFiring handles a triggered alert message from alertmanager
func (s *Server) amFiring(amMsg *template.Data) []error {
	var env = amDataToEnv(amMsg)

	// Execute our commands, and wait for them to return
	var cmdErrors = make([]chan error, 0)
	for _, cmd := range s.config.Commands {
		ok, reason := s.CanRun(cmd, amMsg)
		if !ok {
			// This is not a command we should run for this alert.
			if s.config.Verbose {
				log.Printf("Skipping command due to '%s': %s", reason, cmd)
			}
			continue
		}
		if s.config.Verbose {
			log.Println("Executing:", cmd)
		}

		fingerprint, _ := cmd.Fingerprint(amMsg)
		out := make(chan error, 1)
		cmdErrors = append(cmdErrors, out)
		go s.instrument(fingerprint, cmd, env, out)
	}

	// Collect errors from our commands, which also has us wait for all commands to finish
	var errors = make([]error, 0)
	for len(cmdErrors) > 0 {
		out := cmdErrors[0]
		cmdErrors = cmdErrors[1:]
		for err := range out {
			if err != nil {
				errors = append(errors, err)
			}
		}
	}

	return errors
}

// amResolved handles a resolved alert message from alertmanager
func (s *Server) amResolved(amMsg *template.Data) {
	for _, cmd := range s.config.Commands {
		fingerprint, ok := cmd.Fingerprint(amMsg)
		if !ok || fingerprint == "" {
			// This is not a command that we support quitting based on a resolved alert
			continue
		}

		s.tellFingers.Close(fingerprint)
	}
}

// handleWebhook is meant to respond to webhook requests from prometheus alertmanager.
// It unpacks the alert, and dispatches it to the matching programs through environment variables.
//
// If a command fails, an HTTP 500 response is returned to alertmanager.
// Note that alertmanager may treat non HTTP 200 responses as 'failure to notify', and may re-dispatch the alert to us.
func (s *Server) handleWebhook(w http.ResponseWriter, req *http.Request) {
	if s.config.Verbose {
		log.Println("Webhook triggered from remote address:port", req.RemoteAddr)
	}
	data, err := ioutil.ReadAll(req.Body)
	if err != nil {
		handleError(w, err)
		s.errCounter.WithLabelValues("read").Inc()
		return
	}

	if s.config.Verbose {
		log.Println("Body:", string(data))
	}
	var amMsg = &template.Data{}
	if err := json.Unmarshal(data, amMsg); err != nil {
		handleError(w, err)
		s.errCounter.WithLabelValues("unmarshal").Inc()
		return
	}
	if s.config.Verbose {
		log.Printf("Got: %#v", amMsg)
	}

	var errors []error
	switch amMsg.Status {
	case "firing":
		errors = s.amFiring(amMsg)
	case "resolved":
		// When an alert is resolved, we will attempt to kill any active commands
		// that were dispatched on behalf of it, by matching commands against fingerprints
		// used to run them.
		s.amResolved(amMsg)
	default:
		errors = append(errors, fmt.Errorf("Unknown alertmanager message status: %s", amMsg.Status))
	}

	if len(errors) > 0 {
		handleError(w, concatErrors(errors...))
		s.errCounter.WithLabelValues("start").Add(float64(len(errors)))
	}
}

// instrument a command.
// It is meant to be called as a goroutine with context provided by handleWebhook.
//
// The prometheus structs use sync/atomic in methods like Dec and Observe,
// so they're safe to call concurrently from goroutines.
func (s *Server) instrument(fingerprint string, cmd *Command, env []string, err chan<- error) {
	s.processCurrent.Inc()
	defer s.processCurrent.Dec()
	var quit chan struct{}
	if len(fingerprint) > 0 {
		// The goroutine running the command will listen to this channel
		// to determine if it should exit early.
		quit = s.tellFingers.Add(fingerprint)
		// This value is used to determine if new commands matching this fingerprint should start.
		s.fingerCount.Inc(fingerprint)
		defer s.fingerCount.Dec(fingerprint)
	} else if s.config.Verbose {
		log.Println("Command has no fingerprint, so it won't quit early if alert is resolved first:", cmd)
	}

	done := make(chan struct{})
	start := time.Now()
	cmd.Run(err, quit, done, env...)
	<-done
	s.processDuration.Observe(time.Since(start).Seconds())
}

// CanRun returns true if the Command is allowed to run based on its fingerprint and settings
func (s *Server) CanRun(cmd *Command, amMsg *template.Data) (bool, string) {
	if !cmd.Matches(amMsg) {
		return false, "no match for alert labels"
	}

	if cmd.Max <= 0 {
		return true, "No maximum simultaneous command limit defined"
	}

	fingerprint, ok := cmd.Fingerprint(amMsg)
	if !ok || fingerprint == "" {
		return true, "No fingerprint found for command"
	}

	v, ok := s.fingerCount.Get(fingerprint)
	if !ok || v < cmd.Max {
		return true, "Command count for fingerprint is under limit"
	}

	return false, "Command count for fingerprint is over limit"
}

// Start runs a golang http server with the given routes.
// Returns
// * a reference to the HTTP server (so that we can gracefully shut it down)
// a channel that will contain the error result of the ListenAndServe call
func (s *Server) Start() (*http.Server, chan error) {
	s.registry.MustRegister(s.processDuration)
	s.registry.MustRegister(s.processCurrent)
	s.registry.MustRegister(s.errCounter)

	// We use our own instance of ServeMux instead of DefaultServeMux,
	// to keep handler registration separate between server instances.
	mux := http.NewServeMux()
	srv := &http.Server{Addr: s.config.ListenAddr, Handler: mux}
	mux.HandleFunc("/", s.handleWebhook)
	mux.HandleFunc("/_health", handleHealth)
	mux.Handle("/metrics", promhttp.Handler())

	// Start http server in a goroutine, so that it doesn't block other activities
	var httpSrvResult = make(chan error, 1)
	go func() {
		commands := make([]string, len(s.config.Commands))
		for i, e := range s.config.Commands {
			commands[i] = e.String()
		}
		log.Println("Listening on", s.config.ListenAddr, "with commands", strings.Join(commands, ", "))
		if (s.config.TLSCrt != "") && (s.config.TLSKey != "") {
			if s.config.Verbose {
				log.Println("HTTPS on")
			}
			httpSrvResult <- srv.ListenAndServeTLS(s.config.TLSCrt, s.config.TLSKey)
		} else {
			if s.config.Verbose {
				log.Println("HTTPS off")
			}
			httpSrvResult <- srv.ListenAndServe()
		}
	}()

	return srv, httpSrvResult
}

// NewServer returns a new server instance
func NewServer(config *Config) *Server {
	s := Server{
		config:          config,
		tellFingers:     chanmap.NewChannelMap(),
		fingerCount:     countermap.NewCounter(),
		registry:        prometheus.NewPedanticRegistry(),
		processDuration: prometheus.NewHistogram(procDurationOpts),
		processCurrent:  prometheus.NewGauge(procCurrentOpts),
		errCounter:      prometheus.NewCounterVec(errCountOpts, errCountLabels),
	}

	return &s
}
