package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"
)

const (
	// How long we are willing to wait for the HTTP server to shut down gracefully
	serverShutdownTime = time.Second * 4
)

// stopServer issues a time-limited server shutdown
func stopServer(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), serverShutdownTime)
	defer cancel()
	return srv.Shutdown(ctx)
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
	s := NewServer(c)
	defer s.fingerCount.Stop()

	// Listen for signals telling us to stop
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)

	// Start the http server
	srv, srvResult := s.Start()

	select {
	case err := <-srvResult:
		if err != nil {
			log.Fatalf("Failed to serve for %s: %v", c.ListenAddr, err)
		} else {
			log.Println("HTTP server shut down")
		}
	case s := <-signals:
		log.Println("Shutting down due to signal:", s)
		err := stopServer(srv)
		if err != nil {
			log.Printf("Failed to shut down HTTP server: %v", err)
		}
	}
}
