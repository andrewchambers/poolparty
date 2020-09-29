package poolparty

import (
	"context"
	"sync"

	"git.sr.ht/~sircmpwn/go-bare"
	"github.com/andrewchambers/srop"
)

const (
	TYPE_RESTART_WORKERS = 0x8b7a7f45627b6e31
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

func (m *CtlError) SropType() uint64 { return TYPE_RESTART_WORKERS }
func (m *CtlError) SropMarshal() []byte {
	buf, err := bare.Marshal(m)
	if err != nil {
		panic(err)
	}
	return buf
}
func (m *CtlError) SropUnmarshal(buf []byte) bool {
	return bare.Unmarshal(buf, m) == nil
}

func init() {
	srop.RegisterMessage(TYPE_RESTART_WORKERS, func() srop.Message { return &RestartWorkersMsg{} })
	srop.RegisterMessage(TYPE_CTL_ERROR, func() srop.Message { return &CtlError{} })
}

type RootCtlObject struct {
	m    sync.Mutex
	Pool *WorkerPool
}

func (r *RootCtlObject) Message(ctx context.Context, cs *srop.ConnServer, m srop.Message, respond srop.RespondFunc) {
	r.m.Lock() // Super coarse locking, we don't need more for now.
	defer r.m.Unlock()

	switch m.(type) {
	case *RestartWorkersMsg:
		err := r.Pool.RestartWorkers(ctx)
		if err != nil {
			respond(&CtlError{Msg: err.Error()})
		} else {
			respond(&srop.Ok{})
		}
	default:
		respond(&srop.UnexpectedMessage{})
	}
}

func (r *RootCtlObject) UnknownMessage(ctx context.Context, cs *srop.ConnServer, t uint64, buf []byte, respond srop.RespondFunc) {
	respond(&srop.UnexpectedMessage{})
}

// Clunk is the cleanup method of an object, the name Clunk comes from the 9p protocol.
// An object is clunked when a server is done with it.
func (r *RootCtlObject) Clunk(cs *srop.ConnServer) {
}
