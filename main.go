package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/http-echo/version"
)

var (
	listenFlag  = flag.String("listen", ":5678", "address and port to listen")
	textFlag    = flag.String("text", "", "text to put on the webpage")
	versionFlag = flag.Bool("version", false, "display version information")
	counterFlag = flag.Bool("counter", false, "count handled request by second bucket")

	// stdoutW and stderrW are for overriding in test.
	stdoutW = os.Stdout
	stderrW = os.Stderr

	requestCount  = make(map[string]int64)
	incomingReqCh = make(chan string, 10000)
	m = sync.Mutex{}
)

func main() {
	flag.Parse()

	// Asking for the version?
	if *versionFlag {
		fmt.Fprintln(stderrW, version.HumanVersion)
		os.Exit(0)
	}

	// Validation
	if *textFlag == "" {
		fmt.Fprintln(stderrW, "Missing -text option!")
		os.Exit(127)
	}

	args := flag.Args()
	if len(args) > 0 {
		fmt.Fprintln(stderrW, "Too many arguments!")
		os.Exit(127)
	}

	// Flag gets printed as a page
	mux := http.NewServeMux()
	mux.HandleFunc("/", httpLog(stdoutW, withAppHeaders(httpEcho(*textFlag))))

	// Health endpoint
	mux.HandleFunc("/health", withAppHeaders(httpHealth()))
	
	// Get metrics endpoint
	if *counterFlag {
		mux.HandleFunc("/metric", withAppHeaders(httpGetMetrics()))
	}

	server := &http.Server{
		Addr:    *listenFlag,
		Handler: mux,
	}
	serverCh := make(chan struct{})
	go func() {
		log.Printf("[INFO] server is listening on %s\n", *listenFlag)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("[ERR] server exited with: %s", err)
		}
		close(serverCh)
	}()

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt)
	
	if *counterFlag {
		go handleRequestCount(serverCh)
	}

	// Wait for interrupt
	<-signalCh

	log.Printf("[INFO] received interrupt, shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("[ERR] failed to shutdown server: %s", err)
	}

	// If we got this far, it was an interrupt, so don't exit cleanly
	os.Exit(2)
}

func httpEcho(v string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, v)
		
		// add request counter
		if *counterFlag {
			go func() {
				incomingReqCh <- fmt.Sprint(time.Now().Unix())
			}()
		}
	}
}

func httpHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"ok"}`)
	}
}

func handleRequestCount(stopCh chan struct{}) {
	for {
		select {
		case <-stopCh:
			return
		case k := <-incomingReqCh:
			m.Lock()
			if n, ok := requestCount[k]; !ok {
				requestCount[k] = 1
			} else {
				requestCount[k] = n + 1
			}
			m.Unlock()
		}
	}
}

func httpGetMetrics() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.ToUpper(r.Method) == "DELETE" {
			m.Lock()
			requestCount = make(map[string]int64, 10000)
			m.Unlock()
			w.Write([]byte("map reset"))
			return
		}
		w.Header().Set("content-type", "application/json")
		payload, _ := json.Marshal(requestCount)
		w.Write(payload)
	}
}
