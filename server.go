package main

import (
	"encoding/json"
	"fmt"
	"github.com/imgix/prometheus-am-executor/chanmap"
	"github.com/imgix/prometheus-am-executor/countermap"
	"github.com/prometheus/alertmanager/template"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	pm "github.com/prometheus/client_model/go"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// Enum for reasons of why a command could or couldn't run
	CmdRunNoLabelMatch CmdRunReason = iota
	CmdRunNoMax
	CmdRunNoFinger
	CmdRunFingerUnder
	CmdRunFingerOver
)

const (
	// Namespace for prometheus metrics produced by this program
	metricNamespace = "am_executor"

	ErrLabelRead       = "read"
	ErrLabelUnmarshall = "unmarshal"
	ErrLabelStart      = "start"
	SigLabelOk         = "ok"
	SigLabelFail       = "fail"
)

var (
	CmdRunDesc = map[CmdRunReason]string{
		CmdRunNoLabelMatch: "No match for alert labels",
		CmdRunNoMax:        "No maximum simultaneous command limit defined",
		CmdRunNoFinger:     "No fingerprint found for command",
		CmdRunFingerUnder:  "Command count for fingerprint is under limit",
		CmdRunFingerOver:   "Command count for fingerprint is over limit",
	}

	// These labels are meant to be applied to prometheus metrics
	CmdRunLabel = map[CmdRunReason]string{
		CmdRunNoLabelMatch: "nomatch",
		CmdRunNoMax:        "nomax",
		CmdRunNoFinger:     "nofinger",
		CmdRunFingerUnder:  "fingerunder",
		CmdRunFingerOver:   "fingerover",
	}

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

	sigCountOpts = prometheus.CounterOpts{
		Namespace: metricNamespace,
		Subsystem: "signalled",
		Name:      "total",
		Help:      "Total number of active processes signalled due to alarm resolving.",
	}

	skipCountOpts = prometheus.CounterOpts{
		Namespace: metricNamespace,
		Subsystem: "skipped",
		Name:      "total",
		Help:      "Total number of commands that were skipped instead of run for matching alerts.",
	}

	errCountLabels  = []string{"stage"}
	sigCountLabels  = []string{"result"}
	skipCountLabels = []string{"reason"}
)

type CmdRunReason int

type Server struct {
	config *Config
	// A mapping of an alarm fingerprint to a channel that can be used to
	// trigger action on all executing commands matching that fingerprint.
	// In our case, we want the ability to signal a running process if the matching channel is closed.
	// Alarms without a fingerprint aren't tracked by the map.
	tellFingers *chanmap.ChannelMap
	// A mapping of an alarm fingerprint to the number of commands being executed for it.
	// This is compared to the Command.Max value to determine if a command should execute.
	fingerCount *countermap.Counter
	// An instance of metrics registry.
	// We use this instead of the default, because the default only allows one instance of metrics to be registered.
	registry        *prometheus.Registry
	processDuration prometheus.Histogram
	processCurrent  prometheus.Gauge
	errCounter      *prometheus.CounterVec
	// Track number of active processes signalled due to a 'resolved' message being received from alertmanager.
	sigCounter *prometheus.CounterVec
	// Track number of commands skipped instead of run.
	skipCounter *prometheus.CounterVec
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

// Label returns a prometheus-compatible label for a reason why a command could or couldn't run
func (r CmdRunReason) Label() string {
	return CmdRunLabel[r]
}

// String returns a string representation of the reason why a command could or couldn't run
func (r CmdRunReason) String() string {
	return CmdRunDesc[r]
}

// amFiring handles a triggered alert message from alertmanager
func (s *Server) amFiring(amMsg *template.Data) []error {
	var wg, collectWg sync.WaitGroup
	var env = amDataToEnv(amMsg)

	// Execute our commands, and wait for them to return
	type future struct {
		cmd *Command
		out chan CommandResult
	}

	// Aggregate error messages into a single channel
	var errors = make(chan error)
	var allErrors = make([]error, 0)
	wg.Add(2)
	go func() {
		defer wg.Done()
		collectWg.Wait()
		close(errors)
	}()
	go func() {
		defer wg.Done()
		for err := range errors {
			allErrors = append(allErrors, err)
		}
	}()

	// collect error messages returned by running the command
	var collect = func(f future) {
		defer collectWg.Done()
		var resultState Result
		for result := range f.out {
			resultState = resultState | result.Kind
			// We don't consider errors from CmdSigOk or CmdSigFail states, as
			// conditions that should be passed back to the caller.
			if result.Kind.Has(CmdFail) && result.Err != nil && f.cmd.ShouldNotify() {
				errors <- result.Err
			}
		}
		if s.config.Verbose {
			log.Printf("Command: %s, result: %s", f.cmd.String(), resultState)
		}
	}

	for _, cmd := range s.config.Commands {
		ok, reason := s.CanRun(cmd, amMsg)
		if !ok {
			// This is not a command we should run for this alert.
			if s.config.Verbose {
				log.Printf("Skipping command due to '%s': %s", reason, cmd)
			}
			s.skipCounter.WithLabelValues(reason.Label()).Inc()
			continue
		}
		if s.config.Verbose {
			log.Println("Executing:", cmd)
		}

		fingerprint, _ := cmd.Fingerprint(amMsg)
		out := make(chan CommandResult)
		collectWg.Add(1)
		go collect(future{cmd: cmd, out: out})
		// s.instrument() runs the command and updates related metrics
		go s.instrument(fingerprint, cmd, env, out)
	}

	// Wait for instrumentation, error collection to finish
	wg.Wait()

	return allErrors
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
		s.errCounter.WithLabelValues(ErrLabelRead).Inc()
		return
	}

	if s.config.Verbose {
		log.Println("Body:", string(data))
	}
	var amMsg = &template.Data{}
	if err := json.Unmarshal(data, amMsg); err != nil {
		handleError(w, err)
		s.errCounter.WithLabelValues(ErrLabelUnmarshall).Inc()
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
		// When an alert is resolved, we will attempt to signal any active commands
		// that were dispatched on behalf of it, by matching commands against fingerprints
		// used to run them.
		s.amResolved(amMsg)
	default:
		errors = append(errors, fmt.Errorf("Unknown alertmanager message status: %s", amMsg.Status))
	}

	if len(errors) > 0 {
		handleError(w, concatErrors(errors...))
	}
}

// initMetrics initializes prometheus metrics
func (s *Server) initMetrics() error {
	var pd, pc pm.Metric
	err := s.processDuration.Write(&pd)
	if err != nil {
		return err
	}

	err = s.processCurrent.Write(&pc)
	if err != nil {
		return err
	}

	_ = s.errCounter.WithLabelValues(ErrLabelRead)
	_ = s.errCounter.WithLabelValues(ErrLabelUnmarshall)
	_ = s.errCounter.WithLabelValues(ErrLabelStart)
	_ = s.sigCounter.WithLabelValues(ErrLabelStart)
	_ = s.sigCounter.WithLabelValues(SigLabelOk)
	_ = s.sigCounter.WithLabelValues(SigLabelFail)
	_ = s.skipCounter.WithLabelValues(CmdRunNoLabelMatch.Label())
	_ = s.skipCounter.WithLabelValues(CmdRunFingerOver.Label())

	return nil
}

// instrument a command.
// It is meant to be called as a goroutine with context provided by handleWebhook.
//
// The prometheus structs use sync/atomic in methods like Dec and Observe,
// so they're safe to call concurrently from goroutines.
func (s *Server) instrument(fingerprint string, cmd *Command, env []string, out chan<- CommandResult) {
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
	cmdOut := make(chan CommandResult)
	// Intercept responses from commands, so that we can update metrics we're interested in
	go func() {
		defer close(out)
		for r := range cmdOut {
			if r.Kind.Has(CmdFail) && r.Err != nil && cmd.ShouldNotify() {
				s.errCounter.WithLabelValues(ErrLabelStart).Inc()
			}
			if r.Kind.Has(CmdSigOk) {
				s.sigCounter.WithLabelValues(SigLabelOk).Inc()
			}
			if r.Kind.Has(CmdSigFail) {
				s.sigCounter.WithLabelValues(SigLabelFail).Inc()
			}
			out <- r
		}
	}()

	start := time.Now()
	cmd.Run(cmdOut, quit, done, env...)
	<-done
	s.processDuration.Observe(time.Since(start).Seconds())
}

// CanRun returns true if the Command is allowed to run based on its fingerprint and settings
func (s *Server) CanRun(cmd *Command, amMsg *template.Data) (bool, CmdRunReason) {
	if !cmd.Matches(amMsg) {
		return false, CmdRunNoLabelMatch
	}

	if cmd.Max <= 0 {
		return true, CmdRunNoMax
	}

	fingerprint, ok := cmd.Fingerprint(amMsg)
	if !ok || fingerprint == "" {
		return true, CmdRunNoFinger
	}

	v, ok := s.fingerCount.Get(fingerprint)
	if !ok || v < cmd.Max {
		return true, CmdRunFingerUnder
	}

	return false, CmdRunFingerOver
}

// Start runs a golang http server with the given routes.
// Returns
// * a reference to the HTTP server (so that we can gracefully shut it down)
// a channel that will contain the error result of the ListenAndServe call
func (s *Server) Start() (*http.Server, chan error) {
	s.registry.MustRegister(s.processDuration)
	s.registry.MustRegister(s.processCurrent)
	s.registry.MustRegister(s.errCounter)
	s.registry.MustRegister(s.sigCounter)
	s.registry.MustRegister(s.skipCounter)

	// Initialize metrics
	err := s.initMetrics()
	if err != nil {
		panic(err)
	}

	// We use our own instance of ServeMux instead of DefaultServeMux,
	// to keep handler registration separate between server instances.
	mux := http.NewServeMux()
	srv := &http.Server{Addr: s.config.ListenAddr, Handler: mux}
	mux.HandleFunc("/", s.handleWebhook)
	mux.HandleFunc("/_health", handleHealth)
	mux.Handle("/metrics", promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{
		// Prometheus can use the same logger we are, when printing errors about serving metrics
		ErrorLog: log.New(os.Stderr, "", log.LstdFlags),
		// Include metric handler errors in metrics output
		Registry: s.registry,
	}))

	// Start http server in a goroutine, so that it doesn't block other activities
	var httpSrvResult = make(chan error, 1)
	go func() {
		defer close(httpSrvResult)
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
		sigCounter:      prometheus.NewCounterVec(sigCountOpts, sigCountLabels),
		skipCounter:     prometheus.NewCounterVec(skipCountOpts, skipCountLabels),
	}

	return &s
}
