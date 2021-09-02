package poolparty

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"git.sr.ht/~sircmpwn/go-bare"
	"github.com/valyala/fasthttp"
)

var (
	ErrWorkerPoolBusy   = errors.New("worker pool busy")
	ErrWorkerPoolClosed = errors.New("worker pool closed")
)

type PoolConfig struct {
	Logfn                     func(keyvals ...interface{})
	MinWorkers                uint32
	MaxWorkers                uint32
	OnWorkerOutput            func(ln []byte)
	WorkerProc                []string
	WorkerSpawnTimeout        time.Duration
	WorkerRendezvousTimeout   time.Duration
	WorkerRequestTimeout      time.Duration
	WorkerRestartDelay        time.Duration
	WorkerAttritionDelay      time.Duration
	WorkerHealthCheckInterval time.Duration
}

type HTTPRequest struct {
	RemoteAddress string
	Uri           string
	Method        string
	Headers       map[string]string
	Body          []byte
	RespChan      chan workResponse
}

type workRequest struct {
	Req      HTTPRequest
	RespChan chan workResponse
}

type HTTPResponse struct {
	Status  int
	Headers map[string][]string
	Body    []byte
}

type workResponse struct {
	Err  error
	Resp HTTPResponse
}

type ctlRequest struct {
	Req      interface{}
	RespChan chan interface{}
}

type restartWorkerProcRequest struct{}
type removeWorkerProcRequest struct{}

type WorkerPool struct {
	cfg              PoolConfig
	mu               sync.Mutex
	workerCtx        context.Context
	cancelAllWorkers func()
	wg               sync.WaitGroup
	dispatch         chan workRequest
	ctl              []chan ctlRequest
	numWorkers       uint32
	cancelWorker     []func()
	attritionMarker  int32
	workerRestarts   uint64
}

func NewWorkerPool(cfg PoolConfig) (*WorkerPool, error) {
	if cfg.Logfn == nil {
		cfg.Logfn = func(v ...interface{}) {}
	}
	if cfg.OnWorkerOutput == nil {
		cfg.OnWorkerOutput = func(ln []byte) {}
	}
	if cfg.MinWorkers < 0 {
		return nil, errors.New("pool minimum worker count must be greater than or equal to zero")
	}
	if cfg.MaxWorkers == 0 {
		return nil, errors.New("pool maximum worker count must not be zero")
	}
	if cfg.MaxWorkers >= 10000000 {
		return nil, errors.New("pool maximum worker count must less than 10000000")
	}
	if cfg.MaxWorkers < cfg.MinWorkers {
		return nil, errors.New("pool maximum worker count must be greater than or equal to the minimum")
	}
	if len(cfg.WorkerProc) <= 0 {
		return nil, errors.New("pool worker proc must not be empty")
	}

	attritionTicker := time.NewTicker(cfg.WorkerAttritionDelay)

	workerCtx, cancelAllWorkers := context.WithCancel(context.Background())
	p := &WorkerPool{
		cfg:              cfg,
		workerCtx:        workerCtx,
		cancelAllWorkers: cancelAllWorkers,
		wg:               sync.WaitGroup{},
		dispatch:         make(chan workRequest),
		ctl:              []chan ctlRequest{},
		cancelWorker:     []func(){},
		attritionMarker:  1, // Start wanting a check.
	}

	for i := uint32(0); i < cfg.MinWorkers; i++ {
		p.SpawnWorker()
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer attritionTicker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-attritionTicker.C:
				// If the attrition marker remains 1 for a whole tick, remove a worker.
				shouldRemove := atomic.LoadInt32(&p.attritionMarker) == 1
				atomic.StoreInt32(&p.attritionMarker, 1)
				if shouldRemove {
					p.RemoveWorker()
				}
			}
		}
	}()

	return p, nil
}

type WorkerPoolStats struct {
	Workers        uint32
	WorkerRestarts uint64
}

func (p *WorkerPool) Stats() WorkerPoolStats {
	return WorkerPoolStats{
		Workers:        p.NumWorkers(),
		WorkerRestarts: atomic.LoadUint64(&p.workerRestarts),
	}
}

func workerHandleRequest(ctx context.Context, p *WorkerPool, workReq workRequest, out io.Writer, in io.Reader) (ok bool) {
	ok = false

	var buf bytes.Buffer
	buf.Grow(256)
	bw := bare.NewWriter(&buf)
	// Reserve space for size.
	_ = bw.WriteU32(0)
	// Request variant.
	_ = bw.WriteUint(0)
	_ = bw.WriteString(workReq.Req.RemoteAddress)
	_ = bw.WriteString(workReq.Req.Uri)
	_ = bw.WriteString(workReq.Req.Method)
	_ = bw.WriteUint(uint64(len(workReq.Req.Headers)))
	for k, v := range workReq.Req.Headers {
		_ = bw.WriteString(k)
		_ = bw.WriteString(v)
	}
	_ = bw.WriteUint(uint64(len(workReq.Req.Body)))

	bufBytes := buf.Bytes()

	reqLen := len(bufBytes) + len(workReq.Req.Body) - 4
	if reqLen > 0x7fffffff {
		workReq.RespChan <- workResponse{Err: fmt.Errorf("request body too large")}
		return
	}

	binary.LittleEndian.PutUint32(bufBytes, uint32(reqLen))

	_, err := out.Write(buf.Bytes())
	if err != nil {
		workReq.RespChan <- workResponse{Err: fmt.Errorf("writing header failed: %w", err)}
		return
	}

	_, err = out.Write(workReq.Req.Body)
	if err != nil {
		workReq.RespChan <- workResponse{Err: fmt.Errorf("writing body failed: %w", err)}
		return
	}

	lenBuf := [4]byte{}
	_, err = in.Read(lenBuf[:])
	if err != nil {
		workReq.RespChan <- workResponse{Err: fmt.Errorf("unable to worker read response length: %w", err)}
		return
	}

	respLen := binary.LittleEndian.Uint32(lenBuf[:])
	if respLen > 0x7fffffff {
		workReq.RespChan <- workResponse{Err: fmt.Errorf("response too large")}
		return
	}

	buf.Reset()
	buf.Grow(int(respLen))

	_, err = buf.ReadFrom(&io.LimitedReader{R: in, N: int64(respLen)})
	if err != nil {
		workReq.RespChan <- workResponse{Err: fmt.Errorf("unable to read response")}
		return
	}

	br := bare.NewReader(&buf)
	// Because we are reading from a buffer, we ignore errors as there
	// should be no failures.
	//
	// If the request comes out wonky, it because of a bug in the
	// worker dispatcher writing corrupt responses, so they will
	// just get a bogus response.

	variant, _ := br.ReadUint()
	switch variant {
	case 0:
		status, _ := br.ReadUint()
		numHeaders, _ := br.ReadUint()
		headers := make(map[string][]string)
		for i := uint64(0); i < numHeaders; i++ {
			hdr, _ := br.ReadString()
			numValues, _ := br.ReadUint()
			values := []string{}
			for j := uint64(0); j < numValues; j++ {
				value, _ := br.ReadString()
				values = append(values, value)
			}
			headers[hdr] = values
		}

		body, _ := br.ReadData()

		workReq.RespChan <- workResponse{Resp: HTTPResponse{
			Status:  int(status),
			Headers: headers,
			Body:    body,
		}}
	default:
		workReq.RespChan <- workResponse{Err: fmt.Errorf("worker sent unknown response variant")}
		return
	}

	ok = true
	return
}

func (p *WorkerPool) NumWorkers() uint32 {
	return atomic.LoadUint32(&p.numWorkers)
}

func (p *WorkerPool) RemoveWorker() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.NumWorkers() <= p.cfg.MinWorkers {
		return
	}

	p.cancelWorker[len(p.cancelWorker)-1]()
	p.ctl = p.ctl[:len(p.ctl)-1]
	p.cancelWorker = p.cancelWorker[:len(p.cancelWorker)-1]
	atomic.AddUint32(&p.numWorkers, ^uint32(0)) // Decrement
}

func (p *WorkerPool) SpawnWorker() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.NumWorkers() >= p.cfg.MaxWorkers {
		return
	}

	ctx, cancelWorker := context.WithCancel(p.workerCtx)
	// These are deliberately not buffered.
	ctl := make(chan ctlRequest)
	p.ctl = append(p.ctl, ctl)
	p.cancelWorker = append(p.cancelWorker, cancelWorker)
	p.wg.Add(1)
	atomic.AddUint32(&p.numWorkers, 1)

	go func() {
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

			var workerProcessError error

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
				cmd.Stderr = p4
				cmd.ExtraFiles = []*os.File{p6}

				cmdWorkerWg.Add(1)
				go func() {
					defer cmdWorkerWg.Done()
					brdr := bufio.NewReader(p3)
					for {
						ln, err := brdr.ReadBytes('\n')
						if len(ln) != 0 {
							p.cfg.OnWorkerOutput(ln)
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
						_ = p5.Close()
					case <-cmdShuttingDown:
					}
				}()

				err = cmd.Start()
				if err != nil {
					logfn("msg", "unable to spawn worker", "err", err)
					return
				}

				workerCmdDied := make(chan struct{})
				cmdWorkerWg.Add(1)
				go func() {
					defer cmdWorkerWg.Done()
					defer close(workerCmdDied)
					workerProcessError = cmd.Wait()
				}()

				logfn("msg", "worker spawned")

				// After the command has started, we need to close our side
				// of the pipes we gave it.
				_ = p1.Close()
				_ = p4.Close()
				_ = p6.Close()

				workerHealthCheckTicker := time.NewTicker(p.cfg.WorkerHealthCheckInterval)
				defer workerHealthCheckTicker.Stop()

				for {
					select {
					case <-ctx.Done():
						_ = cmd.Process.Signal(syscall.SIGTERM)
						return
					case <-workerCmdDied:
						return
					case ctlRequest := <-ctl:
						respChan := ctlRequest.RespChan
						switch req := ctlRequest.Req.(type) {
						case restartWorkerProcRequest:
							_ = cmd.Process.Signal(syscall.SIGTERM)
							respChan <- struct{}{}
							return
						default:
							respChan <- fmt.Errorf("unknown request type: %v", req)
							return
						}
					case workReq := <-p.dispatch:
						workerRequestTimeoutTimer := time.AfterFunc(p.cfg.WorkerRequestTimeout, func() {
							logfn("msg", "janet worker request timed out, aborting request")
							_ = cmd.Process.Signal(syscall.SIGTERM)
						})
						ok := workerHandleRequest(ctx, p, workReq, p2, p5)
						timerStopped := workerRequestTimeoutTimer.Stop()
						if !ok || !timerStopped {
							logfn("msg", "worker restarting due to error")
							return
						}
					case <-workerHealthCheckTicker.C:
						// size=1 ++ variant=1.
						healthCheckRequest := []byte{1, 0, 0, 0, 1}
						_, err = p2.Write(healthCheckRequest)
						if err != nil {
							logfn("msg", "worker restarting, error requesting health check")
							return
						}
					}
				}

			}()

			cmdWorkerWg.Wait()

			if ctx.Err() == nil {
				logfn("msg", "pool worker died", "err", workerProcessError)
			} else {
				logfn("msg", "worker shutdown by request")
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(p.cfg.WorkerRestartDelay):
				atomic.AddUint64(&p.workerRestarts, 1)
			}
		}

	}()
}

func (p *WorkerPool) Dispatch(req HTTPRequest) (HTTPResponse, error) {

	atomic.StoreInt32(&p.attritionMarker, 0)

	respChan := make(chan workResponse, 1)

	workReq := workRequest{
		Req:      req,
		RespChan: respChan,
	}

	t := time.NewTimer(p.cfg.WorkerSpawnTimeout)
	select {
	case <-t.C:

		// Only bother grabbing the mutex if we know it has a chance
		// of spawning a new worker (NumWorkers does not lock).
		if p.NumWorkers() < p.cfg.MaxWorkers {
			p.SpawnWorker()
		}
		t.Reset(p.cfg.WorkerRendezvousTimeout)
		select {
		case <-t.C:
			return HTTPResponse{}, ErrWorkerPoolBusy
		case <-p.workerCtx.Done():
			t.Stop()
			return HTTPResponse{}, ErrWorkerPoolClosed
		case p.dispatch <- workReq:
			t.Stop()
		}
	case <-p.workerCtx.Done():
		t.Stop()
		return HTTPResponse{}, ErrWorkerPoolClosed
	case p.dispatch <- workReq:
		t.Stop()
	}

	select {
	case <-p.workerCtx.Done():
		return HTTPResponse{}, ErrWorkerPoolClosed
	case r := <-workReq.RespChan:
		if r.Err != nil {
			return HTTPResponse{}, fmt.Errorf("request failed: %w", r.Err)
		}
		return r.Resp, nil
	}
}

func (p *WorkerPool) RestartWorkers(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i := uint32(0); i < p.NumWorkers(); i++ {
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
	p.cancelAllWorkers()
	p.wg.Wait()
}

type HandlerConfig struct {
	Logfn           func(keyvals ...interface{})
	StaticCompress  bool
	StaticNoBrotli  bool
	StaticRoot      string
	StaticUrlPrefix string
}

func MakeHTTPHandler(pool *WorkerPool, cfg HandlerConfig) fasthttp.RequestHandler {
	if cfg.Logfn == nil {
		cfg.Logfn = func(v ...interface{}) {}
	}

	if !strings.HasSuffix(cfg.StaticUrlPrefix, "/") {
		cfg.StaticUrlPrefix += "/"
	}

	var staticFileRequestHandler fasthttp.RequestHandler
	staticUrlPrefixBytes := []byte(cfg.StaticUrlPrefix)

	if cfg.StaticRoot != "" {
		fs := &fasthttp.FS{
			Root:           cfg.StaticRoot,
			Compress:       cfg.StaticCompress,
			CompressBrotli: !cfg.StaticNoBrotli,
			CompressedFileSuffixes: map[string]string{
				"gzip": ".gz",
				"br":   ".br",
			},
			PathRewrite: fasthttp.NewPathPrefixStripper(len(staticUrlPrefixBytes) - 1),
		}
		staticFileRequestHandler = fs.NewRequestHandler()
	}

	logfn := cfg.Logfn

	return func(ctx *fasthttp.RequestCtx) {
		uri := ctx.Request.URI()

		if staticFileRequestHandler != nil {
			if bytes.HasPrefix(uri.Path(), staticUrlPrefixBytes) {
				staticFileRequestHandler(ctx)
				return
			}
		}

		reqHeaders := make(map[string]string)
		ctx.Request.Header.VisitAll(func(key, value []byte) {
			reqHeaders[string(key)] = string(value)
		})

		resp, err := pool.Dispatch(HTTPRequest{
			RemoteAddress: ctx.RemoteAddr().String(),
			Uri:           string(uri.FullURI()),
			Headers:       reqHeaders,
			Method:        string(ctx.Request.Header.Method()),
			Body:          ctx.Request.Body(),
		})
		if err != nil {
			logfn("msg", "error while dispatching to worker", "err", err)
			if err == ErrWorkerPoolBusy {
				ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
				ctx.SetBody([]byte("server overloaded\n"))
			} else {
				ctx.SetStatusCode(fasthttp.StatusInternalServerError)
				ctx.SetBody([]byte("internal server error\n"))
			}
			return
		}

		ctx.SetStatusCode(resp.Status)
		for hdr, values := range resp.Headers {
			for i, value := range values {
				if i == 0 {
					ctx.Response.Header.Set(hdr, value)
				} else {
					ctx.Response.Header.Add(hdr, value)
				}
			}
		}
		ctx.SetBody(resp.Body)
	}
}
