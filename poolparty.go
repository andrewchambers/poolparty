package poolparty

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	log "github.com/inconshreveable/log15"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fastjson"
)

var (
	ErrWorkerPoolBusy   = errors.New("worker pool busy")
	ErrWorkerPoolClosed = errors.New("worker pool closed")
)

type PoolConfig struct {
	Logger               log.Logger
	NumWorkers           int
	WorkerProc           []string
	WorkerRequestTimeout time.Duration
}

// XXX would be much better if these were
// not strings, we probably want msgpack or
// flatbuffers or something like that.
type JanetRequest struct {
	RequestID string            `json:"request-id"`
	Uri       string            `json:"uri"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body"`
}

type JanetResponse struct {
	RawResponse    []byte
	ParsedResponse *fastjson.Value
}

type workRequest struct {
	Req      JanetRequest
	RespChan chan workResponse
}

type workResponse struct {
	Err  error
	Resp JanetResponse
}

type WorkerPool struct {
	cfg           PoolConfig
	workerCtx     context.Context
	cancelWorkers func()
	wg            sync.WaitGroup
	dispatch      chan workRequest
}

func NewWorkerPool(cfg PoolConfig) (*WorkerPool, error) {
	if cfg.Logger == nil {
		cfg.Logger = log.New()
	}
	if cfg.NumWorkers < 0 {
		return nil, errors.New("pool needs at least one worker")
	}
	if len(cfg.WorkerProc) <= 0 {
		return nil, errors.New("pool worker proc must not be empty")
	}

	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	p := &WorkerPool{
		cfg:           cfg,
		workerCtx:     workerCtx,
		cancelWorkers: cancelWorkers,
		wg:            sync.WaitGroup{},
		dispatch:      make(chan workRequest),
	}

	for i := 0; i < cfg.NumWorkers; i++ {
		p.spawnWorker()
	}

	return p, nil
}

func (p *WorkerPool) spawnWorker() {
	p.wg.Add(1)
	go func(ctx context.Context) {
		defer p.wg.Done()

		for {
			logger := p.cfg.Logger
			var cmd *exec.Cmd
			cmdWorkerWg := &sync.WaitGroup{}

			func() {

				perrmsg := "unable to create worker pipes"
				p1, p2, err := os.Pipe()
				if err != nil {
					logger.Error(perrmsg, "err", err)
					return
				}
				defer p1.Close()
				defer p2.Close()
				p3, p4, err := os.Pipe()
				if err != nil {
					logger.Error(perrmsg, "err", err)
					return
				}
				defer p3.Close()
				defer p4.Close()

				p5, p6, err := os.Pipe()
				if err != nil {
					logger.Error(perrmsg, "err", err)
					return
				}
				defer p5.Close()
				defer p6.Close()

				if len(p.cfg.WorkerProc) > 1 {
					cmd = exec.Command(p.cfg.WorkerProc[0], p.cfg.WorkerProc[1:]...)
				} else {
					cmd = exec.Command(p.cfg.WorkerProc[0])
				}

				logger.Info("launching worker command", "cmd", cmd)

				cmd.Stdin = p1
				cmd.Stdout = p4
				cmd.Stderr = p6

				cmdWorkerWg.Add(1)
				go func() {
					defer cmdWorkerWg.Done()
					brdr := bufio.NewReader(p5)
					for {
						ln, err := brdr.ReadString('\n')
						if ln != "" {
							logger.Info("worker stderr", "ln", ln)
						}
						if err != nil {
							return
						}
					}
				}()

				err = cmd.Start()
				if err != nil {
					logger.Error("unable to spawn worker", "err", err)
					return
				}

				logger = logger.New("pid", fmt.Sprintf("%d", cmd.Process.Pid))
				logger.Info("worker spawned")

				// After the command has started, we need to close our side
				// of the pipes we gave it.
				_ = p1.Close()
				_ = p4.Close()
				_ = p6.Close()

				encoder := json.NewEncoder(p2)
				brdr := bufio.NewReader(p3)

				for {
					var workReq workRequest

					select {
					case <-p.workerCtx.Done():
						return
					case workReq = <-p.dispatch:
					}

					logger := logger.New("id", workReq.Req.RequestID)

					workerRequestTimeoutTimer := time.AfterFunc(p.cfg.WorkerRequestTimeout, func() {
						logger.Info("worker request timeout triggered")
						_ = p2.Close()
						_ = p3.Close()
					})
					defer workerRequestTimeoutTimer.Stop()

					err = encoder.Encode(workReq.Req)
					if err != nil {
						logger.Error("unable to forward request to worker", "err", err)
						select {
						case <-p.workerCtx.Done():
							return
						case workReq.RespChan <- workResponse{Err: fmt.Errorf("error writing to worker process: %w", err)}:
							logger.Error("writing request fails", "err", err)
							return
						}
					}

					rawResp, err := brdr.ReadBytes('\n')
					if err != nil {
						select {
						case <-p.workerCtx.Done():
							return
						case workReq.RespChan <- workResponse{Err: fmt.Errorf("decoding worker process response: %w", err)}:
							return
						}
					}

					parsedResp, err := fastjson.ParseBytes(rawResp)
					if err != nil {
						select {
						case <-p.workerCtx.Done():
							return
						case workReq.RespChan <- workResponse{Err: fmt.Errorf("decoding worker process response: %w", err)}:
							return
						}
					}

					select {
					case <-p.workerCtx.Done():
						return
					case workReq.RespChan <- workResponse{Resp: JanetResponse{
						RawResponse:    rawResp,
						ParsedResponse: parsedResp,
					}}:
					}

					// Timer has triggered, we need to restart the worker.
					if !workerRequestTimeoutTimer.Stop() {
						return
					}
				}

			}()

			// Ensure child is gone before we try again.
			var err error

			if cmd != nil {
				err = cmd.Wait()
			}
			cmdWorkerWg.Wait()

			if err != nil {
				if p.workerCtx.Err() == nil {
					logger.Error("pool worker died", "err", err)
				} else {
					logger.Info("worker shutdown by request")
				}
			}
			select {
			case <-p.workerCtx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}

	}(p.workerCtx)
}

func (p *WorkerPool) Dispatch(req JanetRequest, timeout time.Duration) (JanetResponse, error) {

	respChan := make(chan workResponse)

	workReq := workRequest{
		Req:      req,
		RespChan: respChan,
	}

	t := time.NewTimer(timeout)
	defer t.Stop()

	select {
	case <-t.C:
		return JanetResponse{}, ErrWorkerPoolBusy
	case <-p.workerCtx.Done():
		return JanetResponse{}, ErrWorkerPoolClosed
	case p.dispatch <- workReq:
	}

	select {
	case <-p.workerCtx.Done():
		return JanetResponse{}, ErrWorkerPoolClosed
	case r := <-workReq.RespChan:
		if r.Err != nil {
			return JanetResponse{}, fmt.Errorf("request failed: %w", r.Err)
		}
		return r.Resp, nil
	}
}

func (p *WorkerPool) Close() {
	p.cancelWorkers()
	p.wg.Wait()
}

func MakeHTTPHandler(pool *WorkerPool, workerRendezvousTimeout time.Duration) fasthttp.RequestHandler {
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

		resp, err := pool.Dispatch(JanetRequest{
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
