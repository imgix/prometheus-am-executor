package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
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
	defaultListenAddr  = ":8080"
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
type Config struct {
	ListenAddr      string `yaml:"listen_address"`
	Verbose         bool   `yaml:"verbose"`
	processDuration prometheus.Histogram
	processCurrent  prometheus.Gauge
	errCounter      *prometheus.CounterVec
	Commands        []*Command `yaml:"commands"`
}

// Represent a command that could be run based on what labels match
type Command struct {
	Cmd  string   `yaml:"cmd"`
	Args []string `yaml:"args"`
	// Only execute this command when all of the given labels match.
	// The CommonLabels field of prometheus alert data is used for comparison.
	MatchLabels map[string]string `yaml:"match_labels"`
}

// Equal returns true if the Command is identical to another Command
func (c *Command) Equal(other *Command) bool {
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

// Matches returns true if all of its labels match against the given prometheus alert.
// If we have no MatchLabels defined, we also return true.
func (c *Command) Matches(alert *template.Data) bool {
	if len(c.MatchLabels) == 0 {
		return true
	}

	for k, v := range c.MatchLabels {
		other, ok := alert.CommonLabels[k]
		if !ok || v != other {
			return false
		}
	}

	return true
}

// handleWebhook is meant to respond to webhook requeests from prometheus alert manager.
// It unpacks the alert payload, and passes the information to the program specified in its configuration.
func (c *Config) handleWebhook(w http.ResponseWriter, req *http.Request) {
	if c.Verbose {
		log.Println("Webhook triggered")
	}
	data, err := ioutil.ReadAll(req.Body)
	if err != nil {
		handleError(w, err)
		c.errCounter.WithLabelValues("read").Inc()
		return
	}

	if c.Verbose {
		log.Println("Body:", string(data))
	}
	var payload = &template.Data{}
	if err := json.Unmarshal(data, payload); err != nil {
		handleError(w, err)
		c.errCounter.WithLabelValues("unmarshal").Inc()
		return
	}
	if c.Verbose {
		log.Printf("Got: %#v", payload)
	}

	var env = amDataToEnv(payload)

	// Define a function to instrument and run a command. This will be called in a goroutine.
	//
	// The prometheus structs use sync/atomic in methods like Dec and Observe,
	// so they're safe to call concurrently from goroutines.
	var do = func(cmd *Command, err chan<- error) {
		defer close(err)
		defer c.processCurrent.Dec()
		start := time.Now()
		e := run(cmd.Cmd, cmd.Args, env)
		c.processDuration.Observe(time.Since(start).Seconds())
		err <- e
	}

	// Execute our commands, and wait for them to return
	var results = make([]chan error, 0)
	for _, cmd := range c.Commands {
		if !cmd.Matches(payload) {
			// This is not a command we should run for this alert.
			if c.Verbose {
				log.Printf("Skipping non-matching command: %s %s", cmd.Cmd, strings.Join(cmd.Args, " "))
			}
			continue
		}
		if c.Verbose {
			log.Printf("Executing: %s %s", cmd.Cmd, strings.Join(cmd.Args, " "))
		}
		out := make(chan error, 1)
		results = append(results, out)
		c.processCurrent.Inc()
		go do(cmd, out)
	}

	// Collect errors from our commands, which also has us wait for all commands to finish
	var errors = make([]string, 0)
	for len(results) > 0 {
		out := results[0]
		results = results[1:]
		select {
		case err := <-out:
			if err != nil {
				errors = append(errors, err.Error())
			}
		default:
			results = append(results, out)
		}
	}

	if len(errors) > 0 {
		err := fmt.Errorf(strings.Join(errors, "\n"))
		handleError(w, err)
		c.errCounter.WithLabelValues("start").Add(float64(len(errors)))
	}
}

// HasCommand returns True if the config contains the given Command
func (c *Config) HasCommand(other *Command) bool {
	for _, cmd := range c.Commands {
		if cmd.Equal(other) {
			return true
		}
	}
	return false
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

// mergeConfigs returns a config representing all the Configs merged together,
// with later Config structs overriding settings in earlier ones (like ListenAddr).
// Commands are added if they are unique from others.
func mergeConfigs(all ...*Config) *Config {
	var merged = &Config{}

	for _, c := range all {
		if len(c.ListenAddr) > 0 {
			merged.ListenAddr = c.ListenAddr
		}
		merged.Verbose = merged.Verbose || c.Verbose
		merged.processDuration = c.processDuration
		merged.processCurrent = c.processCurrent
		merged.errCounter = c.errCounter

		for _, cmd := range c.Commands {
			if !merged.HasCommand(cmd) {
				merged.Commands = append(merged.Commands, cmd)
			}
		}
	}

	return merged
}

// readCli parses cli flags and populates them in config
func readCli(c *Config) (string, error) {
	var configFile string
	flag.StringVar(&c.ListenAddr, "l", "", fmt.Sprintf("HTTP Port to listen on (default \"%s\")", defaultListenAddr))
	flag.BoolVar(&c.Verbose, "v", false, "Enable verbose/debug logging")
	flag.StringVar(&configFile, "f", "", "YAML config file to use")
	flag.Parse()
	args := flag.Args()
	if len(configFile) == 0 && len(args) == 0 {
		return configFile, fmt.Errorf("missing command to execute on receipt of alarm")
	} else if len(args) == 0 {
		return configFile, nil
	}

	// Add the command specified at the cli to the config
	cmd := Command{
		Cmd: args[0],
	}

	if len(args) > 1 {
		cmd.Args = args[1:]
	}
	c.Commands = append(c.Commands, &cmd)

	return configFile, nil
}

// readConfig reads config from supported means (cli flags, config file), and returns a config struct for this program.
// Cli flags will overwrite settings in the config file.
func readConfig() (*Config, error) {
	var cli = &Config{}
	configFile, err := readCli(cli)
	if err != nil {
		flag.Usage()
		return nil, err
	}

	var file = &Config{}
	if len(configFile) > 0 {
		data, err := ioutil.ReadFile(configFile)
		err = yaml.Unmarshal(data, file)
		if err != nil {
			return nil, err
		}
	}

	c := mergeConfigs(file, cli)

	if len(c.Commands) == 0 {
		return nil, fmt.Errorf("missing command to execute on receipt of alarm")
	}

	if len(c.ListenAddr) == 0 {
		c.ListenAddr = defaultListenAddr
	}

	c.processDuration = prometheus.NewHistogram(procDurationOpts)
	c.processCurrent = prometheus.NewGauge(procCurrentOpts)
	c.errCounter = prometheus.NewCounterVec(errCountOpts, errCountLabels)

	return c, err
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
func serve(c *Config) (*http.Server, chan error) {
	prometheus.MustRegister(c.processDuration)
	prometheus.MustRegister(c.processCurrent)
	prometheus.MustRegister(c.errCounter)

	// Use DefaultServeMux as the handler
	srv := &http.Server{Addr: c.ListenAddr, Handler: nil}
	http.HandleFunc("/", c.handleWebhook)
	http.HandleFunc("/_health", handleHealth)
	http.Handle("/metrics", promhttp.Handler())

	// Start http server in a goroutine, so that it doesn't block other activities
	var httpSrvResult = make(chan error)
	go func() {
		commands := make([]string, len(c.Commands))
		for i, e := range c.Commands {
			commands[i] = e.Cmd
		}
		log.Println("Listening on", c.ListenAddr, "with commands", strings.Join(commands, ", "))
		httpSrvResult <- srv.ListenAndServe()
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
			log.Fatalf("Failed to serve for %s: %v", c.ListenAddr, err)
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
