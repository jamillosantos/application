package application

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

type httpService struct {
	s *http.Server
}

func (h *httpService) Name() string {
	return "http"
}

func (h *httpService) Listen(ctx context.Context) error {
	h.s = &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("hello"))
		}),
		Addr: ":8080",
	}
	return h.s.ListenAndServe()
}

func (h *httpService) Close(ctx context.Context) error {
	return h.s.Close()
}

type longToGetReadyService struct {
	cancelFunc     context.CancelFunc
	listenDuration time.Duration
	ready          bool
	readyM         sync.Mutex
}

func (s *longToGetReadyService) Name() string {
	return "Long to get Ready"
}

func (s *longToGetReadyService) Listen(ctx context.Context) error {
	c, cancelFunc := context.WithCancel(ctx)
	defer cancelFunc()
	s.cancelFunc = cancelFunc
	time.Sleep(s.listenDuration)
	s.readyM.Lock()
	s.ready = true
	s.readyM.Unlock()
	<-c.Done()
	return nil
}

func (h *longToGetReadyService) Close(ctx context.Context) error {
	h.cancelFunc()
	return nil
}

func (s *longToGetReadyService) IsReady(ctx context.Context) error {
	s.readyM.Lock()
	if s.ready {
		s.readyM.Unlock()
		return nil
	}
	s.readyM.Unlock()
	return errors.New("not ready")
}

type dummyResource struct {
	startDuration time.Duration
	started       bool
}

func (r *dummyResource) Name() string {
	return "http"
}

func (r *dummyResource) Start(ctx context.Context) error {
	r.started = true
	if r.startDuration > 0 {
		time.Sleep(r.startDuration)
	}
	return nil
}

func (r *dummyResource) Stop(ctx context.Context) error {
	r.started = false
	return nil
}
