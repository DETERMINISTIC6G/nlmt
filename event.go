package nlmt

import (
	"fmt"
	"net"
)

// Code uniquely identifies events and errors to improve context.
type Code int

// IsError returns true if the code for an error (negative).
func (c Code) IsError() bool {
	return c < 0
}

//go:generate stringer -type=Code

// Server event codes.
const (
	MultipleAddresses Code = iota + 1*1024
	ServerStart
	ServerStop
	ListenerStart
	ListenerStop
	ListenerError
	Drop
	NewConn
	OpenClose
	CloseConn
	NoDSCPSupport
	ExceededDuration
	NoReceiveDstAddrSupport
	RemoveNoConn
	InvalidServerFill
)

// Client event codes.
const (
	Connecting Code = iota + 2*1024
	Connected
	WaitForPackets
	ServerRestriction
	NoTest
	ConnectedClosed
)

// Event is an event sent to a Handler.
type Event struct {
	Code       Code
	LocalAddr  *net.UDPAddr
	RemoteAddr *net.UDPAddr
	format     string
	Detail     []interface{}
}

// Eventf returns a new event.
func Eventf(code Code, laddr *net.UDPAddr, raddr *net.UDPAddr, format string,
	detail ...interface{}) *Event {
	return &Event{code, laddr, raddr, format, detail}
}

// IsError returns true if the event is an error (its code is negative).
func (e *Event) IsError() bool {
	return e.Code.IsError()
}

func (e *Event) String() string {
	msg := fmt.Sprintf(e.format, e.Detail...)
	if e.RemoteAddr != nil {
		return fmt.Sprintf("[%s] [%s] %s", e.RemoteAddr, e.Code.String(), msg)
	}
	return fmt.Sprintf("[%s] %s", e.Code.String(), msg)
}

// Handler is called with events.
type Handler interface {
	// OnEvent is called when an event occurs.
	OnEvent(e *Event)
}

// MultiHandler calls multiple event handlers.
type MultiHandler struct {
	Handlers []Handler
}

// AddHandler adds a handler.
func (m *MultiHandler) AddHandler(h Handler) {
	m.Handlers = append(m.Handlers, h)
}

// OnEvent calls all event handlers.
func (m *MultiHandler) OnEvent(e *Event) {
	for _, h := range m.Handlers {
		h.OnEvent(e)
	}
}
