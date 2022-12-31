package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"runtime/debug"
	"testing"
	"time"

	"github.com/DataDog/gostackparse"
	goservices "github.com/jamillosantos/go-services"
	svchealthcheck "github.com/jamillosantos/services-healthcheck"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplication_WithContext(t *testing.T) {
	wantContext := context.Background()
	app := (&Application{}).WithContext(wantContext)
	assert.Equal(t, wantContext, app.context)
}

func TestApplication_WithVersion(t *testing.T) {
	wantVersion, wantBuild, wantBuildDate := "version", "build", "build_date"
	app := (&Application{}).WithVersion(wantVersion, wantBuild, wantBuildDate)
	assert.Equal(t, wantVersion, app.version)
	assert.Equal(t, wantBuild, app.build)
	assert.Equal(t, wantBuildDate, app.buildDate)
}

func TestApplication_WithEnvironment(t *testing.T) {
	wantEnvironment := "environment"
	app := (&Application{}).WithEnvironment(wantEnvironment)
	assert.Equal(t, wantEnvironment, app.environment)
}

func TestApplication_Shutdown(t *testing.T) {
	wantShutdownHandler := func() {}
	app := (&Application{}).Shutdown(wantShutdownHandler)
	require.Len(t, app.shutdownHandler, 1)
}

func TestApplication(t *testing.T) {
	t.Run("should start and stop all servers and resources", func(t *testing.T) {
		ctx, cancelFunc := context.WithCancel(context.Background())

		app := New().WithContext(ctx)

		r := &dummyResource{}

		go func() {
			os.Setenv("CONFIG", "./testdata/.config.yaml")
			os.Setenv("SECRETS", "./testdata/.secrets.yaml")

			app.Run(func(ctx context.Context, app *Application) ([]goservices.Service, error) {
				h := &httpService{}
				return []goservices.Service{h, r}, nil
			})
		}()

		require.Eventually(t, func() bool {
			_, err := http.Get("http://localhost:8080")
			return assert.NoError(t, err)
		}, time.Second*5, time.Second)

		require.Eventually(t, func() bool {
			resp, err := http.Get("http://localhost:8082/healthz")
			if err != nil {
				return assert.NoError(t, err)
			}
			responseContent, err := io.ReadAll(resp.Body)
			if err != nil {
				return assert.NoError(t, err)
			}
			fmt.Println(string(responseContent))
			return true
		}, time.Second*2, time.Millisecond*100)

		require.Eventually(t, func() bool {
			resp, err := http.Get("http://localhost:8082/readyz")
			if err != nil {
				return assert.NoError(t, err)
			}
			responseContent, err := io.ReadAll(resp.Body)
			if err != nil {
				return assert.NoError(t, err)
			}
			fmt.Println(string(responseContent))
			return true
		}, time.Second*2, time.Millisecond*100)

		assert.True(t, r.started)

		time.Sleep(time.Millisecond * 300)

		cancelFunc()

		require.Eventually(t, func() bool {
			_, err := http.Get("http://localhost:8080")
			return assert.Error(t, err)
		}, time.Second, time.Millisecond*100)

		assert.False(t, r.started)
	})

	t.Run("should not be ready until the Run function finishes", func(t *testing.T) {
		ctx, cancelFunc := context.WithCancel(context.Background())

		app := New().WithContext(ctx)

		go func() {
			os.Setenv("CONFIG", "./testdata/.config.yaml")
			os.Setenv("SECRETS", "./testdata/.secrets.yaml")

			r := &dummyResource{
				startDuration: time.Second,
			}
			app.Run(func(ctx context.Context, app *Application) ([]goservices.Service, error) {
				return []goservices.Service{r}, nil
			},
			)
		}()

		// Ensure the system http endpoint is ok and not ready.
		assert.Eventually(t, appIsNotReady(), time.Second*200, time.Millisecond*100, "should be not ready until all services are started")

		assert.Eventually(t, healthCheckBecomesReady(), time.Second*3, time.Millisecond*100, "app should be ready after 2s")

		cancelFunc()

		assert.Eventually(t, func() bool {
			_, err := http.Get("http://localhost:8082/readyz")
			return assert.Error(t, err)
		}, time.Second*3, time.Millisecond*100)
	})

	t.Run("should be ready only when all readable services are ready", func(t *testing.T) {
		ctx, cancelFunc := context.WithCancel(context.Background())

		app := New().WithContext(ctx)

		lgrs := &longToGetReadyService{
			listenDuration: time.Second * 1,
		}

		go func() {
			os.Setenv("CONFIG", "./testdata/.config.yaml")
			os.Setenv("SECRETS", "./testdata/.secrets.yaml")

			app.Run(func(ctx context.Context, app *Application) ([]goservices.Service, error) {
				return []goservices.Service{lgrs}, nil
			},
			)
		}()

		now := time.Now()
		require.Eventually(t, func() bool {
			readyz, err := getReadyz()
			if err != nil {
				logError("failed to get readyz", err)
				return false
			}
			if c, ok := readyz.Checks[lgrs.Name()]; !ok || c.Error != "" {
				logError("check not available", c)
				return false
			}
			return true
		}, time.Second*5, time.Millisecond*500)
		assert.InDelta(t, time.Second*1, time.Since(now), float64(time.Second), "took too long to become ready")

		cancelFunc()

		require.Eventually(t, func() bool {
			_, err := http.Get("http://localhost:8080")
			return assert.Error(t, err)
		}, time.Second, time.Millisecond*100)
	})

	t.Run("should clean and proper finish all services when one of 2 long starting servers fail", func(t *testing.T) {
		t.Skip("not implemented")
	})

	t.Run("should clean and properly finish all services when during a long starting server receive a Finish", func(t *testing.T) {
		t.Skip("not implemented")
	})
}

func getReadyz() (svchealthcheck.CheckResponse, error) {
	resp, err := http.Get("http://localhost:8082/readyz")
	if err != nil {
		return svchealthcheck.CheckResponse{}, err
	}
	var jsonResp svchealthcheck.CheckResponse
	err = json.NewDecoder(resp.Body).Decode(&jsonResp)
	if err != nil {
		return svchealthcheck.CheckResponse{}, err
	}
	jsonResp.StatusCode = resp.StatusCode
	return jsonResp, nil
}

func appIsNotReady() func() bool {
	return func() bool {
		readyz, err := getReadyz()
		if err != nil {
			logError("failed getting Readyz", err)
			return false
		}
		if len(readyz.Checks) == 0 {
			logError("jsonResp.checks is 0")
			return false
		}
		if readyz.StatusCode != http.StatusServiceUnavailable {
			logError("resp.statusCode is", readyz.StatusCode, ". 503 expected")
			return false
		}
		if readyz.Checks["app"].Error != ErrAppNotRunningYet.Error() {
			logError("jsonResp.Checks[\"app\"].Error is", readyz.Checks["app"].Error, ". \"", ErrAppNotRunningYet.Error(), "\" expected")
			return false
		}
		return true
	}
}

func logError(v ...interface{}) {
	s := debug.Stack()
	goroutines, _ := gostackparse.Parse(bytes.NewReader(s))
	v = append(v, fmt.Sprintf("%s:%d", path.Base(goroutines[0].Stack[2].File), goroutines[0].Stack[2].Line))
	v = append([]interface{}{"  >"}, v...)
	fmt.Println(v...)
}

func healthCheckBecomesReady() func() bool {
	return func() bool {
		readyz, err := getReadyz()
		if err != nil {
			logError("failed getting Readyz", err)
			return false
		}
		if len(readyz.Checks) != 2 {
			logError("jsonResp.checks expected to have len 2. Got", len(readyz.Checks))
			return false
		}
		if readyz.StatusCode != http.StatusOK {
			logError("resp.statusCode is", readyz.StatusCode, ". 200 expected")
			return false
		}
		if readyz.Checks["app"].Error != "" {
			logError("jsonResp.Checks[\"app\"].Error is", readyz.Checks["app"].Error, ". \"\" expected")
			return false
		}
		return true
	}
}
