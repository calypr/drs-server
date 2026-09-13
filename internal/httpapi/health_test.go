package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestHealthReadinessTracksServingAndDependency(t *testing.T) {
	var checks atomic.Int32
	dependencyUp := atomic.Bool{}
	dependencyUp.Store(true)
	health := NewHealth(func(_ context.Context) error {
		checks.Add(1)
		if !dependencyUp.Load() {
			return errors.New("database unavailable")
		}
		return nil
	}, time.Second)
	app := fiber.New()
	app.Get(RouteHealthz, health.live)
	app.Get(RouteLivez, health.live)
	app.Get(RouteReadyz, health.ready)

	for _, path := range []string{RouteHealthz, RouteLivez} {
		response, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, response.StatusCode)
		}
		_ = response.Body.Close()
	}
	response, err := app.Test(httptest.NewRequest(http.MethodGet, RouteReadyz, nil))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("ready status = %d, want 200", response.StatusCode)
	}
	_ = response.Body.Close()
	if checks.Load() != 1 {
		t.Fatalf("dependency checks = %d, want 1", checks.Load())
	}

	dependencyUp.Store(false)
	response, err = app.Test(httptest.NewRequest(http.MethodGet, RouteReadyz, nil))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("down dependency status = %d, want 503", response.StatusCode)
	}
	_ = response.Body.Close()

	health.StopServing()
	response, err = app.Test(httptest.NewRequest(http.MethodGet, RouteReadyz, nil))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("stopping status = %d, want 503", response.StatusCode)
	}
	_ = response.Body.Close()
	if checks.Load() != 2 {
		t.Fatalf("dependency checks after stopping = %d, want 2", checks.Load())
	}
}
