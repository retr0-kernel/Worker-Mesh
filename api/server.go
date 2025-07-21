package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/retr0-kernel/worker-mesh/internal/worker"
)

type Server struct {
	echo *echo.Echo
	node *worker.Node
	port int
}

func NewServer(node *worker.Node, port int) *Server {
	e := echo.New()

	// Middleware
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())

	// Custom middleware to add request ID
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Response().Header().Set("X-Node-ID", node.ID)
			return next(c)
		}
	})

	// Create handlers
	handlers := NewHandlers(node)

	// Routes
	api := e.Group("/api/v1")
	{
		api.GET("/health", handlers.HealthCheck)
		api.GET("/status", handlers.GetStatus)
		api.GET("/peers", handlers.GetPeers)
		api.GET("/jobs", handlers.GetJobs)
		api.POST("/jobs", handlers.SubmitJob)
	}

	// Root endpoints
	e.GET("/", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"service": "worker-mesh",
			"node_id": node.ID,
			"version": "1.0.0",
			"endpoints": map[string]string{
				"health": "/api/v1/health",
				"status": "/api/v1/status",
				"peers":  "/api/v1/peers",
				"jobs":   "/api/v1/jobs",
			},
		})
	})

	return &Server{
		echo: e,
		node: node,
		port: port,
	}
}

func (s *Server) Start(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.port)
	fmt.Printf("Starting API server on %s\n", addr)

	// Start server in goroutine
	go func() {
		if err := s.echo.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Printf("API server error: %v\n", err)
		}
	}()

	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	fmt.Println("Shutting down API server...")

	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	return s.echo.Shutdown(shutdownCtx)
}
