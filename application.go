package application

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"

	"github.com/gofiber/fiber/v2"
	"github.com/jamillosantos/config"
	"github.com/jamillosantos/go-env"
	"github.com/jamillosantos/go-services"
	"github.com/jamillosantos/logctx"
	srvfiber "github.com/jamillosantos/server-fiber"
	svchealthcheck "github.com/jamillosantos/services-healthcheck"
	"github.com/jamillosantos/services-healthcheck/hcfiber"
	"go.uber.org/zap"

	"github.com/jamillosantos/application/zapreporter"
)

var (
	// Version is the version of the application. It can be set from using LDFLAGS.
	Version = "dev"
	// Build is the commit hash originated the build version. It can be set from using LDFLAGS.
	Build = "local"
	// BuildDate is the timestamp informing when the app was built. It can be set from using LDFLAGS.
	BuildDate = "unspecified"
)

type appState string

const (
	stateRunning appState = "running"
)

// ServiceSetup is the handler that is pased to the Application.Run receiver.
type ServiceSetup func(ctx context.Context, app *Application) ([]services.Service, error)

type Application struct {
	context context.Context

	stateM sync.Mutex
	state  appState

	version   string
	build     string
	buildDate string

	loggerZapOptions    []zap.Option
	disableSystemServer bool

	environment string

	ConfigManager *config.Manager
	Runner        *services.Runner

	shutdownHandlerMutex sync.Mutex
	shutdownHandler      []func()
}

func defaultApplication() *Application {
	return &Application{
		context: context.Background(),

		version:   Version,
		build:     Build,
		buildDate: BuildDate,

		environment: env.GetStringDefault("ENV", "production"),

		shutdownHandler: []func(){},
	}
}

func New() *Application {
	return defaultApplication()
}

func (app *Application) WithContext(ctx context.Context) *Application {
	app.context = ctx
	return app
}

func (app *Application) WithVersion(version, build, buildDate string) *Application {
	app.version, app.build, app.buildDate = version, build, buildDate
	return app
}

func (app *Application) WithLoggerZapOptions(options ...zap.Option) *Application {
	app.loggerZapOptions = options
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

func (app *Application) Shutdown(handler func()) *Application {
	app.shutdownHandlerMutex.Lock()
	app.shutdownHandler = append(app.shutdownHandler, handler)
	app.shutdownHandlerMutex.Unlock()
	return app
}

func (app *Application) Run(serviceName string, setup ServiceSetup) {
	err := func() error {
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
		zapcfg.DisableStacktrace = true
		logger, err = zapcfg.Build(app.loggerZapOptions...)
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "failed initialising logger:", err.Error())
			return err
		}
		logger = logger.With(
			zap.String("service", serviceName),
			zap.String("version", app.version),
			zap.String("build", app.build),
			zap.String("build_date", app.buildDate),
			zap.String("go_version", runtime.Version()),
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

		app.Runner = services.NewRunner(
			services.WithReporter(zapreporter.New(logger)),
			services.WithObserver(hcObserver),
		)
		defer func() {
			r := recover()
			if r != nil {
				logger.Error("application panic: ", zap.Any("panic", r), zap.StackSkip("stack", 1))
			}

			err := app.Runner.Finish(ctx)
			if err != nil {
				logger.Error("error stopping the services", zap.Error(err))
			}

			_ = logger.Sync()
		}()

		if err := app.runSystemServer(ctx, hc); err != nil {
			logger.Error("failed to start system server", zap.Error(err))
			return err
		}

		// Initializes and load the plain configuration
		plainConfigLoader := config.NewFileLoader(env.GetStringDefault("CONFIG", ".config.yaml"))
		plainEngine := config.NewYAMLEngine(plainConfigLoader)
		err = plainEngine.Load()
		if err != nil {
			logger.Error("could not initialize the plain engine", zap.Error(err))
			return err
		}

		// Initializes tand load the secret configuration
		secretConfigLoader := config.NewFileLoader(env.GetStringDefault("SECRETS", ".secrets.yaml"))
		secretEngine := config.NewYAMLEngine(secretConfigLoader)
		err = secretEngine.Load()
		if err != nil {
			logger.Error("could not initialize the secret engine", zap.Error(err))
			return err
		}

		configManager := config.NewManager()
		configManager.AddPlainEngine(plainEngine)
		configManager.AddSecretEngine(secretEngine)

		// Publish the config manager to be used into the setup callback
		app.ConfigManager = configManager

		svcs, err := setup(ctx, app)
		if err != nil {
			logger.Error("failed setting the service up", zap.Error(err))
			return err
		}

		// If nothing needs to be started, the listener is done.
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

		app.Runner.Wait(ctx)

		// TODO Remove the defer Wait from the go-services

		return nil
	}()
	if err != nil {
		os.Exit(1)
	}
}

// buildSystemServer initializes the server for metrics.
func (app *Application) buildSystemServer(hc *svchealthcheck.Healthcheck) *srvfiber.FiberServer {
	return srvfiber.NewFiberServer(func(app *fiber.App) error {
		hcfiber.FiberInitialize(hc, app)
		return nil
	}, srvfiber.WithName("metrics/health/live"), srvfiber.WithBindAddress(":8082"))
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
