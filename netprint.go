package nlmt

import (
	"net"
	"sync"
)

// NetPrint represents a connection manager
type NetPrint struct {
	conn net.Conn
	mu   sync.Mutex // Mutex for synchronization
}

// NewConnectionManager creates a new NetPrint and establishes a connection to the given address
func NewNetPrint(destAddr string) (*NetPrint, error) {
	conn, err := net.Dial("tcp", destAddr)
	if err != nil {
		return nil, err
	}

	return &NetPrint{conn: conn}, nil
}

// WriteToConnection writes the provided message to the connection managed by NetPrint
func (cm *NetPrint) WriteToConnection(message string) error {
	// Lock the mutex before writing to the connection
	cm.mu.Lock()
	defer cm.mu.Unlock()

	_, err := cm.conn.Write([]byte(message))
	return err
}

// CloseConnection closes the connection managed by NetPrint
func (cm *NetPrint) CloseConnection() error {
	// Lock the mutex before closing the connection
	cm.mu.Lock()
	defer cm.mu.Unlock()

	return cm.conn.Close()
}
