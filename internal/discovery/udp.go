/*

Listens for UDP broadcasts from other nodes
Broadcasts its own heartbeat every 5 seconds
Tracks all known peers in a thread-safe map
Cleans up stale nodes that haven't been seen in 30+ seconds

*/

package discovery

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	pb "github.com/retr0-kernel/worker-mesh/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	BroadcastInterval = 5 * time.Second
	NodeTimeout       = 30 * time.Second
)

type UDPDiscovery struct {
	nodeID       string
	address      string
	port         int
	conn         *net.UDPConn
	peers        map[string]*pb.NodeInfo
	peersMutex   sync.RWMutex
	onPeerUpdate func(*pb.NodeInfo) // Callback when peer info changes
}

func NewUDPDiscovery(nodeID, address string, port int) *UDPDiscovery {
	return &UDPDiscovery{
		nodeID:  nodeID,
		address: address,
		port:    port,
		peers:   make(map[string]*pb.NodeInfo),
	}
}

// Start begins UDP discovery (listening and broadcasting)
func (d *UDPDiscovery) Start(ctx context.Context) error {
	// Setup UDP connection for broadcasting
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf(":%d", d.port))
	if err != nil {
		return fmt.Errorf("failed to resolve UDP address: %w", err)
	}

	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on UDP: %w", err)
	}
	d.conn = conn

	// Start listener goroutine
	go d.listen(ctx)

	// Start broadcaster goroutine
	go d.broadcast(ctx)

	// Start peer cleanup goroutine
	go d.cleanupStaleNodes(ctx)

	return nil
}

// listen handles incoming UDP messages
func (d *UDPDiscovery) listen(ctx context.Context) {
	buffer := make([]byte, 1024)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			err := d.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			if err != nil {
				return
			}
			n, _, err := d.conn.ReadFromUDP(buffer)
			if err != nil {
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					continue // Timeout is expected due to deadline
				}
				fmt.Printf("UDP read error: %v\n", err)
				continue
			}

			// Parse protobuf message
			var meshMsg pb.MeshMessage
			if err := proto.Unmarshal(buffer[:n], &meshMsg); err != nil {
				fmt.Printf("Failed to unmarshal message: %v\n", err)
				continue
			}

			// Handle heartbeat messages
			if meshMsg.Type == pb.MessageType_HEARTBEAT {
				if nodeInfo := meshMsg.GetNodeInfo(); nodeInfo != nil {
					d.updatePeer(nodeInfo)
				}
			}
		}
	}
}

// broadcast sends periodic heartbeat messages
func (d *UDPDiscovery) broadcast(ctx context.Context) {
	ticker := time.NewTicker(BroadcastInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.sendHeartbeat(); err != nil {
				fmt.Printf("Failed to send heartbeat: %v\n", err)
			}
		}
	}
}

func (d *UDPDiscovery) sendHeartbeat() error {
	// Create NodeInfo message
	nodeInfo := &pb.NodeInfo{
		NodeId:        d.nodeID,
		Address:       d.address,
		Status:        pb.NodeStatus_IDLE,
		LastHeartbeat: timestamppb.Now(),
		Version:       "1.0.0",
		Metadata:      make(map[string]string),
	}

	// Wrap in MeshMessage
	meshMsg := &pb.MeshMessage{
		Type:         pb.MessageType_HEARTBEAT,
		Payload:      &pb.MeshMessage_NodeInfo{NodeInfo: nodeInfo},
		Timestamp:    timestamppb.Now(),
		SenderNodeId: d.nodeID,
	}

	// Serialize to protobuf
	data, err := proto.Marshal(meshMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Instead of broadcast, send to localhost for testing
	//targetAddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("127.0.0.1:%d", DiscoveryPort))
	targetAddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("224.0.0.1:%d", d.port))

	if err != nil {
		return err
	}

	// Create a separate connection for sending
	sendConn, err := net.DialUDP("udp4", nil, targetAddr)
	if err != nil {
		return fmt.Errorf("failed to create send connection: %w", err)
	}
	defer func(sendConn *net.UDPConn) {
		err := sendConn.Close()
		if err != nil {

		}
	}(sendConn)

	_, err = sendConn.Write(data)
	return err
}

func (d *UDPDiscovery) updatePeer(nodeInfo *pb.NodeInfo) {
	// Don't track ourselves
	if nodeInfo.NodeId == d.nodeID {
		return
	}

	d.peersMutex.Lock()
	d.peers[nodeInfo.NodeId] = nodeInfo
	d.peersMutex.Unlock()

	// Notify callback if set
	if d.onPeerUpdate != nil {
		d.onPeerUpdate(nodeInfo)
	}

	fmt.Printf("Updated peer: %s at %s (status: %s)\n",
		nodeInfo.NodeId, nodeInfo.Address, nodeInfo.Status)
}

// GetPeers returns current known peers
func (d *UDPDiscovery) GetPeers() map[string]*pb.NodeInfo {
	d.peersMutex.RLock()
	defer d.peersMutex.RUnlock()

	result := make(map[string]*pb.NodeInfo)
	for k, v := range d.peers {
		result[k] = v
	}
	return result
}

// cleanupStaleNodes removes nodes that haven't sent heartbeats recently
func (d *UDPDiscovery) cleanupStaleNodes(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			d.peersMutex.Lock()
			for nodeID, nodeInfo := range d.peers {
				lastSeen := nodeInfo.LastHeartbeat.AsTime()
				if now.Sub(lastSeen) > NodeTimeout {
					delete(d.peers, nodeID)
					fmt.Printf("Removed stale node: %s (last seen %v ago)\n",
						nodeID, now.Sub(lastSeen))
				}
			}
			d.peersMutex.Unlock()
		}
	}
}

// SetPeerUpdateCallback sets a callback for when peer info changes
func (d *UDPDiscovery) SetPeerUpdateCallback(callback func(*pb.NodeInfo)) {
	d.onPeerUpdate = callback
}

// Stop closes the UDP connection
func (d *UDPDiscovery) Stop() error {
	if d.conn != nil {
		return d.conn.Close()
	}
	return nil
}
