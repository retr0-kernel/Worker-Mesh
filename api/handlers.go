package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/retr0-kernel/worker-mesh/internal/worker"
	pb "github.com/retr0-kernel/worker-mesh/proto"
)

type Handlers struct {
	node *worker.Node
}

func NewHandlers(node *worker.Node) *Handlers {
	return &Handlers{node: node}
}

// StatusResponse represents the current node status
type StatusResponse struct {
	NodeID    string            `json:"node_id"`
	Address   string            `json:"address"`
	Status    string            `json:"status"`
	Peers     int               `json:"peer_count"`
	Metadata  map[string]string `json:"metadata"`
	Timestamp string            `json:"timestamp"`
}

// PeerInfo represents information about a peer node
type PeerInfo struct {
	NodeID        string            `json:"node_id"`
	Address       string            `json:"address"`
	Status        string            `json:"status"`
	LastHeartbeat string            `json:"last_heartbeat"`
	Version       string            `json:"version"`
	Metadata      map[string]string `json:"metadata"`
}

// JobRequest represents a job submission request
type JobRequest struct {
	TargetNodeID   string            `json:"target_node_id"`
	Command        string            `json:"command"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int32             `json:"timeout_seconds,omitempty"`
}

// JobResponse represents a job status response
type JobResponse struct {
	JobID        string  `json:"job_id"`
	TargetNodeID string  `json:"target_node_id"`
	Command      string  `json:"command"`
	Status       string  `json:"status"`
	ExitCode     *int32  `json:"exit_code,omitempty"`
	Stdout       string  `json:"stdout,omitempty"`
	Stderr       string  `json:"stderr,omitempty"`
	ErrorMessage string  `json:"error_message,omitempty"`
	CreatedAt    string  `json:"created_at"`
	StartedAt    *string `json:"started_at,omitempty"`
	FinishedAt   *string `json:"finished_at,omitempty"`
}

// GetStatus returns the current node status
func (h *Handlers) GetStatus(c echo.Context) error {
	nodeInfo := h.node.GetNodeInfo()
	peers := h.node.GetPeers()

	response := StatusResponse{
		NodeID:    nodeInfo.NodeId,
		Address:   nodeInfo.Address,
		Status:    nodeInfo.Status.String(),
		Peers:     len(peers),
		Metadata:  nodeInfo.Metadata,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	return c.JSON(http.StatusOK, response)
}

// GetPeers returns information about known peer nodes
func (h *Handlers) GetPeers(c echo.Context) error {
	peers := h.node.GetPeers()
	peerList := make([]PeerInfo, 0, len(peers))

	for _, peer := range peers {
		peerInfo := PeerInfo{
			NodeID:        peer.NodeId,
			Address:       peer.Address,
			Status:        peer.Status.String(),
			LastHeartbeat: peer.LastHeartbeat.AsTime().Format(time.RFC3339),
			Version:       peer.Version,
			Metadata:      peer.Metadata,
		}
		peerList = append(peerList, peerInfo)
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"peers": peerList,
		"count": len(peerList),
	})
}

// SubmitJob handles job submission requests
func (h *Handlers) SubmitJob(c echo.Context) error {
	var req JobRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	// Validate required fields
	if req.Command == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "Command is required",
		})
	}

	// Default timeout
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 300 // 5 minutes default
	}

	// Generate job ID
	jobID := uuid.New().String()

	// If no target specified, assign to this node
	targetNodeID := req.TargetNodeID
	if targetNodeID == "" {
		targetNodeID = h.node.ID
	}

	// Create job
	job := &pb.Job{
		JobId:          jobID,
		TargetNodeId:   targetNodeID,
		Command:        req.Command,
		Env:            req.Env,
		TimeoutSeconds: req.TimeoutSeconds,
		Status:         pb.JobStatus_JOB_PENDING,
		CreatedAt:      timestamppb.Now(),
		CreatedByNode:  h.node.ID,
	}

	// Submit job
	if err := h.node.SubmitJob(job); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to submit job: " + err.Error(),
		})
	}

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"job_id":      jobID,
		"target_node": targetNodeID,
		"command":     req.Command,
		"status":      job.Status.String(),
		"created_at":  job.CreatedAt.AsTime().Format(time.RFC3339),
	})
}

// GetJobs returns job history
// GetJobs returns job history
func (h *Handlers) GetJobs(c echo.Context) error {
	// Parse limit parameter
	limitStr := c.QueryParam("limit")
	limit := 50 // default
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	// Get jobs from database
	jobs, err := h.node.GetJobHistory(limit)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to fetch jobs",
		})
	}

	// Convert to response format
	jobList := make([]JobResponse, 0, len(jobs))
	for _, job := range jobs {
		jobResp := JobResponse{
			JobID:        job.JobID,
			TargetNodeID: job.TargetNodeID,
			Command:      job.Command,
			Status:       job.Status,
			CreatedAt:    job.CreatedAt.Format(time.RFC3339),
		}

		// Handle optional fields properly
		if job.ExitCode != 0 { // Check if exit code was set
			exitCode := job.ExitCode
			jobResp.ExitCode = &exitCode
		}

		if job.Stdout != "" { // Check if stdout is not empty
			jobResp.Stdout = job.Stdout
		}

		if job.Stderr != "" { // Check if stderr is not empty
			jobResp.Stderr = job.Stderr
		}

		if job.ErrorMessage != "" { // Check if error message is not empty
			jobResp.ErrorMessage = job.ErrorMessage
		}

		// Handle time fields - check if they're not zero time
		if !job.StartedAt.IsZero() {
			startedStr := job.StartedAt.Format(time.RFC3339)
			jobResp.StartedAt = &startedStr
		}

		if !job.FinishedAt.IsZero() {
			finishedStr := job.FinishedAt.Format(time.RFC3339)
			jobResp.FinishedAt = &finishedStr
		}

		jobList = append(jobList, jobResp)
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"jobs":  jobList,
		"count": len(jobList),
	})
}

// HealthCheck returns a simple health check response
func (h *Handlers) HealthCheck(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
		"node_id":   h.node.ID,
	})
}
