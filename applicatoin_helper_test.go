package application

import (
	"encoding/json"
	"net/http"

	svchealthcheck "github.com/jamillosantos/services-healthcheck"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func (app *Application) IsRunning() bool {
	app.stateM.Lock()
	defer app.stateM.Unlock()
	return app.state == stateRunning
}

func (app *Application) IsStopped() bool {
	app.stateM.Lock()
	defer app.stateM.Unlock()
	return app.state == stateStopped
}

func (app *Application) IsReady() bool {
	GinkgoHelper()

	resp, err := http.Get("http://localhost:8082/readyz")
	Expect(err).ToNot(HaveOccurred())

	if resp.StatusCode != http.StatusOK {
		return false
	}

	var jsonResp svchealthcheck.CheckResponse
	Expect(json.NewDecoder(resp.Body).Decode(&jsonResp)).To(Succeed())

	if len(jsonResp.Checks) == 0 {
		return true
	}

	for _, check := range jsonResp.Checks {
		if check.Error != "" {
			return false
		}
	}

	return true
}

func (app *Application) IsNotReady() bool {
	GinkgoHelper()

	resp, err := http.Get("http://localhost:8082/readyz")
	Expect(err).ToNot(HaveOccurred())

	return resp.StatusCode != http.StatusOK
}
