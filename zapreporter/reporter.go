package zapreporter

import (
	"context"
	"os"

	services "github.com/jamillosantos/go-services"
	"github.com/jamillosantos/logctx"
	"go.uber.org/zap"
)

const (
	loggingFieldDependencyService = "dependency.service"
	loggingFieldOSSignal          = "os.signal"
)

type ZapReporter struct {
	logger *zap.Logger
}

func New(logger *zap.Logger) *ZapReporter {
	return &ZapReporter{logger}
}

func (reporter *ZapReporter) BeforeStart(ctx context.Context, service services.Service) {
	reporter.logger.
		With(zap.String(loggingFieldDependencyService, service.Name())).
		Info("starting service")
}

func (reporter *ZapReporter) AfterStart(ctx context.Context, service services.Service, err error) {
	logger := reporter.logger.With(zap.String(loggingFieldDependencyService, service.Name()))
	if err != nil {
		logger.Error("failed starting service", zap.Error(err))
		return
	}
	logger.Info("service started")
}

func (reporter *ZapReporter) BeforeStop(ctx context.Context, service services.Service) {
	reporter.logger.
		With(zap.String(loggingFieldDependencyService, service.Name())).
		Info("stopping service")
}

func (reporter *ZapReporter) AfterStop(ctx context.Context, service services.Service, err error) {
	logger := reporter.logger.With(zap.String(loggingFieldDependencyService, service.Name()))
	if err != nil {
		logger.Error("failed stopping service", zap.Error(err))
		return
	}
	logger.Info("service stopped")
}

func (reporter *ZapReporter) BeforeLoad(ctx context.Context, configurable services.Configurable) {
	// TODO
}

func (reporter *ZapReporter) AfterLoad(ctx context.Context, configurable services.Configurable, err error) {
	// TODO
}

func (reporter *ZapReporter) SignalReceived(signal os.Signal) {
	logctx.From(context.Background()).
		Info("signal received", zap.String(loggingFieldOSSignal, signal.String()))
}

func (reporter *ZapReporter) BeforeRetry(ctx context.Context, service services.Service, i int) {
	reporter.logger.
		With(zap.String(loggingFieldDependencyService, service.Name())).
		Info("retrying service")
}
