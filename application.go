package application

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/jamillosantos/config"
	"github.com/jamillosantos/go-env"
	"github.com/jamillosantos/go-services"
	"github.com/jamillosantos/logctx"
	"go.uber.org/zap"

	"github.com/jamillosantos/application/zapreporter"
)

// ServiceSetup is the handler that is pased to the Application.Run receiver.
type ServiceSetup func(ctx context.Context, app *Application) ([]services.Service, error)

type Application struct {
	context context.Context

	version   string
	build     string
	buildDate string

	environment string

	ConfigManager *config.Manager
	Runner        *services.Runner

	shutdownHandlerMutex sync.Mutex
	shutdownHandler      []func()
}

func defaultApplication() *Application {
	return &Application{
		context: context.Background(),

		version:   "dev",
		build:     "local",
		buildDate: "unspecified",

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

func (app *Application) WithEnvironment(environment string) *Application {
	app.environment = environment
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
		switch app.environment {
		case "dev":
			logger, err = zap.NewDevelopment()
		default:
			logger, err = zap.NewProduction()
		}
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "failed initializing logger:", err.Error())
			return err
		}
		logger = logger.With(
			zap.String("service", serviceName),
			zap.String("version", app.version),
			zap.String("build", app.build),
			zap.String("build_date", app.buildDate),
			zap.String("go_version", runtime.Version()),
		)

		ctx := logctx.WithLogger(app.context, logger)

		// Initializes the default logger instance
		err = logctx.Initialize(logctx.WithDefaultLogger(logger))
		if err != nil {
			return err
		}

		app.Runner = services.NewRunner(
			services.WithReporter(zapreporter.New(logger)),
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

		return nil
	}()
	if err != nil {
		os.Exit(1)
	}
}
