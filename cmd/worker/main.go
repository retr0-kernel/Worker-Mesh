package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/retr0-kernel/worker-mesh/api"
	"github.com/retr0-kernel/worker-mesh/internal/worker"
)

func main() {
	// Command line flags
	var (
		apiPort       = flag.Int("api-port", 3000, "API server port")
		discoveryPort = flag.Int("discovery-port", 8080, "UDP discovery port")
		help          = flag.Bool("help", false, "Show help")
	)
	flag.Parse()

	if *help {
		showHelp()
		return
	}

	// Create context that handles shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create worker node with user-specified discovery port
	fmt.Println("Initializing worker node...")
	node, err := worker.NewNode(*discoveryPort)
	if err != nil {
		fmt.Printf("Failed to create worker node: %v\n", err)
		os.Exit(1)
	}

	// Start worker node
	fmt.Println("Starting worker node...")
	if err := node.Start(ctx); err != nil {
		fmt.Printf("Failed to start worker node: %v\n", err)
		os.Exit(1)
	}

	// Create and start API server
	fmt.Println("Starting API server...")
	server := api.NewServer(node, *apiPort)
	if err := server.Start(ctx); err != nil {
		fmt.Printf("Failed to start API server: %v\n", err)
		err := node.Stop()
		if err != nil {
			return
		}
		os.Exit(1)
	}

	fmt.Printf("\n🚀 Worker Mesh Node started successfully!\n")
	fmt.Printf("📋 Node ID: %s\n", node.ID)
	fmt.Printf("🌐 API Server: http://localhost:%d\n", *apiPort)
	fmt.Printf("📡 Discovery Port: %d (UDP)\n", *discoveryPort)
	fmt.Printf("💾 Database: worker_%s.db\n", node.ID)
	fmt.Println("\n📖 API Endpoints:")
	fmt.Printf("   GET  /api/v1/health  - Health check\n")
	fmt.Printf("   GET  /api/v1/status  - Node status\n")
	fmt.Printf("   GET  /api/v1/peers   - Known peers\n")
	fmt.Printf("   GET  /api/v1/jobs    - Job history\n")
	fmt.Printf("   POST /api/v1/jobs    - Submit job\n")
	fmt.Println("\nPress Ctrl+C to shutdown...")

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\n🛑 Shutdown signal received, gracefully stopping...")

	cancel()

	if err := server.Stop(ctx); err != nil {
		fmt.Printf("Error stopping API server: %v\n", err)
	}
	if err := node.Stop(); err != nil {
		fmt.Printf("Error stopping worker node: %v\n", err)
	}
	fmt.Println("✅ Shutdown complete")
}

func showHelp() {
	fmt.Println(`
Worker Mesh - Zero-Config Distributed Worker System

USAGE:
    worker [OPTIONS]

OPTIONS:
    -api-port int    Port for REST API server (default: 3000)
    -help           Show this help message

EXAMPLES:
    # Start with default settings
    ./worker

    # Start with custom API port
    ./worker -api-port 8080

    # Submit a job via API
    curl -X POST http://localhost:3000/api/v1/jobs \
      -H "Content-Type: application/json" \
      -d '{"command": "echo Hello from worker mesh!"}'

    # Check node status
    curl http://localhost:3000/api/v1/status

    # View known peers
    curl http://localhost:3000/api/v1/peers
`)
}
