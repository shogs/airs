package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
)

// httptestRequest is a LAN request that arrived on netA's address.
func httptestRequest(path string) *http.Request {
	r := httptest.NewRequest("GET", path, nil)
	r.RemoteAddr = remote
	r.Host = netA.LocalIP + ":7777"
	la := &net.TCPAddr{IP: net.ParseIP(netA.LocalIP), Port: 7777}
	return r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, la))
}

func newRecorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }
