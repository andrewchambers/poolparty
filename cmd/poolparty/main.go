package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/andrewchambers/poolparty"
	"github.com/andrewchambers/poolparty/textctl"
	"github.com/go-logfmt/logfmt"
	flag "github.com/spf13/pflag"
	"github.com/valyala/fasthttp"
)

var logenc *logfmt.Encoder = logfmt.NewEncoder(os.Stderr)
var logmut sync.Mutex

func rawlog(ln []byte) {
	logmut.Lock()
	os.Stderr.Write(ln)
	logmut.Unlock()
}

func log(kvs ...interface{}) {
	logmut.Lock()
	logenc.EncodeKeyvals(kvs...)
	logenc.EndRecord()
	logmut.Unlock()
}

type fasthttpLogAdaptor struct {
}

func (l *fasthttpLogAdaptor) Printf(format string, args ...interface{}) {
	log("msg", fmt.Sprintf(format, args...))
}

func main() {
	staticRoot := flag.String("static-root", "", "Path to serve static files from.")
	staticNoBrotli := flag.Bool("static-no-brotli", false, "Don't use brotli compression.")
	staticCompress := flag.Bool("static-compress", false, "Try to use compressed files or compress them if not already compressed.")
	staticUrlPrefix := flag.String("static-url-prefix", "/static/", "Serve static files below this prefix.")
	workerRendezvousTimeout := flag.Duration("worker-rendezvous-timeout", 60*time.Second, "Time to wait for a janet worker to accept a request.")
	workerSpawnTimeout := flag.Duration("worker-spawn-timeout", 50*time.Millisecond, "Time to wait for a janet worker before spawning a new one to meet demand.")
	workerRequestTimeout := flag.Duration("worker-request-timeout", 60*time.Second, "Time before a worker is considered crashed.")
	workerRestartDelay := flag.Duration("worker-restart-delay", 1*time.Second, "Delay between worker restarts.")
	workerHealthCheckInterval := flag.Duration("worker-health-check-interval", 120*time.Second, "Delay between worker health checks.")
	readTimeout := flag.Duration("request-read-timeout", 60*time.Second, "Read timeout before an http request is aborted.")
	writeTimeout := flag.Duration("request-write-timeout", 60*time.Second, "Write timeout before an http request is aborted.")
	workerAttritionDelay := flag.Duration("worker-attrition-delay", 120*time.Second, "If no requests arrive in this period, a worker will be culled (down to the minimum pool size).")
	minPoolSize := flag.Uint("min-pool-size", 1, "Minimum number of worker processes.")
	maxPoolSize := flag.Uint("max-pool-size", 1, "Maximum number of worker processes.")
	requestBacklog := flag.Int("request-backlog", 1024, "Number of requests to accept in the backlog.")
	maxRequestBodySize := flag.Int("max-request-body-size", 4*1024*1024, "Maximum request size in bytes.")
	listenOn := flag.String("listen-address", "127.0.0.1:8080", "Address to listen on.")
	ctlSocket := flag.String("ctl-socket", "./poolparty.sock", "Control socket you can interact with using poolparty-ctl.")

	flag.Parse()

	cfg := poolparty.PoolConfig{
		OnWorkerOutput:            rawlog,
		WorkerSpawnTimeout:        *workerSpawnTimeout,
		WorkerRendezvousTimeout:   *workerRendezvousTimeout,
		WorkerRestartDelay:        *workerRestartDelay,
		WorkerAttritionDelay:      *workerAttritionDelay,
		WorkerRequestTimeout:      *workerRequestTimeout,
		WorkerHealthCheckInterval: *workerHealthCheckInterval,
		Logfn:                     log,
		MinWorkers:                uint32(*minPoolSize),
		MaxWorkers:                uint32(*maxPoolSize),
		WorkerProc:                flag.Args(),
	}

	pool, err := poolparty.NewWorkerPool(cfg)
	if err != nil {
		log("msg", "unable to start worker pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	ctlListener, err := net.Listen("unix", *ctlSocket)
	if err != nil {
		log("msg", "unable to listen on ctl socket", "err", err)
		os.Exit(1)
	}
	defer os.Remove(*ctlSocket)

	go textctl.Serve(ctlListener, &poolparty.CtlHandler{Pool: pool})

	handler := poolparty.MakeHTTPHandler(pool, poolparty.HandlerConfig{
		Logfn:           log,
		StaticCompress:  *staticCompress,
		StaticNoBrotli:  *staticNoBrotli,
		StaticRoot:      *staticRoot,
		StaticUrlPrefix: *staticUrlPrefix,
	})

	server := &fasthttp.Server{
		Name:               "poolparty",
		ReadTimeout:        *readTimeout,
		WriteTimeout:       *writeTimeout,
		Concurrency:        *requestBacklog,
		MaxRequestBodySize: *maxRequestBodySize,
		Logger:             &fasthttpLogAdaptor{},
		Handler:            handler,
		ReduceMemoryUsage:  true,
	}

	gracefulShutdown := make(chan struct{}, 1)

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		signal.Reset(os.Interrupt)
		log("msg", "got shutdown signal, shutting down")
		_ = ctlListener.Close()
		server.Shutdown()
		close(gracefulShutdown)
	}()

	err = server.ListenAndServe(*listenOn)
	if err != nil {
		log("msg", "server stopped", "err", err)
		os.Exit(1)
	}
	<-gracefulShutdown
	log("msg", "shutting down worker pool")
	pool.Close()
	log("msg", "graceful shutdown complete")
}
