package poolparty

import (
	"context"
	"sync"

	"github.com/coolmsg/go-coolmsg"
)

const (
	TYPE_RESTART_JANET_WORKERS = 0x8b7a7f45627b6e31
)

type RestartJanetWorkersMsg struct {
}

func (m *RestartJanetWorkersMsg) CoolMsg_TypeId() uint64  { return TYPE_RESTART_JANET_WORKERS }
func (m *RestartJanetWorkersMsg) CoolMsg_Marshal() []byte { return coolmsg.MsgpackMarshal(m) }
func (m *RestartJanetWorkersMsg) CoolMsg_Unmarshal(buf []byte) bool {
	return coolmsg.MsgpackUnmarshal(buf, m)
}

func init() {
	coolmsg.RegisterMessage(TYPE_RESTART_JANET_WORKERS, func() coolmsg.Message { return &RestartJanetWorkersMsg{} })
}

type RootCtlObject struct {
	m    sync.Mutex
	Pool *WorkerPool
}

func (r *RootCtlObject) Message(ctx context.Context, cs *coolmsg.ConnServer, m coolmsg.Message, respond coolmsg.RespondFunc) {
	r.m.Lock() // Super coarse locking, we don't need more for now.
	defer r.m.Unlock()

	switch m.(type) {
	case *RestartJanetWorkersMsg:
		err := r.Pool.RestartWorkers(ctx)
		if err != nil {
			respond(&coolmsg.Error{Code: coolmsg.ERRCODE_GENERIC, Display: err.Error()})
		} else {
			respond(&coolmsg.Ok{})
		}
	default:
		respond(coolmsg.ErrUnexpectedMessage)
	}
}

func (r *RootCtlObject) UnknownMessage(ctx context.Context, cs *coolmsg.ConnServer, t uint64, buf []byte, respond coolmsg.RespondFunc) {
	respond(coolmsg.ErrUnexpectedMessage)
}

// Clunk is the cleanup method of an object, the name Clunk comes from the 9p protocol.
// An object is clunked when a server is done with it.
func (r *RootCtlObject) Clunk(cs *coolmsg.ConnServer) {
}
