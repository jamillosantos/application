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
	"time"

	"github.com/DataDog/gostackparse"
	goservices "github.com/jamillosantos/go-services"
	svchealthcheck "github.com/jamillosantos/services-healthcheck"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Application", func() {
	Describe("WithContext", func() {
		It("should set the context", func() {
			wantContext := context.Background()
			app := (&Application{}).WithContext(wantContext)
			Expect(app.context).To(Equal(wantContext))
		})
	})

	Describe("WithVersion", func() {
		It("should set the version, build and buildDate", func() {
			wantVersion, wantBuild, wantBuildDate := "version", "build", "build_date"
			app := (&Application{}).WithVersion(wantVersion, wantBuild, wantBuildDate)
			Expect(app.version).To(Equal(wantVersion))
			Expect(app.build).To(Equal(wantBuild))
			Expect(app.buildDate).To(Equal(wantBuildDate))
		})
	})

	Describe("WithEnvironment", func() {
		It("should set the environment", func() {
			wantEnvironment := "environment"
			app := (&Application{}).WithEnvironment(wantEnvironment)
			Expect(app.environment).To(Equal(wantEnvironment))
		})
	})

	Describe("Shutdown", func() {
		It("should add a shutdown handler", func() {
			wantShutdownHandler := func() {}
			app := (&Application{}).Shutdown(wantShutdownHandler)
			Expect(app.shutdownHandler).To(HaveLen(1))
		})

		When(`processing a long running shutdown function`, func() {
			It("should wait for all shutdown function complete", func() {
				ctx, cancelFunc := context.WithCancel(context.Background())

				shutdownDuration := 500 * time.Millisecond
				var handlerCompleted bool

				app := New().
					WithContext(ctx).
					WithSkipConfig(true).
					WithDisableSystemServer(true).
					Shutdown(func() {
						time.Sleep(shutdownDuration)
						handlerCompleted = true
					})

				runDone := make(chan struct{})
				go func() {
					defer close(runDone)
					_ = app.run(func(ctx context.Context, app *Application) ([]goservices.Service, error) {
						return []goservices.Service{&dummyResource{}}, nil
					})
				}()

				Eventually(app.IsRunning).
					WithTimeout(3 * time.Second).
					WithPolling(50 * time.Millisecond).
					Should(BeTrue())

				beforeShutdown := time.Now()
				cancelFunc() // Shutdown!

				Eventually(runDone).WithTimeout(3 * time.Second).Should(BeClosed())

				Expect(handlerCompleted).To(BeTrue())
				Expect(time.Since(beforeShutdown)).To(BeNumerically("~", shutdownDuration, time.Millisecond*10))
			})
		})

		When(`processing a long running closing service`, func() {
			It("should wait for all shutdown handlers to complete before finishing", func() {
				ctx, cancelFunc := context.WithCancel(context.Background())

				shutdownDuration := 500 * time.Millisecond

				app := New().
					WithContext(ctx).
					WithSkipConfig(true).
					WithDisableSystemServer(true)

				srv := &dummyResource{
					stopDuration: shutdownDuration,
				}

				runDone := make(chan struct{})
				go func() {
					defer close(runDone)
					_ = app.run(func(ctx context.Context, app *Application) ([]goservices.Service, error) {
						return []goservices.Service{srv}, nil
					})
				}()

				Eventually(app.IsRunning).
					WithTimeout(3 * time.Second).
					WithPolling(50 * time.Millisecond).
					Should(BeTrue())

				beforeShutdown := time.Now()
				cancelFunc() // Shutdown!

				Eventually(runDone).WithTimeout(3 * time.Second).Should(BeClosed())

				Expect(srv.started).To(BeFalse())
				Expect(time.Since(beforeShutdown)).To(BeNumerically("~", shutdownDuration, time.Millisecond*10))
			})
		})
	})

	Describe("Run", func() {
		It("should start and stop all servers and resources", func() {
			ctx, cancelFunc := context.WithCancel(context.Background())

			os.Setenv("CONFIG_LOAD_OPTIONS", `{"plain":["yamlfile:./testdata/.config.yaml","secrets":[yamlfile:./testdata/.secrets.yaml]}`)

			app := New().WithContext(ctx)

			r := &dummyResource{}

			go func() {
				app.Run(func(ctx context.Context, app *Application) ([]goservices.Service, error) {
					h := &httpService{}
					return []goservices.Service{h, r}, nil
				})
			}()

			Eventually(func() error {
				_, err := http.Get("http://localhost:8080")
				return err
			}).WithTimeout(5 * time.Second).WithPolling(time.Second).Should(Succeed())

			Eventually(func() error {
				resp, err := http.Get("http://localhost:8082/healthz")
				if err != nil {
					return err
				}
				_, err = io.ReadAll(resp.Body)
				return err
			}).WithTimeout(2 * time.Second).WithPolling(100 * time.Millisecond).Should(Succeed())

			Eventually(func() error {
				resp, err := http.Get("http://localhost:8082/readyz")
				if err != nil {
					return err
				}
				_, err = io.ReadAll(resp.Body)
				return err
			}).WithTimeout(2 * time.Second).WithPolling(100 * time.Millisecond).Should(Succeed())

			Expect(r.started).To(BeTrue())

			time.Sleep(300 * time.Millisecond)

			cancelFunc()

			Eventually(func() error {
				_, err := http.Get("http://localhost:8080")
				if err != nil {
					return nil
				}
				return fmt.Errorf("expected connection to be refused")
			}).WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(Succeed())

			Expect(r.started).To(BeFalse())
		})

		It("should not be ready until the Run function finishes", func() {
			ctx, cancelFunc := context.WithCancel(context.Background())

			os.Setenv("CONFIG_LOAD_OPTIONS", `{"plain":["yamlfile:./testdata/.config.yaml","secrets":[yamlfile:./testdata/.secrets.yaml]}`)

			port := "8089"

			app := New().
				WithSystemServerBindAddress(":" + port).
				WithContext(ctx)

			go func() {
				r := &dummyResource{
					startDuration: time.Second,
				}
				app.Run(func(ctx context.Context, app *Application) ([]goservices.Service, error) {
					return []goservices.Service{r}, nil
				})
			}()

			Eventually(appIsNotReady(port)).
				WithTimeout(200*time.Second).
				WithPolling(100*time.Millisecond).
				Should(BeTrue(), "should be not ready until all services are started")

			Eventually(healthCheckBecomesReady(port)).
				WithTimeout(3*time.Second).
				WithPolling(100*time.Millisecond).
				Should(BeTrue(), "app should be ready after 2s")

			cancelFunc()

			Eventually(func() error {
				_, err := http.Get("http://localhost:8089/readyz")
				if err != nil {
					return nil
				}
				return fmt.Errorf("expected connection to be refused")
			}).WithTimeout(3 * time.Second).WithPolling(100 * time.Millisecond).Should(Succeed())
		})

		It(`should be ready only when all "ready"ble services are ready`, func() {
			ctx, cancelFunc := context.WithCancel(context.Background())

			os.Setenv("CONFIG_LOAD_OPTIONS", `{"plain":["yamlfile:./testdata/.config.yaml","secrets":[yamlfile:./testdata/.secrets.yaml]}`)

			app := New().WithContext(ctx)

			lgrs := &longToGetReadyService{
				listenDuration: time.Second * 1,
			}

			go func() {
				app.Run(func(ctx context.Context, app *Application) ([]goservices.Service, error) {
					return []goservices.Service{lgrs}, nil
				})
			}()

			now := time.Now()
			Eventually(func() bool {
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
			}).WithTimeout(5 * time.Second).WithPolling(500 * time.Millisecond).Should(BeTrue())
			Expect(time.Since(now)).To(BeNumerically("~", time.Second, time.Second))

			cancelFunc()

			Eventually(func() error {
				_, err := http.Get("http://localhost:8080")
				if err != nil {
					return nil
				}
				return fmt.Errorf("expected connection to be refused")
			}).WithTimeout(time.Second).WithPolling(100 * time.Millisecond).Should(Succeed())
		})

		It("should clean and proper finish all services when one of 2 long starting servers fail", func() {
			Skip("not implemented")
		})

		It("should clean and properly finish all services when during a long starting server receive a Finish", func() {
			Skip("not implemented")
		})
	})
})

func getReadyz(port ...string) (svchealthcheck.CheckResponse, error) {
	p := "8082"
	if len(port) > 0 {
		p = port[0]
	}
	resp, err := http.Get(fmt.Sprintf("http://localhost:%s/readyz", p))
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

func appIsNotReady(port ...string) func() bool {
	return func() bool {
		readyz, err := getReadyz(port...)
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

func healthCheckBecomesReady(port ...string) func() bool {
	return func() bool {
		readyz, err := getReadyz(port...)
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
