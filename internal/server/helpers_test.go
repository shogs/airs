package server

import (
	"net/http"
	"net/http/httptest"
)

func httptestRequest(path string) *http.Request {
	r := httptest.NewRequest("GET", path, nil)
	r.RemoteAddr = remote
	return r
}

func newRecorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }
