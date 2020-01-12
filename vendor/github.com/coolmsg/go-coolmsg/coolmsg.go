package coolmsg

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/vmihailenco/msgpack"
	"golang.org/x/sync/semaphore"
)

const (
	// From spec
	TYPE_ERR = 0x81aba3f7522edc6b
	// From spec
	TYPE_OK = 0xd4924862b91c639d
	// From spec
	TYPE_CLUNK = 0xcf3a50d623ee637d
	// From spec
	TYPE_OBJECT_REF = 0xd782cf4b395eca05

	// From spec
	ERRCODE_OBJECT_NOT_EXIST = 0xab0547366de885bc
	// From spec
	ERRCODE_UNEXPECTED_MESSAGE = 0xd47d4e94917934b2
	ERRCODE_GENERIC            = 0

	RESERVED_OBJECT_ID  = 0
	BOOTSTRAP_OBJECT_ID = 1
)

var (
	ErrBadResponse        = errors.New("bad response.")
	ErrPayloadTooBig      = errors.New("payload too big.")
	ErrRequestCancelled   = errors.New("request cancelled.")
	ErrClientShutdown     = errors.New("client has been shutdown.")
	ErrObjectDoesNotExist = &Error{Code: ERRCODE_OBJECT_NOT_EXIST, Display: "object does not exist."}
	ErrUnexpectedMessage  = &Error{Code: ERRCODE_UNEXPECTED_MESSAGE, Display: "unexpected message."}
)

type Request struct {
	RequestId   uint64
	ObjectId    uint64
	MessageType uint64
	// Modifying this buffer is an error.
	MessageData []byte
}

type RespondFunc func(Message)

type Response struct {
	RequestId    uint64
	ResponseType uint64
	// Modifying this buffer is an error.
	ResponseData []byte
}

type Object interface {
	Message(context.Context, *ConnServer, Message, RespondFunc)
	UnknownMessage(context.Context, *ConnServer, uint64, []byte, RespondFunc)
	Clunk(*ConnServer)
}

type ServerOptions struct {
	ConnOptions ConnServerOptions
}

type ConnServerOptions struct {
	MaxRequestSize uint64
	// Each  will stop reading new
	// requests if this is exceeded, zero
	// means unlimited.
	MaxOutstandingRequests uint64
	// This function is used to create the root object
	// a client can send messages to.
	//
	// It takes a connection object as a way for out of band connection
	// information to be used while creating the root object.
	// One example is using a unix socket to get the remote user id,
	// and then using that for authentication.
	BootstrapFunc func(io.ReadWriteCloser) Object
	// If nil, defaults to DefaultRegistry
	Registry *Registry
}

type Server struct {
	options   ServerOptions
	connCtx   context.Context
	cancelCtx func()
	wg        sync.WaitGroup
}

type ConnServer struct {
	lock sync.Mutex

	options       ConnServerOptions
	objects       map[uint64]Object
	objectCounter uint64
	requestSem    *semaphore.Weighted
	wg            sync.WaitGroup
}

type ClientOptions struct {
	MaxResponseSize uint64
	// If nil, defaults to DefaultRegistry
	Registry *Registry
}

type Client struct {
	options ClientOptions

	conn          io.ReadWriteCloser
	workerContext context.Context
	shutdown      func()
	wg            sync.WaitGroup
	outbound      chan Request

	requestsLock   sync.Mutex
	requests       map[uint64]chan Response
	requestCounter uint64
}

type Message interface {
	CoolMsg_TypeId() uint64
	CoolMsg_Marshal() []byte
	// The buffer is guaranteed to be read only.
	// This means zero copy references into the
	// buffer are okay (and encouraged).
	CoolMsg_Unmarshal([]byte) bool
}

type Registry struct {
	mkFuncs     map[uint64]func() Message
	errorLookup map[uint64]func(*Error) error
}

func NewServer(options ServerOptions) *Server {

	ctx, cancelCtx := context.WithCancel(context.Background())

	return &Server{
		connCtx:   ctx,
		cancelCtx: cancelCtx,
		options:   options,
	}
}

func (s *Server) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		s.GoHandle(c)
	}
}

func (s *Server) GoHandle(c io.ReadWriteCloser) {

	select {
	case <-s.connCtx.Done():
		c.Close()
		return
	default:
	}

	sc := NewConnServer(c, s.options.ConnOptions)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		sc.Serve(s.connCtx, c)
	}()
}

func (s *Server) Wait() {
	s.wg.Wait()
}

func (s *Server) Close() {
	s.cancelCtx()
}

func NewConnServer(c io.ReadWriteCloser, options ConnServerOptions) *ConnServer {

	if options.Registry == nil {
		options.Registry = DefaultRegistry
	}

	var requestSem *semaphore.Weighted
	if options.MaxOutstandingRequests != 0 {
		requestSem = semaphore.NewWeighted(int64(options.MaxOutstandingRequests))
	}
	s := &ConnServer{
		options:       options,
		objects:       make(map[uint64]Object),
		objectCounter: 2, // 0, 1 are never used, it is reserved for bootstrap.

		requestSem: requestSem,
	}

	if options.BootstrapFunc != nil {
		s.RegisterBootstrap(options.BootstrapFunc(c))
	}

	return s
}

func (s *ConnServer) Register(o Object) uint64 {
	s.lock.Lock()
	defer s.lock.Unlock()

	oid := s.objectCounter
	s.objectCounter += 1
	s.objects[oid] = o
	return oid
}

func (s *ConnServer) RegisterBootstrap(o Object) {
	s.lock.Lock()
	old, hadOld := s.objects[BOOTSTRAP_OBJECT_ID]
	s.objects[BOOTSTRAP_OBJECT_ID] = o
	s.lock.Unlock()
	if hadOld {
		old.Clunk(s)
	}
}

func (s *ConnServer) Clunk(oid uint64) {
	s.lock.Lock()

	obj, ok := s.objects[oid]
	if !ok {
		s.lock.Unlock()
		return
	}

	delete(s.objects, oid)
	s.lock.Unlock()

	obj.Clunk(s)
}

func (s *ConnServer) Go(f func()) {
	s.wg.Add(1)
	go func(s *ConnServer) {
		defer s.wg.Done()
		f()
	}(s)
}

func (s *ConnServer) HandleRequest(ctx context.Context, r Request, respond RespondFunc) {
	s.lock.Lock()
	obj, objOk := s.objects[r.ObjectId]
	s.lock.Unlock()

	if !objOk {
		respond(ErrObjectDoesNotExist)
		return
	}

	m, ok := s.options.Registry.Unmarshal(r.MessageType, r.MessageData)
	if ok {
		obj.Message(ctx, s, m, respond)
	} else {
		obj.UnknownMessage(ctx, s, r.MessageType, r.MessageData, respond)
	}

}

func (s *ConnServer) Serve(ctx context.Context, c io.ReadWriteCloser) {

	ctx, cancelCtx := context.WithCancel(ctx)

	shutdown := func() {
		cancelCtx()
		_ = c.Close()
	}

	outbound := make(chan Response, 16)

	s.Go(func() {
		defer shutdown()

		for {
			if s.options.MaxOutstandingRequests != 0 {
				err := s.requestSem.Acquire(ctx, 1)
				if err != nil {
					return
				}
			}

			req, err := ReadRequest(c, s.options.MaxRequestSize)
			if err != nil {
				break
			}
			id := req.RequestId
			respond := func(m Message) {
				if s.options.MaxOutstandingRequests != 0 {
					s.requestSem.Release(1)
				}
				select {
				case <-ctx.Done():
					return
				case outbound <- Response{RequestId: id, ResponseType: m.CoolMsg_TypeId(), ResponseData: m.CoolMsg_Marshal()}:
				}
			}

			s.HandleRequest(ctx, req, respond)
		}
	})

	s.Go(func() {
		defer shutdown()

		for {
			select {
			case <-ctx.Done():
				return
			case response := <-outbound:
				err := WriteResponse(c, response)
				if err != nil {
					break
				}
			}
		}
	})

	<-ctx.Done()
	shutdown()

	s.lock.Lock()
	for _, o := range s.objects {
		o.Clunk(s)
	}
	s.objects = make(map[uint64]Object)
	s.lock.Unlock()

	s.Wait()
}

func (s *ConnServer) Wait() {
	s.wg.Wait()
}

func WriteRequest(w io.Writer, req Request) error {
	header := [32]byte{}

	binary.BigEndian.PutUint64(header[0:8], req.RequestId)
	binary.BigEndian.PutUint64(header[8:16], req.ObjectId)
	binary.BigEndian.PutUint64(header[16:24], req.MessageType)
	binary.BigEndian.PutUint64(header[24:32], uint64(len(req.MessageData)))

	_, err := w.Write(header[:])
	if err != nil {
		return err
	}

	_, err = w.Write(req.MessageData)
	if err != nil {
		return err
	}

	return nil
}

func ReadRequest(r io.Reader, maxRequestSize uint64) (Request, error) {
	header := [32]byte{}

	_, err := io.ReadFull(r, header[:])
	if err != nil {
		return Request{}, err
	}

	req := Request{}

	req.RequestId = binary.BigEndian.Uint64(header[0:8])
	req.ObjectId = binary.BigEndian.Uint64(header[8:16])
	req.MessageType = binary.BigEndian.Uint64(header[16:24])
	paramLen := binary.BigEndian.Uint64(header[24:32])
	if maxRequestSize != 0 && paramLen > maxRequestSize {
		return Request{}, ErrPayloadTooBig
	}
	paramData := make([]byte, paramLen)

	_, err = io.ReadFull(r, paramData)
	if err != nil {
		return Request{}, err
	}

	req.MessageData = paramData

	return req, nil
}

func WriteResponse(w io.Writer, resp Response) error {
	header := [24]byte{}

	binary.BigEndian.PutUint64(header[0:8], resp.RequestId)
	binary.BigEndian.PutUint64(header[8:16], resp.ResponseType)
	binary.BigEndian.PutUint64(header[16:24], uint64(len(resp.ResponseData)))

	_, err := w.Write(header[:])
	if err != nil {
		return err
	}

	_, err = w.Write(resp.ResponseData)
	if err != nil {
		return err
	}

	return nil
}

func ReadResponse(r io.Reader, maxResponseSize uint64) (Response, error) {
	header := [24]byte{}

	_, err := io.ReadFull(r, header[:])
	if err != nil {
		return Response{}, err
	}

	resp := Response{}

	resp.RequestId = binary.BigEndian.Uint64(header[0:8])
	resp.ResponseType = binary.BigEndian.Uint64(header[8:16])
	responseLen := binary.BigEndian.Uint64(header[16:24])
	if maxResponseSize != 0 && responseLen > maxResponseSize {
		return Response{}, ErrPayloadTooBig
	}
	responseData := make([]byte, responseLen)

	_, err = io.ReadFull(r, responseData)
	if err != nil {
		return Response{}, err
	}

	resp.ResponseData = responseData

	return resp, nil
}

func NewClient(conn io.ReadWriteCloser, options ClientOptions) *Client {
	workerCtx, cancelWorkers := context.WithCancel(context.Background())

	shutdown := func() {
		conn.Close()
		cancelWorkers()
	}

	if options.Registry == nil {
		options.Registry = DefaultRegistry
	}

	c := &Client{
		options:       options,
		conn:          conn,
		workerContext: workerCtx,
		shutdown:      shutdown,
		requests:      make(map[uint64]chan Response),
		outbound:      make(chan Request),
	}

	c.wg.Add(1)
	go func() {
		defer shutdown()
		defer c.wg.Done()

		for {
			select {
			case <-workerCtx.Done():
				return
			case req := <-c.outbound:
				err := WriteRequest(conn, req)
				if err != nil {
					return
				}
			}
		}
	}()

	c.wg.Add(1)
	go func() {
		defer shutdown()
		defer c.wg.Done()

		for {
			resp, err := ReadResponse(conn, options.MaxResponseSize)
			if err != nil {
				return
			}

			c.requestsLock.Lock()
			rChan, ok := c.requests[resp.RequestId]
			if ok {
				delete(c.requests, resp.RequestId)
			}
			c.requestsLock.Unlock()

			if !ok {
				return
			}

			select {
			case <-workerCtx.Done():
				return
			case rChan <- resp:
			}
		}
	}()

	return c
}

func (c *Client) Send(oid uint64, arg Message) (interface{}, error) {
	return c.SendCtx(context.Background(), oid, arg)
}

func (c *Client) SendWithReg(reg *Registry, oid uint64, arg Message) (interface{}, error) {
	return c.SendWithRegCtx(context.Background(), reg, oid, arg)
}

func (c *Client) RawSendParsedReply(reg *Registry, oid uint64, paramType uint64, paramData []byte) (interface{}, error) {
	return c.RawSendParsedReplyCtx(context.Background(), reg, oid, paramType, paramData)
}

func (c *Client) RawSend(oid uint64, paramType uint64, paramData []byte) (Response, error) {
	return c.RawSendCtx(context.Background(), oid, paramType, paramData)
}

func (c *Client) SendCtx(ctx context.Context, oid uint64, arg Message) (interface{}, error) {
	return c.SendWithRegCtx(ctx, c.options.Registry, oid, arg)
}

func (c *Client) SendWithRegCtx(ctx context.Context, reg *Registry, oid uint64, arg Message) (interface{}, error) {
	m, err := c.RawSendParsedReplyCtx(ctx, reg, oid, arg.CoolMsg_TypeId(), arg.CoolMsg_Marshal())
	if err != nil {
		return nil, err
	}

	return m, nil
}

func (c *Client) RawSendParsedReplyCtx(ctx context.Context, reg *Registry, oid uint64, paramType uint64, paramData []byte) (interface{}, error) {
	resp, err := c.RawSendCtx(ctx, oid, paramType, paramData)
	if err != nil {
		return nil, err
	}

	m, ok := reg.Unmarshal(resp.ResponseType, resp.ResponseData)
	if !ok {
		return nil, ErrBadResponse
	}

	if rpcErr, isErr := m.(*Error); isErr {
		return nil, reg.MakeError(rpcErr)
	}

	return m, nil
}

func (c *Client) RawSendCtx(ctx context.Context, oid uint64, paramType uint64, paramData []byte) (Response, error) {
	rChan := make(chan Response, 1)
	c.requestsLock.Lock()
	reqId := c.requestCounter
	c.requestCounter += 1
	c.requests[reqId] = rChan
	c.requestsLock.Unlock()

	select {
	case <-c.workerContext.Done():
		return Response{}, ErrClientShutdown
	case c.outbound <- Request{
		RequestId:   reqId,
		ObjectId:    oid,
		MessageType: paramType,
		MessageData: paramData,
	}:
	}

	select {
	case <-ctx.Done():
		c.requestsLock.Lock()
		delete(c.requests, reqId)
		c.requestsLock.Unlock()

		return Response{}, ErrRequestCancelled
	case <-c.workerContext.Done():
		return Response{}, ErrClientShutdown
	case response := <-rChan:
		return response, nil
	}

}

func (c *Client) Close() {
	c.shutdown()
	c.wg.Wait()
}

func NewRegistry() *Registry {
	return &Registry{
		mkFuncs:     make(map[uint64]func() Message),
		errorLookup: make(map[uint64]func(*Error) error),
	}
}

func (reg *Registry) RegisterMessage(id uint64, mk func() Message) {
	_, has := reg.mkFuncs[id]
	if has {
		panic(fmt.Sprintf("duplicate id: %x", id))
	}
	reg.mkFuncs[id] = mk
}

func (reg *Registry) RegisterError(code uint64, mk func(e *Error) error) {
	_, has := reg.errorLookup[code]
	if has {
		panic(fmt.Sprintf("duplicate error code: %x", code))
	}
	reg.errorLookup[code] = mk
}

func (reg *Registry) Unmarshal(id uint64, data []byte) (Message, bool) {
	mk, has := reg.mkFuncs[id]
	if !has {
		return nil, false
	}

	v := mk()

	ok := v.CoolMsg_Unmarshal(data)
	if !ok {
		return nil, false
	}

	return v, true
}

func (reg *Registry) MakeError(e *Error) error {
	f, ok := reg.errorLookup[e.Code]
	if ok {
		err := f(e)
		if err != nil {
			return err
		}
	}

	return errors.New(e.Display)
}

var DefaultRegistry *Registry

func RegisterMessage(id uint64, mk func() Message) {
	DefaultRegistry.RegisterMessage(id, mk)
}

func RegisterError(code uint64, mk func(e *Error) error) {
	DefaultRegistry.RegisterError(code, mk)
}

func RegisterStandardMessagesAndErrors(reg *Registry) {
	reg.RegisterMessage(TYPE_ERR, func() Message { return &Error{} })
	reg.RegisterMessage(TYPE_OK, func() Message { return &Ok{} })
	reg.RegisterMessage(TYPE_OBJECT_REF, func() Message { return &ObjectRef{} })
	reg.RegisterMessage(TYPE_CLUNK, func() Message { return &Clunk{} })
	reg.RegisterError(ERRCODE_OBJECT_NOT_EXIST, func(*Error) error { return ErrObjectDoesNotExist })
	reg.RegisterError(ERRCODE_UNEXPECTED_MESSAGE, func(*Error) error { return ErrUnexpectedMessage })
}

func JsonUnmarshal(buf []byte, v interface{}) bool {
	err := json.Unmarshal(buf, v)
	if err != nil {
		return false
	}

	return true
}

func JsonMarshal(v interface{}) []byte {
	buf, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}

	return buf
}

func MsgpackUnmarshal(buf []byte, v interface{}) bool {
	err := msgpack.Unmarshal(buf, v)
	if err != nil {
		return false
	}

	return true
}

func MsgpackMarshal(v interface{}) []byte {
	buf, err := msgpack.Marshal(v)
	if err != nil {
		panic(err)
	}

	return buf
}

func MsgpackMarshalAsArray(v interface{}) []byte {
	var buf bytes.Buffer
	encoder := msgpack.NewEncoder(&buf)
	encoder.StructAsArray(true)
	err := encoder.Encode(v)
	if err != nil {
		panic(err)
	}

	return buf.Bytes()
}

func init() {
	DefaultRegistry = NewRegistry()
	RegisterStandardMessagesAndErrors(DefaultRegistry)
}

// Fields must remain compatible.
// with spec, Order is significant as
// Error is marshaled as an array.
type Error struct {
	Code    uint64
	Display string
	Debug   string
}

func (m *Error) CoolMsg_TypeId() uint64            { return TYPE_ERR }
func (m *Error) CoolMsg_Marshal() []byte           { return MsgpackMarshalAsArray(m) }
func (m *Error) CoolMsg_Unmarshal(buf []byte) bool { return MsgpackUnmarshal(buf, m) }
func (m *Error) Error() string                     { return m.Display }

type Ok struct{}

func (m *Ok) CoolMsg_TypeId() uint64            { return TYPE_OK }
func (m *Ok) CoolMsg_Marshal() []byte           { return []byte{} }
func (m *Ok) CoolMsg_Unmarshal(buf []byte) bool { return true }

type Clunk struct{}

func (m *Clunk) CoolMsg_TypeId() uint64            { return TYPE_CLUNK }
func (m *Clunk) CoolMsg_Marshal() []byte           { return []byte{} }
func (m *Clunk) CoolMsg_Unmarshal(buf []byte) bool { return true }

type ObjectRef struct {
	Id uint64
}

func (m *ObjectRef) CoolMsg_TypeId() uint64            { return TYPE_OBJECT_REF }
func (m *ObjectRef) CoolMsg_Marshal() []byte           { return MsgpackMarshal(&m.Id) }
func (m *ObjectRef) CoolMsg_Unmarshal(buf []byte) bool { return MsgpackUnmarshal(buf, &m.Id) }
