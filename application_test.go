package application

import (
	"context"
	"testing"

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
