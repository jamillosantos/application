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

	shutdownHandlerMutex sync.Mutex
	shutdownHandler      []func()
}

func defaultApplication() *Application {
	return &Application{
		context: context.Background(),

		version:   Version,
		build:     Build,
		buildDate: BuildDate,

		environment: goenv.GetStringDefault("ENV", "production"),

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

func (app *Application) WithName(value string) *Application {
	app.name = value
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

	if app.skipConfig {
		// Initializes and load the plain configuration
		plainConfigLoader := config.NewFileLoader(goenv.GetStringDefault("CONFIG", ".config.yaml"))
		plainEngine := config.NewYAMLEngine(plainConfigLoader)
		err = plainEngine.Load()
		if err != nil {
			logger.Error("could not initialize the plain engine", zap.Error(err))
			return err
		}

		// Initializes tand load the secret configuration
		secretConfigLoader := config.NewFileLoader(goenv.GetStringDefault("SECRETS", ".secrets.yaml"))
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

	}

	svcs, err := setup(ctx, app)
	if err != nil {
		logger.Error("failed setting the service up", zap.Error(err))
		return err
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
	return srvfiber.NewFiberServer(func(app *fiberv2.App) error {
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
