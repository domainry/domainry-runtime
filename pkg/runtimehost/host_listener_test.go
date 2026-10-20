package runtimehost

import (
	"errors"
	"net"
	"net/http"
	"testing"
)

type listenerProbe struct{ closed bool }

func (*listenerProbe) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (listener *listenerProbe) Close() error {
	listener.closed = true
	return nil
}
func (*listenerProbe) Addr() net.Addr { return addressProbe("probe") }

type addressProbe string

func (address addressProbe) Network() string { return string(address) }
func (address addressProbe) String() string  { return string(address) }

func TestBindHTTPListenersClosesPartialStartupBeforeStarted(t *testing.T) {
	first := &listenerProbe{}
	wantErr := errors.New("address already in use")
	calls := 0
	listeners, err := bindHTTPListeners([]*http.Server{{Addr: "one"}, {Addr: "two"}}, func(_, _ string) (net.Listener, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) || listeners != nil || !first.closed {
		t.Fatalf("listeners=%v closed=%v err=%v", listeners, first.closed, err)
	}
}
