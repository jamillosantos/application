package application

import (
	"context"
	"os"

	goservices "github.com/jamillosantos/go-services"
	svchealthcheck "github.com/jamillosantos/services-healthcheck"
)

type healthcheckObserver struct {
	hc *svchealthcheck.Healthcheck
}

func newHealthchekcObserver(hc *svchealthcheck.Healthcheck) *healthcheckObserver {
	return &healthcheckObserver{
		hc: hc,
	}
}

func (h *healthcheckObserver) BeforeStart(ctx context.Context, service goservices.Service) {
	h.addIfHealthCheck(service)
	h.addIfReadyCheck(service)
}

func (h healthcheckObserver) AfterStart(context.Context, goservices.Service, error) {}

func (h healthcheckObserver) BeforeStop(context.Context, goservices.Service) {}

func (h healthcheckObserver) AfterStop(context.Context, goservices.Service, error) {}

func (h healthcheckObserver) BeforeLoad(context.Context, goservices.Configurable) {}

func (h healthcheckObserver) AfterLoad(context.Context, goservices.Configurable, error) {}

func (h healthcheckObserver) SignalReceived(signal os.Signal) {}

func (h *healthcheckObserver) addIfHealthCheck(service goservices.Service) {
	hc, ok := service.(HealthChecker)
	if !ok {
		return
	}
	h.hc.AddHealthCheck(service.Name(), svchealthcheck.CheckerFunc(hc.IsHealthy))
}

func (h *healthcheckObserver) addIfReadyCheck(service goservices.Service) {
	rd, ok := service.(ReadyChecker)
	if !ok {
		return
	}
	h.hc.AddReadyCheck(service.Name(), svchealthcheck.CheckerFunc(rd.IsReady))
}
