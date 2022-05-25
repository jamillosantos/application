package application

import (
	"context"
	"errors"
)

type HealthChecker interface {
	IsHealthy(ctx context.Context) error
}

type ReadyChecker interface {
	IsReady(ctx context.Context) error
}

type appChecker struct {
	*Application
}

var (
	ErrAppNotRunningYet = errors.New("app is not running yet")
)

func (a appChecker) Check(ctx context.Context) error {
	a.stateM.Lock()
	if a.state != stateRunning {
		a.stateM.Unlock()
		return ErrAppNotRunningYet
	}
	a.stateM.Unlock()
	return nil
}
