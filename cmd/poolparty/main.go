package main


import (
	"fmt"
	"os"

	"github.com/andrewchambers/poolparty"
	"github.com/valyala/fasthttp"
	flag "github.com/spf13/pflag"
	log "github.com/inconshreveable/log15"
)

var global_pool *poolparty.WorkerPool
	
// request handler in fasthttp style, i.e. just plain function.
func handler(ctx *fasthttp.RequestCtx) {
	log.Info("got request", "ctx", ctx)
	resp, err := global_pool.Dispatch(poolparty.JanetRequest{
		Headers: string(ctx.Request.Header.RawHeaders()),
		Body: string(ctx.Request.Body()),
	})
	if err != nil {
		log.Error("error dispatching request to pool", "err", err)
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		fmt.Fprintf(ctx, "internal server error\n")
		return
	}
	ctx.SetStatusCode(resp.Status)
	ctx.SetBody([]byte(resp.Body))
}

func main () {
	log.Root().SetHandler(log.StderrHandler)
	flag.Parse()
	cfg := poolparty.PoolConfig{
		Logger: log.New(),
		DevMode: true,
		NumWorkers: 1,
		WorkerProc: flag.Args(),
	}
	log.Info("starting janet worker pool", "cfg", cfg)
	pool, err := poolparty.NewWorkerPool(cfg)
	if err != nil {
		log.Error("unable to start worker pool", "err", err)
		os.Exit(1)
	}
	global_pool = pool
	defer pool.Close()

	err = fasthttp.ListenAndServe(":8080", handler)
	log.Error("server stopped", "err", err)
	os.Exit(1)
}