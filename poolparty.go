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

	"github.com/valyala/fasthttp"
	"github.com/valyala/fastjson"
)

var (
	ErrWorkerPoolBusy   = errors.New("worker pool busy")
	ErrWorkerPoolClosed = errors.New("worker pool closed")
)

type PoolConfig struct {
	OnChildOutput        func(ln []byte)
	Logfn                func(keyvals ...interface{})
	NumWorkers           int
	WorkerProc           []string
	WorkerRequestTimeout time.Duration
}

// XXX would be much better if these were
// not strings, we probably want msgpack or
// flatbuffers or something like that.
type JanetRequest struct {
	RequestID     string            `json:"poolparty-request-id"`
	RemoteAddress string            `json:"remote-address"`
	Uri           string            `json:"uri"`
	Method        string            `json:"method"`
	Headers       map[string]string `json:"headers"`
	Body          string            `json:"body"`
}

type JanetResponse struct {
	RawResponse    []byte
	ParsedResponse *fastjson.Value
}

type workRequest struct {
	Req      JanetRequest
	RespChan chan workResponse
}

type ctlRequest struct {
	Req      interface{}
	RespChan chan interface{}
}

type restartWorkerProcRequest struct{}

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
	ctl           []chan ctlRequest
}

func NewWorkerPool(cfg PoolConfig) (*WorkerPool, error) {
	if cfg.Logfn == nil {
		cfg.Logfn = func(v ...interface{}) {}
	}
	if cfg.OnChildOutput == nil {
		cfg.OnChildOutput = func(ln []byte) {}
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
		ctl:           []chan ctlRequest{},
	}

	for i := 0; i < cfg.NumWorkers; i++ {
		p.spawnWorker()
	}

	return p, nil
}

func workerHandleCtlRequest(ctx context.Context, p *WorkerPool, req ctlRequest) (ok bool) {
	ok = false
	respChan := req.RespChan
	switch req := req.Req.(type) {
	case restartWorkerProcRequest:
		respChan <- struct{}{}
		return
	default:
		respChan <- fmt.Errorf("unknown request type: %v", req)
		return
	}
}

func workerHandleRequest(ctx context.Context, p *WorkerPool, workReq workRequest, out *json.Encoder, in *bufio.Reader) (ok bool) {
	ok = false

	err := out.Encode(workReq.Req)
	if err != nil {
		workReq.RespChan <- workResponse{Err: fmt.Errorf("error writing to worker process: %w", err)}
		return
	}

	rawResp, err := in.ReadBytes('\n')
	if err != nil {
		workReq.RespChan <- workResponse{Err: fmt.Errorf("decoding worker process response: %w", err)}
		return
	}

	parsedResp, err := fastjson.ParseBytes(rawResp)
	if err != nil {
		workReq.RespChan <- workResponse{Err: fmt.Errorf("decoding worker process response: %w", err)}
		return
	}

	workReq.RespChan <- workResponse{Resp: JanetResponse{
		RawResponse:    rawResp,
		ParsedResponse: parsedResp,
	}}

	ok = true
	return
}

func (p *WorkerPool) spawnWorker() {
	// These are deliberately not buffered.
	workerIndex := len(p.ctl)
	p.ctl = append(p.ctl, make(chan ctlRequest))
	p.wg.Add(1)
	go func(ctx context.Context) {
		defer p.wg.Done()

		for {
			var cmd *exec.Cmd
			cmdWorkerWg := &sync.WaitGroup{}

			logfn := func(vpairs ...interface{}) {
				if cmd != nil && cmd.Process != nil {
					vpairs = append(vpairs, "worker-pid", cmd.Process.Pid)
				}
				p.cfg.Logfn(vpairs...)
			}

			func() {

				perrmsg := "unable to create worker pipes"
				p1, p2, err := os.Pipe()
				if err != nil {
					logfn("msg", perrmsg, "err", err)
					return
				}
				defer p1.Close()
				defer p2.Close()
				p3, p4, err := os.Pipe()
				if err != nil {
					logfn("msg", perrmsg, "err", err)
					return
				}
				defer p3.Close()
				defer p4.Close()

				p5, p6, err := os.Pipe()
				if err != nil {
					logfn("msg", perrmsg, "err", err)
					return
				}
				defer p5.Close()
				defer p6.Close()

				if len(p.cfg.WorkerProc) > 1 {
					cmd = exec.Command(p.cfg.WorkerProc[0], p.cfg.WorkerProc[1:]...)
				} else {
					cmd = exec.Command(p.cfg.WorkerProc[0])
				}

				cmd.Stdin = p1
				cmd.Stdout = p4
				cmd.Stderr = p6

				cmdWorkerWg.Add(1)
				go func() {
					defer cmdWorkerWg.Done()
					brdr := bufio.NewReader(p5)
					for {
						ln, err := brdr.ReadBytes('\n')
						if len(ln) != 0 {
							p.cfg.OnChildOutput(ln)
						}
						if err != nil {
							return
						}
					}
				}()

				cmdShuttingDown := make(chan struct{})
				defer close(cmdShuttingDown)
				cmdWorkerWg.Add(1)
				go func() {
					defer cmdWorkerWg.Done()
					select {
					case <-ctx.Done():
						// If the context is cancelled, we need to propagate
						// the cancellation by closing these fd's early before
						// the current function returns.
						_ = p2.Close()
						_ = p3.Close()
					case <-cmdShuttingDown:
					}
				}()

				err = cmd.Start()
				if err != nil {
					logfn("msg", "unable to spawn worker", "err", err)
					return
				}

				logfn("msg", "worker spawned")

				// After the command has started, we need to close our side
				// of the pipes we gave it.
				_ = p1.Close()
				_ = p4.Close()
				_ = p6.Close()

				encoder := json.NewEncoder(p2)
				brdr := bufio.NewReader(p3)

				for {
					select {
					case <-p.workerCtx.Done():
						return
					case ctlRequest := <-p.ctl[workerIndex]:
						ok := workerHandleCtlRequest(ctx, p, ctlRequest)
						if !ok {
							logfn("msg", "Worker restarting due to ctl message")
							return
						}
					case workReq := <-p.dispatch:
						workerRequestTimeoutTimer := time.AfterFunc(p.cfg.WorkerRequestTimeout, func() {
							logfn("msg", "janet worker request timed out", "err", "timeout")
							_ = p2.Close()
							_ = p3.Close()
						})
						ok := workerHandleRequest(ctx, p, workReq, encoder, brdr)
						timerStopped := workerRequestTimeoutTimer.Stop()
						if !ok || !timerStopped {
							logfn("msg", "worker restarting due to error")
							return
						}
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
					logfn("msg", "pool worker died", "err", err)
				} else {
					logfn("msg", "worker shutdown by request")
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

	respChan := make(chan workResponse, 1)

	workReq := workRequest{
		Req:      req,
		RespChan: respChan,
	}

	t := time.NewTimer(timeout)
	select {
	case <-t.C:
		return JanetResponse{}, ErrWorkerPoolBusy
	case <-p.workerCtx.Done():
		t.Stop()
		return JanetResponse{}, ErrWorkerPoolClosed
	case p.dispatch <- workReq:
		t.Stop()
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

func (p *WorkerPool) RestartWorkers(ctx context.Context) error {
	for i := 0; i < len(p.ctl); i++ {
		respChan := make(chan interface{}, 1)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case p.ctl[i] <- ctlRequest{
			Req:      restartWorkerProcRequest{},
			RespChan: respChan,
		}:
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-respChan:
		}
	}

	return nil
}

func (p *WorkerPool) Close() {
	p.cancelWorkers()
	p.wg.Wait()
}

type HandlerConfig struct {
	Logfn                   func(keyvals ...interface{})
	WorkerRendezvousTimeout time.Duration
}

func MakeHTTPHandler(pool *WorkerPool, cfg HandlerConfig) fasthttp.RequestHandler {
	if cfg.Logfn == nil {
		cfg.Logfn = func(v ...interface{}) {}
	}
	logfn := cfg.Logfn
	return func(ctx *fasthttp.RequestCtx) {
		uri := ctx.Request.URI()
		id := fmt.Sprintf("%d", ctx.ID())

		reqHeaders := make(map[string]string)
		ctx.Request.Header.VisitAll(func(key, value []byte) {
			reqHeaders[string(key)] = string(value)
		})

		resp, err := pool.Dispatch(JanetRequest{
			RequestID:     id,
			RemoteAddress: ctx.RemoteAddr().String(),
			Uri:           string(uri.FullURI()),
			Headers:       reqHeaders,
			Method:        string(ctx.Request.Header.Method()),
			// XXX This copy could be expensive with a large body.
			Body: string(ctx.Request.Body()),
		}, cfg.WorkerRendezvousTimeout)
		if err != nil {
			logfn("msg", "error while dispatching to janet worker", "id", id, "err", err)
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
			// XXX Technically we should be merging duplicate headers.
			ctx.Response.Header.SetBytesKV(kBytes, vBytes)
		})

		ctx.SetBody(resp.ParsedResponse.GetStringBytes("body"))
	}
}
