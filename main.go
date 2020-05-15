package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

type Backend struct {
	sync.RWMutex
	URL          *url.URL
	Alive        bool
	ReverseProxy *httputil.ReverseProxy
}

type ServerPool struct {
	backends []*Backend
	current  uint64
}

var serverPool ServerPool

func (s *ServerPool) NextIndex() int {
	return int(atomic.AddUint64(&s.current, uint64(1)) % uint64(len(s.backends)))
}

func (s *ServerPool) GetNextPeer() *Backend {
	next := s.NextIndex()
	l := next + len(s.backends)
	for i := next; i < l; i++ {
		idx := i % len(s.backends)
		if s.backends[idx].IsAlive() {
			// if we have an alive backend, use it and store if its not the original one
			if i != next {
				atomic.StoreUint64(&s.current, uint64(idx)) // mark the current one
			}
			return s.backends[idx]
		}
	}
	return nil
}

func (b *Backend) IsAlive() (alive bool) {
	b.RLock()
	alive = b.Alive
	b.RUnlock()
	return
}

func (b *Backend) SetAlive(alive bool) {
	b.RLock()
	b.Alive = alive
	b.RUnlock()
	return
}

func LoadBalance(w http.ResponseWriter, r *http.Request) {
	peer := serverPool.GetNextPeer()
	if peer != nil {
		peer.ReverseProxy.ServeHTTP(w, r)
		return
	}
	http.Error(w, "Service not avaliable", http.StatusServiceUnavailable)
}

func main() {
	u, _ := url.Parse("https://localhost:8000")
	proxy := httputil.NewSingleHostReverseProxy(u)
	port := 9
	server := http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: http.HandlerFunc(LoadBalance),
	}

	proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, e error) {
		log.Printf("[%s] %s\n", serverUrl.Host, e.Error())
		retries := GetRetryFromContext(request)
		if retries < 3 {
			select {
			case <-time.After(10 * time.Millisecond):
				ctx := context.WithValue(request.Context(), Retry, retries+1)
				proxy.ServeHTTP(writer, request.WithContext(ctx))
			}
			return
		}

		// after 3 retries, mark this backend as down
		serverPool.MarkBackendStatus(serverUrl, false)

		// if the same request routing for few attempts with different backends, increase the count
		attempts := GetAttemptsFromContext(request)
		log.Printf("%s(%s) Attempting retry %d\n", request.RemoteAddr, request.URL.Path, attempts)
		ctx := context.WithValue(request.Context(), Attempts, attempts+1)
		lb(writer, request.WithContext(ctx))
	}

	http.HandlerFunc(proxy.ServeHTTP)
}
