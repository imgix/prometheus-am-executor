package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"time"

	"github.com/prometheus/alertmanager/template"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	// Namespace for prometheus metrics produced by this program
	metricNamespace = "am_executor"
	// How long we are willing to wait for the HTTP server to shut down gracefully
	serverShutdownTime = time.Second * 4
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

// Represent the configuration for this program
type config struct {
	listenAddr      string
	verbose         bool
	processDuration prometheus.Histogram
	processCurrent  prometheus.Gauge
	errCounter      *prometheus.CounterVec
	command         string
	args            []string
}

// handleWebhook is meant to respond to webhook requeests from prometheus alert manager.
// It unpacks the alert payload, and passes the information to the program specified in its configuration.
func (c *config) handleWebhook(w http.ResponseWriter, req *http.Request) {
	if c.verbose {
		log.Println("Webhook triggered")
	}
	data, err := ioutil.ReadAll(req.Body)
	if err != nil {
		handleError(w, err)
		c.errCounter.WithLabelValues("read").Inc()
		return
	}

	if c.verbose {
		log.Println("Body:", string(data))
	}
	payload := &template.Data{}
	if err := json.Unmarshal(data, payload); err != nil {
		handleError(w, err)
		c.errCounter.WithLabelValues("unmarshal").Inc()
		return
	}
	if c.verbose {
		log.Printf("Got: %#v", payload)
	}

	c.processCurrent.Inc()
	start := time.Now()
	err = run(c.command, c.args, amDataToEnv(payload))
	c.processDuration.Observe(time.Since(start).Seconds())
	c.processCurrent.Dec()
	if err != nil {
		handleError(w, err)
		c.errCounter.WithLabelValues("start").Inc()
	}
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

// readCli parses cli flags and populates them in config
func readCli(c *config) error {
	flag.StringVar(&c.listenAddr, "l", ":8080", "HTTP Port to listen on")
	flag.BoolVar(&c.verbose, "v", false, "Enable verbose/debug logging")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		return fmt.Errorf("Missing command to execute on receipt of alarm")
	}

	c.command = args[0]
	if len(args) > 1 {
		c.args = args[1:]
	}

	return nil
}

// readConfig reads config from supported means (cli flags, config file), and returns a config struct for this program.
// Cli flags will overwrite settings in the config file.
func readConfig() (*config, error) {
	c := config{}
	err := readCli(&c)
	if err != nil {
		flag.Usage()
	}

	c.processDuration = prometheus.NewHistogram(procDurationOpts)
	c.processCurrent = prometheus.NewGauge(procCurrentOpts)
	c.errCounter = prometheus.NewCounterVec(errCountOpts, errCountLabels)

	return &c, err
}

// run executes the given command with the given environment, and attaches its STDOUT and STDERR to the logger.
func run(name string, args []string, env []string) error {
	lw := log.Writer()
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = lw
	cmd.Stderr = lw
	return cmd.Run()
}

// serve starts the golang http server with the given routes, and returns
// a reference to the HTTP server (so that one could gracefully shut it down), and
// a channel that will contain the error result of the ListenAndServe call
func serve(c *config) (*http.Server, chan error) {
	prometheus.MustRegister(c.processDuration)
	prometheus.MustRegister(c.processCurrent)
	prometheus.MustRegister(c.errCounter)

	// Use DefaultServeMux as the handler
	srv := &http.Server{Addr: c.listenAddr, Handler: nil}
	http.HandleFunc("/", c.handleWebhook)
	http.HandleFunc("/_health", handleHealth)
	http.Handle("/metrics", promhttp.Handler())

	// Start http server in a goroutine, so that it doesn't block other activities
	var httpSrvResult = make(chan error)
	go func() {
		err := srv.ListenAndServe()
		log.Println("Listening on", c.listenAddr, "with command", c.command)
		httpSrvResult <- err
	}()

	return srv, httpSrvResult
}

// timeToStr converts the Time struct into a string representing its Unix epoch.
func timeToStr(t time.Time) string {
	if t.IsZero() {
		return "0"
	}
	return strconv.Itoa(int(t.Unix()))
}

func init() {
	// Customize the flag.Usage function's output
	flag.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: %s [options] script [args..]\n\n", os.Args[0])
		flag.PrintDefaults()
	}
}

func main() {
	// Determine configuration for service
	c, err := readConfig()
	if err != nil {
		log.Fatalf("Couldn't determine configuration: %v", err)
	}

	// Listen for signals telling us to stop
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)

	// Start the http server
	srv, srvResult := serve(c)

	select {
	case err := <-srvResult:
		if err != nil {
			log.Fatalf("Failed to serve for %s: %v", c.listenAddr, err)
		} else {
			log.Println("HTTP server shut down")
		}
	case s := <-signals:
		log.Println("Shutting down due to signal:", s)
		ctx, cancel := context.WithTimeout(context.Background(), serverShutdownTime)
		defer cancel()
		err := srv.Shutdown(ctx)
		if err != nil {
			log.Printf("Failed to shut down HTTP server: %v", err)
		}
	}
}
