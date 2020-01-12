package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/andrewchambers/poolparty"
	"github.com/coolmsg/go-coolmsg"
	log "github.com/inconshreveable/log15"
	flag "github.com/spf13/pflag"
	"github.com/valyala/fasthttp"
)

type fasthttpLogAdaptor struct {
	l log.Logger
}

func (l *fasthttpLogAdaptor) Printf(format string, args ...interface{}) {
	l.l.Info(fmt.Sprintf(format, args...))
}

func main() {
	log.Root().SetHandler(log.StderrHandler)

	workerRendezvousTimeout := flag.Duration("worker-rendezvous-timeout", 60*time.Second, "time to wait for a janet worker to accept a request")
	workerRequestTimeout := flag.Duration("worker-request-timeout", 60*time.Second, "timeout before a worker is considered crashed")
	readTimeout := flag.Duration("request-read-timeout", 60*time.Second, "read timeout before an http request is aborted")
	writeTimeout := flag.Duration("request-write-timeout", 60*time.Second, "write timeout before an http request is aborted")
	poolSize := flag.Int("pool-size", 1, "number of worker janet processes")
	requestBacklog := flag.Int("request-backlog", 1024, "number of requests to accept in the backlog ")
	maxRequestBodySize := flag.Int("max-request-body-size", 4*1024*1024, "number of requests to accept in the backlog")
	listenOn := flag.String("listen-on", "127.0.0.1:8080", "address to listen on.")
	ctlSocket := flag.String("ctl-socket", "./poolparty.sock", "control socket you can interact with using poolparty-ctl.")

	flag.Parse()

	cfg := poolparty.PoolConfig{
		Logger:               log.New(),
		NumWorkers:           *poolSize,
		WorkerProc:           flag.Args(),
		WorkerRequestTimeout: *workerRequestTimeout,
	}

	pool, err := poolparty.NewWorkerPool(cfg)
	if err != nil {
		log.Error("unable to start worker pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	ctlListener, err := net.Listen("unix", *ctlSocket)
	if err != nil {
		log.Error("unable to listen on ctl socket", "err", err)
		os.Exit(1)
	}
	defer os.Remove(*ctlSocket)

	ctlServer := coolmsg.NewServer(coolmsg.ServerOptions{
		ConnOptions: coolmsg.ConnServerOptions{
			BootstrapFunc: func(c io.ReadWriteCloser) coolmsg.Object { return &poolparty.RootCtlObject{Pool: pool} },
		},
	})

	go func() {
		_ = ctlServer.Serve(ctlListener)
	}()

	handler := poolparty.MakeHTTPHandler(pool, poolparty.HandlerConfig{
		WorkerRendezvousTimeout: *workerRendezvousTimeout,
	})

	server := &fasthttp.Server{
		Name:               "poolparty",
		ReadTimeout:        *readTimeout,
		WriteTimeout:       *writeTimeout,
		Concurrency:        *requestBacklog,
		MaxRequestBodySize: *maxRequestBodySize,
		Logger:             &fasthttpLogAdaptor{log.Root()},
		Handler:            handler,
		ReduceMemoryUsage:  true,
	}

	gracefulShutdown := make(chan struct{}, 1)

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		signal.Reset(os.Interrupt)
		log.Info("got shutdown signal, shutting down")
		_ = ctlListener.Close()
		server.Shutdown()
		close(gracefulShutdown)
	}()

	err = server.ListenAndServe(*listenOn)
	if err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
	<-gracefulShutdown
	log.Info("shutting down worker pool")
	pool.Close()
	log.Info("graceful shutdown complete")
}
