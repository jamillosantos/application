package application

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	fiberv2 "github.com/gofiber/fiber/v2"
	"github.com/jamillosantos/config"
	goenv "github.com/jamillosantos/go-env"
	goservices "github.com/jamillosantos/go-services"
	"github.com/jamillosantos/logctx"
	srvfiber "github.com/jamillosantos/server-fiber"
	svchealthcheck "github.com/jamillosantos/services-healthcheck"
	"github.com/jamillosantos/services-healthcheck/hcfiber"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/jamillosantos/application/zapreporter"
)

var (
	// Version is the version of the application. It can be set from using LDFLAGS.
	Version = ""
	// Build is the commit hash originated the build version. It can be set from using LDFLAGS.
	Build = ""
	// BuildDate is the timestamp informing when the app was built. It can be set from using LDFLAGS.
	BuildDate = ""
)

type appState string

const (
	stateRunning appState = "running"
)

// ServiceSetup is the handler that is pased to the Application.Run receiver.
type ServiceSetup func(ctx context.Context, app *Application) ([]goservices.Service, error)

type Application struct {
	context context.Context

	stateM sync.Mutex
	state  appState

	name      string
	version   string
	build     string
	buildDate string
	goVersion string

	loggerZapOptions    []zap.Option
	disableSystemServer bool

	environment string

	skipConfig    bool
	ConfigManager *config.Manager
	Runner        *goservices.Runner

	shutdownHandlerMutex       sync.Mutex
	shutdownHandler            []func()
	shutdownGracePeriod        time.Duration
	zapConfigModifier          func(*zap.Config)
	configManagerConfigOptions []config.Option
	systemServerInitialize     func(*fiberv2.App) error
	systemServerBindAddress    string
}

func defaultApplication() *Application {
	return &Application{
		context:                    context.Background(),
		stateM:                     sync.Mutex{},
		state:                      "",
		name:                       "",
		version:                    Version,
		build:                      Build,
		buildDate:                  BuildDate,
		goVersion:                  "",
		loggerZapOptions:           nil,
		disableSystemServer:        false,
		environment:                goenv.GetStringDefault("ENV", "production"),
		skipConfig:                 false,
		ConfigManager:              nil,
		Runner:                     nil,
		shutdownHandlerMutex:       sync.Mutex{},
		shutdownHandler:            []func(){},
		shutdownGracePeriod:        30 * time.Second,
		zapConfigModifier:          nil,
		configManagerConfigOptions: []config.Option{},
		systemServerInitialize:     nil,
		systemServerBindAddress:    ":8082",
	}
}

func New() *Application {
	return defaultApplication()
}

func (app *Application) WithContext(ctx context.Context) *Application {
	app.context = ctx
	return app
}

func (app *Application) WithName(value string) *Application {
	app.name = value
	return app
}

func (app *Application) WithConfigManagerOptions(options ...config.Option) *Application {
	app.configManagerConfigOptions = append(app.configManagerConfigOptions, options...)
	return app
}

func (app *Application) WithSystemServerBindAddress(value string) *Application {
	app.systemServerBindAddress = value
	return app
}

// WithVersion customizes the version of the application.
// Deprecated: Now the versions are extracted automatically from the go1.18 buildinfo.
func (app *Application) WithVersion(version, build, buildDate string) *Application {
	app.version, app.build, app.buildDate = version, build, buildDate
	return app
}

func (app *Application) WithLoggerZapOptions(options ...zap.Option) *Application {
	app.loggerZapOptions = options
	return app
}

func (app *Application) WithZapConfigModifier(f func(*zap.Config)) *Application {
	app.zapConfigModifier = f
	return app
}

func (app *Application) WithEnvironment(environment string) *Application {
	app.environment = environment
	return app
}

func (app *Application) WithDisableSystemServer(disable bool) *Application {
	app.disableSystemServer = disable
	return app
}

// WithSkipConfig skips the configuration loading when this instance runs.
func (app *Application) WithSkipConfig(skip bool) *Application {
	app.skipConfig = skip
	return app
}

func (app *Application) Shutdown(handler func()) *Application {
	app.shutdownHandlerMutex.Lock()
	app.shutdownHandler = append(app.shutdownHandler, handler)
	app.shutdownHandlerMutex.Unlock()
	return app
}

// WithShutdownGracePeriod sets the maximum time to wait for shutdown handlers to complete.
// If the grace period elapses before all handlers finish, the application stops waiting and exits.
func (app *Application) WithShutdownGracePeriod(d time.Duration) *Application {
	app.shutdownGracePeriod = d
	return app
}

func (app *Application) Run(setup ServiceSetup) {
	err := app.run(setup)
	if err != nil {
		os.Exit(1)
	}
}

func (app *Application) run(setup ServiceSetup) error {
	var (
		logger *zap.Logger
		err    error
	)

	var zapcfg zap.Config
	switch app.environment {
	case "dev":
		zapcfg = zap.NewDevelopmentConfig()
	default:
		zapcfg = zap.NewProductionConfig()
	}
	if app.zapConfigModifier != nil {
		app.zapConfigModifier(&zapcfg)
	}
	zapcfg.DisableStacktrace = true
	zapcfg.EncoderConfig.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	logger, err = zapcfg.Build(app.loggerZapOptions...)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "failed initialising logger:", err.Error())
		return err
	}

	if bi, ok := debug.ReadBuildInfo(); ok {
		app.populateFromBuildInfo(bi)
	}

	logger = logger.With(
		zap.String("app", app.name),
		zap.String("version", app.version),
		zap.String("build", app.build),
		zap.String("build_date", app.buildDate),
		zap.String("go_version", app.goVersion),
	)

	ctx, cancelFunc := signal.NotifyContext(app.context, os.Interrupt, syscall.SIGTERM)
	defer cancelFunc()

	ctx = logctx.WithLogger(ctx, logger)

	// Initializes the default logger instance
	err = logctx.Initialize(logctx.WithDefaultLogger(logger))
	if err != nil {
		return err
	}

	hc := svchealthcheck.NewHealthcheck(
		svchealthcheck.WithReadyCheck("app", &appChecker{app}),
	)
	hcObserver := newHealthchekcObserver(hc)

	app.Runner = goservices.NewRunner(
		goservices.WithReporter(zapreporter.New(logger)),
		goservices.WithObserver(hcObserver),
	)
	defer func() {
		r := recover()
		if r != nil {
			logger.Error("application panic: ", zap.Any("panic", r), zap.StackSkip("stack", 1))
		}

		shutdownCtx := context.Background()
		shutdownCancel := context.CancelFunc(func() {})
		if app.shutdownGracePeriod > 0 {
			shutdownCtx, shutdownCancel = context.WithTimeout(context.Background(), app.shutdownGracePeriod)
		}
		defer shutdownCancel()

		app.shutdownHandlerMutex.Lock()
		handlers := make([]func(), len(app.shutdownHandler))
		copy(handlers, app.shutdownHandler)
		app.shutdownHandlerMutex.Unlock()

		done := make(chan struct{})
		go func() {
			defer close(done)

			if err := app.Runner.Finish(shutdownCtx); err != nil {
				logger.Error("error stopping the services", zap.Error(err))
			}
			select {
			case <-shutdownCtx.Done():
				return
			default:
			}

			for _, h := range handlers {
				h()

				select {
				case <-shutdownCtx.Done():
					return
				default:
				}
			}
		}()

		select {
		case <-done:
		case <-shutdownCtx.Done():
			logger.Warn("shutdown grace period exceeded, forcing exit")
		}

		_ = logger.Sync()
	}()

	if err := app.runSystemServer(ctx, hc); err != nil {
		logger.Error("failed to start system server", zap.Error(err))
		return err
	}

	if !app.skipConfig {
		app.ConfigManager = config.NewManager(
			app.configManagerConfigOptions...,
		)
	}

	svcs, err := setup(ctx, app)
	if err != nil {
		logger.Error("failed setting the service up", zap.Error(err))
		return err
	}

	// No need to run the services if there is no service to run.
	if len(svcs) == 0 {
		return nil
	}

	err = app.Runner.Run(ctx, svcs...)
	if err != nil {
		logger.Error("failed running service", zap.Error(err))
		return err
	}

	app.stateM.Lock()
	app.state = stateRunning
	app.stateM.Unlock()

	<-ctx.Done()

	return nil
}

// extractServiceName extracts the service name from the repository path.
func extractServiceName(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func (app *Application) populateFromBuildInfo(bi *debug.BuildInfo) {
	if app.name == "" {
		app.name = extractServiceName(bi.Main.Path)
	}
	app.version = findSettingsIfEmpty(bi, "", app.version, bi.Main.Version, "undefined")
	app.build = findSettingsIfEmpty(bi, "vcs.revision", app.build, bi.Main.Sum, "undefined")
	app.buildDate = findSettingsIfEmpty(bi, "vcs.time", app.buildDate, "", "undefined")
	if bi.GoVersion != "" {
		app.goVersion = bi.GoVersion
	}
}

func findSettingsIfEmpty(bi *debug.BuildInfo, key, value, value2, defaultValue string) string {
	if value != "" {
		return value
	}
	if value2 != "" {
		return value2
	}
	for _, v := range bi.Settings {
		if v.Key == key {
			return v.Value
		}
	}
	return defaultValue
}

// buildSystemServer initializes the server for metrics.
func (app *Application) buildSystemServer(hc *svchealthcheck.Healthcheck) *srvfiber.FiberServer {
	return srvfiber.NewFiberServer(func(fiberApp *fiberv2.App) error {
		hcfiber.FiberInitialize(hc, fiberApp)

		if app.systemServerInitialize != nil {
			return app.systemServerInitialize(fiberApp)
		}

		return nil
	}, srvfiber.WithName("metrics/health/live"), srvfiber.WithBindAddress(app.systemServerBindAddress))
}

// runSystemServer starts the server for metrics, health and ready checks. If the disableSystemServer flag is set,
// this function does nothing returning no error.
func (app *Application) runSystemServer(ctx context.Context, hc *svchealthcheck.Healthcheck) error {
	if app.disableSystemServer {
		return nil
	}
	systemServer := app.buildSystemServer(hc)
	return app.Runner.Run(ctx, systemServer)
}
