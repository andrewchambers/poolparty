package poolparty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/inconshreveable/log15"
)

type PoolConfig struct {
	Logger     log15.Logger
	DevMode    bool
	NumWorkers int
	WorkerProc []string
}

// XXX would be much better if these were
// not strings, we probably need msgpack or
// raw json encoders/decoders for that.
type JanetRequest struct {
	Headers string
	Body    string
}

type JanetResponse struct {
	Status  int
	Headers []string
	Body    string
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
		cfg.Logger = log15.New()
	}
	if cfg.NumWorkers < 0 {
		return nil, errors.New("pool needs at least one worker")
	}
	if len(cfg.WorkerProc) <= 0 {
		return nil, errors.New("pool worker proc must not be empty")
	}
	if cfg.DevMode {
		cfg.NumWorkers = 1
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

				if len(p.cfg.WorkerProc) > 1 {
					// TODO we should use SIGTERM instead of SIGKILL so CommandContext
					// shouldn't be used...
					cmd = exec.CommandContext(ctx, p.cfg.WorkerProc[0], p.cfg.WorkerProc[1:]...)
				} else {
					cmd = exec.CommandContext(ctx, p.cfg.WorkerProc[0])
				}
				
				logger.Info("launching worker command", "cmd", cmd)

				cmd.Stdin = p1
				cmd.Stdout = p4
				cmd.Stderr = os.Stderr
				// XXX cmd.Stderr should be logged...
				// XXX It might be wise to pass the output
				// via fd 3 and fd 4, this means accidental
				// prints to stdout/stderr won't mess with
				// our protocol.

				err = cmd.Start()
				if err != nil {
					logger.Error("unable to spawn worker", "err", err)
					return
				}

				// After the command has started, we need to close our side
				// of the pipes we gave it.
				_ = p1.Close()
				_ = p4.Close()

				encoder := json.NewEncoder(p2)
				decoder := json.NewDecoder(p3)

				for {
					var workReq workRequest

					select {
					case <-p.workerCtx.Done():
						return
					case workReq = <-p.dispatch:
					}

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

					var resp JanetResponse
					err = decoder.Decode(&resp)
					if err != nil {
						select {
						case <-p.workerCtx.Done():
							return
						case workReq.RespChan <- workResponse{Err: fmt.Errorf("decoding worker process response: %w", err)}:
							logger.Error("decoding response failed", "err", err)
							return
						}
					}

					select {
					case <-p.workerCtx.Done():
						return
					case workReq.RespChan <- workResponse{Resp: resp}:
					}

				}

			}()

			// Ensure child is gone before we try again.
			var err error

			if cmd != nil {
				err = cmd.Wait()
			}

			// In dev mode we exit on purpose to reload the code.
			if err != nil {
				logger.Error("pool worker died", "err", err)
			}
			select {
			case <-p.workerCtx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}

	}(p.workerCtx)
}

func (p *WorkerPool) Dispatch(req JanetRequest) (JanetResponse, error) {

	respChan := make(chan workResponse)

	workReq := workRequest{
		Req:      req,
		RespChan: respChan,
	}

	select {
	case <-p.workerCtx.Done():
		return JanetResponse{}, fmt.Errorf("worker pool closed")
	case p.dispatch <- workReq:
	}

	select {
	case <-p.workerCtx.Done():
		return JanetResponse{}, fmt.Errorf("worker pool closed")
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
