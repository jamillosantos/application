package application

import (
	"context"
	"errors"
)

// HealthChecker can be implemented by a goservices.Service to participate in
// the /healthz endpoint. The service is automatically registered when the
// runner starts it.
type HealthChecker interface {
	IsHealthy(ctx context.Context) error
}

// ReadyChecker can be implemented by a goservices.Service to participate in
// the /readyz endpoint. The service is automatically registered when the
// runner starts it. The application itself is ready only after all services
// that implement ReadyChecker have returned nil from IsReady.
type ReadyChecker interface {
	IsReady(ctx context.Context) error
}

type appChecker struct {
	*Application
}

var (
	// ErrAppNotRunningYet is returned by the built-in readiness check while the
	// application has not yet transitioned to the running state.
	ErrAppNotRunningYet = errors.New("app is not running yet")
)

func (a appChecker) Check(_ context.Context) error {
	a.stateM.Lock()
	if a.state != stateRunning {
		a.stateM.Unlock()
		return ErrAppNotRunningYet
	}
	a.stateM.Unlock()
	return nil
}
