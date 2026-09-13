package httpapi

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v3"
)

// Health owns process-serving state separately from dependency readiness.
// Liveness never performs I/O; readiness performs one bounded dependency
// check and is flipped false before owned resources are closed.
type Health struct {
	serving atomic.Bool
	check   func(context.Context) error
	timeout time.Duration
}

func NewHealth(check func(context.Context) error, timeout time.Duration) *Health {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	health := &Health{check: check, timeout: timeout}
	health.serving.Store(true)
	return health
}

func (h *Health) StopServing() {
	if h != nil {
		h.serving.Store(false)
	}
}

func (h *Health) live(c fiber.Ctx) error {
	return c.SendString("OK")
}

func (h *Health) ready(c fiber.Ctx) error {
	if h == nil || !h.serving.Load() {
		return readyError(c, "server is stopping")
	}
	if h.check != nil {
		ctx, cancel := context.WithTimeout(c.Context(), h.timeout)
		defer cancel()
		if err := h.check(ctx); err != nil {
			return readyError(c, "database is unavailable")
		}
	}
	return c.JSON(map[string]string{"status": "ready"})
}

func readyError(c fiber.Ctx, reason string) error {
	return c.Status(fiber.StatusServiceUnavailable).JSON(map[string]string{"status": "not_ready", "reason": reason})
}
