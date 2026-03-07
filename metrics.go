package application

import (
	"net/http"

	"github.com/gofiber/fiber/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type metricsResponseWriter struct {
	fiber       fiber.Ctx
	promHandler http.Handler
}

func (m *metricsResponseWriter) Header() http.Header {
	return http.Header{}
}

func (m *metricsResponseWriter) Write(bytes []byte) (int, error) {
	return m.fiber.Write(bytes)
}

func (m *metricsResponseWriter) WriteHeader(statusCode int) {
	m.fiber.Status(statusCode)
}

// metricsEndpoint is a lightweight adapter from promhttp.Handler to fiber.Handler. It is used to expose the Prometheus
// metrics endpoint.
func metricsEndpoint(reg prometheus.Gatherer, opts promhttp.HandlerOpts) fiber.Handler {
	promHandler := promhttp.HandlerFor(reg, opts)
	return func(ctx fiber.Ctx) error {
		promHandler.ServeHTTP(&metricsResponseWriter{
			fiber:       ctx,
			promHandler: promHandler,
		}, &http.Request{
			// The other fields are not needed, at least for the revision used now.
			Header: ctx.GetReqHeaders(),
		})
		return nil
	}
}
