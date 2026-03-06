package application

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	goservices "github.com/jamillosantos/go-services"
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
					WithPolling(1 * time.Millisecond).
					Should(BeTrue())

				beforeShutdown := time.Now()
				cancelFunc() // Shutdown!

				Eventually(runDone).
					WithTimeout(3 * time.Second).
					Should(BeClosed())

				Expect(handlerCompleted).To(BeTrue())
				Expect(time.Since(beforeShutdown)).To(BeNumerically("~", shutdownDuration, 100*time.Millisecond))
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
				Expect(time.Since(beforeShutdown)).To(BeNumerically("~", shutdownDuration, time.Millisecond*100))
			})
		})

		When("the shutdown handler takes too long", func() {
			It("should wait until the grace period is reached and then force exit", func() {
				ctx, cancelFunc := context.WithCancel(context.Background())

				gracePeriod := 300 * time.Millisecond
				handlerDuration := 2 * time.Second
				var handlerCompleted bool

				app := New().
					WithContext(ctx).
					WithSkipConfig(true).
					WithDisableSystemServer(true).
					WithShutdownGracePeriod(gracePeriod).
					Shutdown(func() {
						time.Sleep(handlerDuration)
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
				cancelFunc()

				Eventually(runDone).WithTimeout(gracePeriod + time.Second).Should(BeClosed())

				Expect(handlerCompleted).To(BeFalse())
				Expect(time.Since(beforeShutdown)).To(BeNumerically("~", gracePeriod, 200*time.Millisecond))
			})
		})

		When("the shutdown closing service takes too long", func() {
			It("should wait until the grace period is reached and then force exit", func() {
				ctx, cancelFunc := context.WithCancel(context.Background())

				gracePeriod := 300 * time.Millisecond
				handlerDuration := 2 * time.Second
				var handlerCompleted bool

				app := New().
					WithContext(ctx).
					WithSkipConfig(true).
					WithDisableSystemServer(true).
					WithShutdownGracePeriod(gracePeriod).
					Shutdown(func() {
						// This should not be called because the service won't finish on time.
						handlerCompleted = true
					})

				runDone := make(chan struct{})
				go func() {
					defer close(runDone)
					_ = app.run(func(ctx context.Context, app *Application) ([]goservices.Service, error) {
						return []goservices.Service{&dummyResource{
							stopDuration: handlerDuration,
						}}, nil
					})
				}()

				Eventually(app.IsRunning).
					WithTimeout(3 * time.Second).
					WithPolling(50 * time.Millisecond).
					Should(BeTrue())

				beforeShutdown := time.Now()
				cancelFunc()

				Eventually(runDone).WithTimeout(gracePeriod + 100*time.Millisecond).Should(BeClosed())

				Expect(handlerCompleted).To(BeFalse())
				Expect(time.Since(beforeShutdown)).To(BeNumerically("~", gracePeriod, 200*time.Millisecond))
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

			app := New().
				WithContext(ctx)
			defer cancelFunc()

			go func() {
				app.Run(func(ctx context.Context, app *Application) ([]goservices.Service, error) {
					return []goservices.Service{&dummyResource{
						startDuration: time.Second,
					}}, nil
				})
			}()

			now := time.Now()

			By("waiting the app to become ready")
			Eventually(app.IsReady).
				WithTimeout(3 * time.Second).
				WithPolling(1 * time.Millisecond).
				Should(BeTrue())

			By("checking how much time the system took to become ready")
			Expect(time.Since(now)).To(BeNumerically("~", time.Second, 10*time.Millisecond))
		})

		It(`should be ready only when all "ready"ble services are ready`, func() {
			ctx, cancelFunc := context.WithCancel(context.Background())
			defer cancelFunc()

			os.Setenv("CONFIG_LOAD_OPTIONS", `{"plain":["yamlfile:./testdata/.config.yaml","secrets":[yamlfile:./testdata/.secrets.yaml]}`)

			app := New().WithContext(ctx)

			go func() {
				app.Run(func(ctx context.Context, app *Application) ([]goservices.Service, error) {
					return []goservices.Service{
						&longToGetReadyService{
							listenDuration: 10 * time.Millisecond,
						},
						&longToGetReadyService{
							listenDuration: 40 * time.Millisecond,
						},
						&longToGetReadyService{
							listenDuration: 100 * time.Millisecond,
						},
						&longToGetReadyService{
							listenDuration: 200 * time.Millisecond,
						},
					}, nil
				})
			}()

			now := time.Now()
			Eventually(app.IsReady).
				WithTimeout(3 * time.Second).
				WithPolling(1 * time.Millisecond).
				Should(BeTrue())
			Expect(time.Since(now)).To(BeNumerically("~", time.Second, time.Second))

			By("checking how much time the system took to become ready")
			Expect(time.Since(now)).To(BeNumerically("~", 350*time.Millisecond, 10*time.Millisecond))
		})

		It("should clean and proper finish all services when one of 2 long starting servers fail", func() {
			Skip("not implemented")
		})

		It("should clean and properly finish all services when during a long starting server receive a Finish", func() {
			Skip("not implemented")
		})
	})
})
