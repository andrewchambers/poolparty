package poolparty

import (
	"context"

	"git.sr.ht/~sircmpwn/go-bare"
	"github.com/andrewchambers/srop"
)

const (
	TYPE_RESTART_WORKERS = 0x8b7a7f45627b6e31
	TYPE_SPAWN_WORKER    = 0xd4ca8af3015c5c7b
	TYPE_REMOVE_WORKER   = 0xa27e5012e8c0a4b0
	TYPE_WORKER_COUNT    = 0xfa945ca096abf1ad
	TYPE_CTL_ERROR       = 0x9a5eec3c075dcd01
)

type RestartWorkersMsg struct {
}

func (m *RestartWorkersMsg) SropType() uint64    { return TYPE_RESTART_WORKERS }
func (m *RestartWorkersMsg) SropMarshal() []byte { return []byte{} }
func (m *RestartWorkersMsg) SropUnmarshal(buf []byte) bool {
	return true
}

type CtlError struct {
	Msg string
}

func marshal(m interface{}) []byte {
	buf, err := bare.Marshal(m)
	if err != nil {
		panic(err)
	}
	return buf
}

func (m *CtlError) SropType() uint64    { return TYPE_RESTART_WORKERS }
func (m *CtlError) SropMarshal() []byte { return marshal(m) }
func (m *CtlError) SropUnmarshal(buf []byte) bool {
	return bare.Unmarshal(buf, m) == nil
}

type SpawnWorkerMsg struct {
}

func (m *SpawnWorkerMsg) SropType() uint64    { return TYPE_SPAWN_WORKER }
func (m *SpawnWorkerMsg) SropMarshal() []byte { return marshal(m) }
func (m *SpawnWorkerMsg) SropUnmarshal(buf []byte) bool {
	return bare.Unmarshal(buf, m) == nil
}

type RemoveWorkerMsg struct {
}

func (m *RemoveWorkerMsg) SropType() uint64    { return TYPE_REMOVE_WORKER }
func (m *RemoveWorkerMsg) SropMarshal() []byte { return marshal(m) }
func (m *RemoveWorkerMsg) SropUnmarshal(buf []byte) bool {
	return bare.Unmarshal(buf, m) == nil
}

type WorkerCountMsg struct {
	Count *uint
}

func (m *WorkerCountMsg) SropType() uint64    { return TYPE_WORKER_COUNT }
func (m *WorkerCountMsg) SropMarshal() []byte { return marshal(m) }
func (m *WorkerCountMsg) SropUnmarshal(buf []byte) bool {
	return bare.Unmarshal(buf, m) == nil
}

func init() {
	srop.RegisterMessage(TYPE_RESTART_WORKERS, func() srop.Message { return &RestartWorkersMsg{} })
	srop.RegisterMessage(TYPE_SPAWN_WORKER, func() srop.Message { return &SpawnWorkerMsg{} })
	srop.RegisterMessage(TYPE_REMOVE_WORKER, func() srop.Message { return &RemoveWorkerMsg{} })
	srop.RegisterMessage(TYPE_WORKER_COUNT, func() srop.Message { return &WorkerCountMsg{} })
	srop.RegisterMessage(TYPE_CTL_ERROR, func() srop.Message { return &CtlError{} })
}

type RootCtlObject struct {
	Pool *WorkerPool
}

func (r *RootCtlObject) Message(ctx context.Context, cs *srop.ConnServer, m srop.Message, respond srop.RespondFunc) {

	switch m.(type) {
	case *RestartWorkersMsg:
		err := r.Pool.RestartWorkers(ctx)
		if err != nil {
			respond(&CtlError{Msg: err.Error()})
		} else {
			respond(&srop.Ok{})
		}
	case *SpawnWorkerMsg:
		r.Pool.SpawnWorker()
		respond(&srop.Ok{})
	case *RemoveWorkerMsg:
		r.Pool.RemoveWorker()
		respond(&srop.Ok{})
	case *WorkerCountMsg:
		count := uint(r.Pool.WorkerCount())
		respond(&WorkerCountMsg{Count: &count})
	default:
		respond(&srop.UnexpectedMessage{})
	}
}

func (r *RootCtlObject) UnknownMessage(ctx context.Context, cs *srop.ConnServer, t uint64, buf []byte, respond srop.RespondFunc) {
	respond(&srop.UnexpectedMessage{})
}

func (r *RootCtlObject) Clunk(cs *srop.ConnServer) {}
