package worker

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"entgo.io/ent/dialect"
	_ "github.com/mattn/go-sqlite3"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/retr0-kernel/worker-mesh/ent"
	"github.com/retr0-kernel/worker-mesh/ent/job"
	"github.com/retr0-kernel/worker-mesh/ent/workernode"
	"github.com/retr0-kernel/worker-mesh/internal/discovery"
	pb "github.com/retr0-kernel/worker-mesh/proto"
)

type Node struct {
	ID      string
	Address string
	Port    int

	// Components
	db        *ent.Client
	discovery *discovery.UDPDiscovery

	// State
	status   pb.NodeStatus
	statusMu sync.RWMutex

	// Job execution
	jobQueue    chan *pb.Job
	jobsMu      sync.RWMutex
	runningJobs map[string]context.CancelFunc
}

// NewNode creates a new worker node
func NewNode(port int) (*Node, error) {
	// Generate unique node ID
	nodeID, err := generateNodeID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate node ID: %w", err)
	}

	// Get local IP address
	address, err := getLocalIP()
	if err != nil {
		return nil, fmt.Errorf("failed to get local IP: %w", err)
	}

	fullAddress := fmt.Sprintf("%s:%d", address, port)

	// Setup database
	db, err := setupDatabase(nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to setup database: %w", err)
	}

	// Create discovery service
	disco := discovery.NewUDPDiscovery(nodeID, fullAddress, port)

	node := &Node{
		ID:          nodeID,
		Address:     fullAddress,
		Port:        port,
		db:          db,
		discovery:   disco,
		status:      pb.NodeStatus_IDLE,
		jobQueue:    make(chan *pb.Job, 100), // Buffer for jobs
		runningJobs: make(map[string]context.CancelFunc),
	}

	// Set up discovery callback
	disco.SetPeerUpdateCallback(node.onPeerUpdate)

	return node, nil
}

// Start initializes and starts the worker node
func (n *Node) Start(ctx context.Context) error {
	fmt.Printf("Starting worker node %s at %s\n", n.ID, n.Address)

	// Start discovery
	if err := n.discovery.Start(ctx); err != nil {
		return fmt.Errorf("failed to start discovery: %w", err)
	}

	// Start job processor
	go n.processJobs(ctx)

	// Register this node in database
	if err := n.registerSelf(ctx); err != nil {
		return fmt.Errorf("failed to register self: %w", err)
	}

	fmt.Printf("Worker node %s started successfully\n", n.ID)
	return nil
}

// processJobs handles incoming job execution
func (n *Node) processJobs(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-n.jobQueue:
			// Execute job in goroutine to avoid blocking
			go n.executeJob(ctx, job)
		}
	}
}

// executeJob runs a job and stores the result
func (n *Node) executeJob(ctx context.Context, job *pb.Job) {
	fmt.Printf("Executing job %s: %s\n", job.JobId, job.Command)

	// Update job status to running
	err := n.updateJobStatus(ctx, job.JobId, pb.JobStatus_JOB_RUNNING)
	if err != nil {
		return
	}

	// Set node as busy
	n.setStatus(pb.NodeStatus_BUSY)
	defer n.setStatus(pb.NodeStatus_IDLE)

	// Create timeout context
	jobCtx, cancel := context.WithTimeout(ctx, time.Duration(job.TimeoutSeconds)*time.Second)
	defer cancel()

	// Store cancel function for potential job termination
	n.jobsMu.Lock()
	n.runningJobs[job.JobId] = cancel
	n.jobsMu.Unlock()

	// Clean up cancel function when done
	defer func() {
		n.jobsMu.Lock()
		delete(n.runningJobs, job.JobId)
		n.jobsMu.Unlock()
	}()

	// Execute the command
	result := n.runCommand(jobCtx, job)

	// Store result in database
	if err := n.storeJobResult(ctx, job, result); err != nil {
		fmt.Printf("Failed to store job result: %v\n", err)
	}

	fmt.Printf("Job %s completed with exit code %d\n", job.JobId, result.ExitCode)
}

// runCommand executes a shell command and returns the result
func (n *Node) runCommand(ctx context.Context, job *pb.Job) *pb.JobResult {
	startTime := time.Now()

	result := &pb.JobResult{
		JobId:          job.JobId,
		ExecutorNodeId: n.ID,
		StartedAt:      timestamppb.New(startTime),
	}

	// Parse command (split by spaces for now, could be more sophisticated)
	parts := strings.Fields(job.Command)
	if len(parts) == 0 {
		result.ExitCode = -1
		result.ErrorMessage = "empty command"
		result.FinishedAt = timestamppb.New(time.Now())
		return result
	}

	// Create command
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)

	// Set environment variables
	if job.Env != nil {
		env := os.Environ()
		for k, v := range job.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = env
	}

	// Capture output
	stdout, err := cmd.Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			result.ExitCode = int32(exitError.ExitCode())
			result.Stderr = string(exitError.Stderr)
		}
	} else {
		result.ExitCode = 0
	}

	result.Stdout = string(stdout)
	result.FinishedAt = timestamppb.New(time.Now())

	return result
}

// SubmitJob adds a job to the queue
func (n *Node) SubmitJob(job *pb.Job) error {
	// Store job in database
	_, err := n.db.Job.Create().
		SetJobID(job.JobId).
		SetTargetNodeID(job.TargetNodeId).
		SetCommand(job.Command).
		SetEnv(job.Env).
		SetTimeoutSeconds(job.TimeoutSeconds).
		SetStatus(job.Status.String()).
		SetCreatedByNode(job.CreatedByNode).
		Save(context.Background())

	if err != nil {
		return fmt.Errorf("failed to store job: %w", err)
	}

	// Add to processing queue
	select {
	case n.jobQueue <- job:
		return nil
	default:
		return fmt.Errorf("job queue is full")
	}
}

// GetStatus returns current node status
func (n *Node) GetStatus() pb.NodeStatus {
	n.statusMu.RLock()
	defer n.statusMu.RUnlock()
	return n.status
}

// setStatus updates the node status
func (n *Node) setStatus(status pb.NodeStatus) {
	n.statusMu.Lock()
	n.status = status
	n.statusMu.Unlock()
}

// GetPeers returns known peer nodes
func (n *Node) GetPeers() map[string]*pb.NodeInfo {
	return n.discovery.GetPeers()
}

// onPeerUpdate handles updates from discovery service
func (n *Node) onPeerUpdate(nodeInfo *pb.NodeInfo) {
	// Try to update existing record first
	updated, err := n.db.WorkerNode.Update().
		Where(workernode.NodeIDEQ(nodeInfo.NodeId)).
		SetAddress(nodeInfo.Address).
		SetStatus(nodeInfo.Status.String()).
		SetLastHeartbeat(nodeInfo.LastHeartbeat.AsTime()).
		SetMetadata(nodeInfo.Metadata).
		SetVersion(nodeInfo.Version).
		SetUpdatedAt(time.Now()).
		Save(context.Background())

	if err != nil || updated == 0 {
		// Record doesn't exist, create new one
		_, err = n.db.WorkerNode.Create().
			SetNodeID(nodeInfo.NodeId).
			SetAddress(nodeInfo.Address).
			SetStatus(nodeInfo.Status.String()).
			SetLastHeartbeat(nodeInfo.LastHeartbeat.AsTime()).
			SetMetadata(nodeInfo.Metadata).
			SetVersion(nodeInfo.Version).
			Save(context.Background())

		if err != nil {
			fmt.Printf("Failed to create peer in database: %v\n", err)
		}
	}
}

// Helper functions

func generateNodeID() (string, error) {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("node-%x", bytes), nil
}

func getLocalIP() (string, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "", err
	}
	defer func(conn net.Conn) {
		err := conn.Close()
		if err != nil {

		}
	}(conn)

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String(), nil
}

func setupDatabase(nodeID string) (*ent.Client, error) {
	dbPath := fmt.Sprintf("worker_%s.db", nodeID)
	client, err := ent.Open(dialect.SQLite, fmt.Sprintf("file:%s?cache=shared&_fk=1", dbPath))
	if err != nil {
		return nil, err
	}

	// Run migrations
	if err := client.Schema.Create(context.Background()); err != nil {
		err := client.Close()
		if err != nil {
			return nil, err
		}
		return nil, err
	}

	return client, nil
}

func (n *Node) registerSelf(ctx context.Context) error {
	// Try to update existing record first
	updated, err := n.db.WorkerNode.Update().
		Where(workernode.NodeIDEQ(n.ID)).
		SetAddress(n.Address).
		SetStatus(n.status.String()).
		SetVersion("1.0.0").
		SetMetadata(map[string]string{
			"hostname": getHostname(),
			"pid":      fmt.Sprintf("%d", os.Getpid()),
		}).
		SetUpdatedAt(time.Now()).
		Save(ctx)

	if err != nil || updated == 0 {
		// Record doesn't exist, create new one
		_, err = n.db.WorkerNode.Create().
			SetNodeID(n.ID).
			SetAddress(n.Address).
			SetStatus(n.status.String()).
			SetVersion("1.0.0").
			SetMetadata(map[string]string{
				"hostname": getHostname(),
				"pid":      fmt.Sprintf("%d", os.Getpid()),
			}).
			Save(ctx)
	}

	return err
}

func (n *Node) updateJobStatus(ctx context.Context, jobID string, status pb.JobStatus) error {
	_, err := n.db.Job.Update().
		Where(job.JobIDEQ(jobID)).
		SetStatus(status.String()).
		SetUpdatedAt(time.Now()).
		Save(ctx)

	return err
}

func (n *Node) storeJobResult(ctx context.Context, jobReq *pb.Job, result *pb.JobResult) error {
	updates := n.db.Job.Update().
		Where(job.JobIDEQ(jobReq.JobId)).
		SetStatus(pb.JobStatus_JOB_COMPLETED.String()).
		SetExitCode(result.ExitCode).
		SetUpdatedAt(time.Now())

	if result.Stdout != "" {
		updates = updates.SetStdout(result.Stdout)
	}
	if result.Stderr != "" {
		updates = updates.SetStderr(result.Stderr)
	}
	if result.ErrorMessage != "" {
		updates = updates.SetErrorMessage(result.ErrorMessage)
	}
	if result.StartedAt != nil {
		updates = updates.SetStartedAt(result.StartedAt.AsTime())
	}
	if result.FinishedAt != nil {
		updates = updates.SetFinishedAt(result.FinishedAt.AsTime())
	}

	_, err := updates.Save(ctx)
	return err
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}

// Stop gracefully shuts down the node
func (n *Node) Stop() error {
	fmt.Printf("Shutting down worker node %s\n", n.ID)

	// Set status to draining
	n.setStatus(pb.NodeStatus_DRAINING)

	// Cancel all running jobs
	n.jobsMu.Lock()
	for jobID, cancel := range n.runningJobs {
		fmt.Printf("Cancelling running job: %s\n", jobID)
		cancel()
	}
	n.jobsMu.Unlock()

	// Stop discovery
	if err := n.discovery.Stop(); err != nil {
		fmt.Printf("Error stopping discovery: %v\n", err)
	}

	// Close database
	if err := n.db.Close(); err != nil {
		fmt.Printf("Error closing database: %v\n", err)
	}

	return nil
}

// GetJobHistory returns recent job history
func (n *Node) GetJobHistory(limit int) ([]*ent.Job, error) {
	return n.db.Job.Query().
		Order(ent.Desc(job.FieldCreatedAt)).
		Limit(limit).
		All(context.Background())
}

// GetNodeInfo returns current node information as protobuf
func (n *Node) GetNodeInfo() *pb.NodeInfo {
	return &pb.NodeInfo{
		NodeId:        n.ID,
		Address:       n.Address,
		Status:        n.GetStatus(),
		LastHeartbeat: timestamppb.Now(),
		Version:       "1.0.0",
		Metadata: map[string]string{
			"hostname": getHostname(),
			"pid":      fmt.Sprintf("%d", os.Getpid()),
		},
	}
}
