package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/andrewchambers/poolparty"
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

	workerRequestTimeout := flag.Duration("worker-request-timeout", 60*time.Second, "timeout before a worker is considered crashed")
	readTimeout := flag.Duration("request-read-timeout", 60*time.Second, "read timeout before an http request is aborted")
	writeTimeout := flag.Duration("request-write-timeout", 60*time.Second, "write timeout before an http request is aborted")
	poolSize := flag.Int("pool-size", 1, "Number of worker janet processes")
	requestBacklog := flag.Int("request-backlog", 2048, "number of requests to accept in the backlog")
	maxRequestBodySize := flag.Int("max-request-body-size", 4*1024*1024, "number of requests to accept in the backlog")
	staticDir := flag.String("static-dir", "", "dir to serve at /static/")

	flag.Parse()

	cfg := poolparty.PoolConfig{
		Logger:               log.New(),
		NumWorkers:           *poolSize,
		WorkerProc:           flag.Args(),
		WorkerRequestTimeout: *workerRequestTimeout,
	}

	log.Info("starting janet worker pool", "cfg", cfg)
	pool, err := poolparty.NewWorkerPool(cfg)
	if err != nil {
		log.Error("unable to start worker pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	var staticFileHandler fasthttp.RequestHandler

	if *staticDir != "" {
		staticDirPath, err := filepath.Abs(*staticDir)
		if err != nil {
			log.Error("unable to determine static file path", "err", err)
			os.Exit(1)
		}

		log.Info("serving static files", "dir", staticDirPath)
		staticFileHandler = fasthttp.FSHandler(staticDirPath, 1)
	}

	handler := func(ctx *fasthttp.RequestCtx) {
		startt := time.Now()
		id := fmt.Sprintf("%d", ctx.ID())
		log := log.New("id", id)
		uri := ctx.Request.URI()
		method := string(ctx.Method())
		path := string(uri.Path())
		log.Info("http request", "path", path, "method", method)

		if staticFileHandler != nil && strings.HasPrefix(path, "/static/") {
			staticFileHandler(ctx)
			log.Info("static request finished", "duration", time.Now().Sub(startt))
			return
		}

		resp, err := pool.Dispatch(poolparty.JanetRequest{
			RequestID: id,
			Headers:   string(ctx.Request.Header.RawHeaders()),
			Body:      string(ctx.Request.Body()),
		})
		log.Info("janet request finished", "duration", time.Now().Sub(startt))
		if err != nil {
			log.Error("error dispatching request to pool", "err", err)
			ctx.SetStatusCode(fasthttp.StatusInternalServerError)
			fmt.Fprintf(ctx, "internal server error\n")
			return
		}
		ctx.SetStatusCode(resp.Status)
		ctx.SetBody([]byte(resp.Body))
	}

	server := &fasthttp.Server{
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
		server.Shutdown()
		close(gracefulShutdown)
	}()

	err = server.ListenAndServe(":8080")
	if err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
	<-gracefulShutdown
	log.Info("shutting down worker pool")
	pool.Close()
	log.Info("graceful shutdown complete")
}
