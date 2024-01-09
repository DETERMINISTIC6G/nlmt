package nlmt

import (
	"net"
	"sync"
	"time"
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

	np := &NetPrint{conn: conn}

	// Start a goroutine to send packets every second
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for range ticker.C {
			// Lock the mutex before writing to the connection
			np.mu.Lock()
			_, err := np.conn.Write([]byte("test\n"))
			np.mu.Unlock()

			if err != nil {
				// Handle error (e.g., log it)
				np.CloseConnection()
				panic(err)
			}
		}
	}()

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
