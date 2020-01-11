package main

import (
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/andrewchambers/poolparty"
	log "github.com/inconshreveable/log15"
	flag "github.com/spf13/pflag"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fastjson"
)

type fasthttpLogAdaptor struct {
	l log.Logger
}

func (l *fasthttpLogAdaptor) Printf(format string, args ...interface{}) {
	l.l.Info(fmt.Sprintf(format, args...))
}

func MakeHandler(pool *poolparty.WorkerPool, workerRendezvousTimeout time.Duration) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		startt := time.Now()
		id := fmt.Sprintf("%d", ctx.ID())
		log := log.New("id", id)
		uri := ctx.Request.URI()
		method := string(ctx.Method())
		path := string(uri.Path())
		log.Info("http request", "path", path, "method", method)

		reqHeaders := make(map[string]string)
		ctx.Request.Header.VisitAll(func(key, value []byte) {
			reqHeaders[string(key)] = string(value)
		})

		resp, err := pool.Dispatch(poolparty.JanetRequest{
			RequestID: id,
			// XXX Uri: string(uri.FullURI()),
			Uri:     string(uri.Path()),
			Headers: reqHeaders,
			Method:  string(ctx.Request.Header.Method()),
			Body:    string(ctx.Request.Body()),
		}, workerRendezvousTimeout)
		log.Info("janet worker request finished", "duration", time.Now().Sub(startt))
		if err != nil {
			log.Error("error while dispatching to janet worker", "err", err)
			ctx.SetStatusCode(fasthttp.StatusInternalServerError)
			ctx.SetBody([]byte("internal server error\n"))
			return
		}

		if resp.ParsedResponse.Exists("file") {
			fasthttp.ServeFile(ctx, string(resp.ParsedResponse.GetStringBytes("file")))
			return
		}

		if resp.ParsedResponse.Exists("status") {
			ctx.SetStatusCode(resp.ParsedResponse.GetInt("status"))
		} else {
			ctx.SetStatusCode(fasthttp.StatusOK)
		}

		respHeaders := resp.ParsedResponse.GetObject("headers")
		respHeaders.Visit(func(kBytes []byte, v *fastjson.Value) {
			vBytes := v.GetStringBytes()
			ctx.Response.Header.SetBytesKV(kBytes, vBytes)
		})

		ctx.SetBody(resp.ParsedResponse.GetStringBytes("body"))
	}
}

func main() {
	log.Root().SetHandler(log.StderrHandler)

	workerRendezvousTimeout := flag.Duration("worker-rendezvous-timeout", 60*time.Second, "time to wait for a janet worker to begin a request")
	workerRequestTimeout := flag.Duration("worker-request-timeout", 60*time.Second, "timeout before a worker is considered crashed")
	readTimeout := flag.Duration("request-read-timeout", 60*time.Second, "read timeout before an http request is aborted")
	writeTimeout := flag.Duration("request-write-timeout", 60*time.Second, "write timeout before an http request is aborted")
	poolSize := flag.Int("pool-size", 1, "number of worker janet processes")
	requestBacklog := flag.Int("request-backlog", 1024, "number of requests to accept in the backlog")
	maxRequestBodySize := flag.Int("max-request-body-size", 4*1024*1024, "number of requests to accept in the backlog")
	listenOn := flag.String("listen-on", "127.0.0.1:8080", "address to listen on.")

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

	handler := MakeHandler(pool, *workerRendezvousTimeout)

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
