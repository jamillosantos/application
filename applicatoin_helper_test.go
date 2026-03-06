package application

func (app *Application) IsRunning() bool {
	app.stateM.Lock()
	defer app.stateM.Unlock()
	return app.state == stateRunning
}
