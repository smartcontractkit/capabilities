package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/smartcontractkit/capabilities/echo/shared"
)

// Ensure Store implements Meterable at compile time.
var _ shared.Meterable = (*Store)(nil)

// Record represents the data stored for each key
type Record struct {
	Key           string    `json:"key"`
	Value         string    `json:"value"`
	StoredKey     string    `json:"stored_key"`
	LastUpdatedBy string    `json:"last_updated_by"` // workflowExecutionID
	LastUpdatedAt time.Time `json:"last_updated_at"`
	WorkflowID    string    `json:"workflow_id"`
	Owner         string    `json:"owner"` // workflowOwner
}

// StoreConfig contains configuration for the Store.
type StoreConfig struct {
	// Dir is the directory to store files.
	Dir string

	// ResourceInfo provides metadata for metering.
	ResourceInfo shared.ResourceInfo
}

// Store handles file-based storage for the echo capability.
// It implements shared.Meterable for resource utilization tracking.
type Store struct {
	dir          string
	resourceInfo shared.ResourceInfo

	mu          sync.RWMutex
	utilization map[string]*shared.Utilization // keyed by storedKey
}

// New creates a new Store with the given configuration.
func New(cfg StoreConfig) (*Store, error) {
	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}
	return &Store{
		dir:          cfg.Dir,
		resourceInfo: cfg.ResourceInfo,
		utilization:  make(map[string]*shared.Utilization),
	}, nil
}

// Put stores a record for the given stored key
func (s *Store) Put(storedKey string, record *Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal record: %w", err)
	}

	// Use a safe filename (replace : with _)
	filename := safeFilename(storedKey) + ".json"
	path := filepath.Join(s.dir, filename)

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	// Track utilization with bytes
	s.mu.Lock()
	s.utilization[storedKey] = &shared.Utilization{
		UtilizationType: shared.UtilizationType_UTILIZATION_TYPE_STORAGE,
		Owner:           record.Owner,
		WorkflowId:      record.WorkflowID,
		ResourceId:      storedKey,
		Bytes:           int64(len(data)), //leak in the abstraction, needs a more general thing
		                                   //maybe size
										   //include 
	}
	s.mu.Unlock()

	return nil
}

// Get retrieves a record for the given stored key
func (s *Store) Get(storedKey string) (*Record, error) {
	filename := safeFilename(storedKey) + ".json"
	path := filepath.Join(s.dir, filename)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("failed to unmarshal record: %w", err)
	}

	return &record, nil
}

// Delete removes a record for the given stored key
func (s *Store) Delete(storedKey string) error {
	filename := safeFilename(storedKey) + ".json"
	path := filepath.Join(s.dir, filename)

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	// Remove from utilization tracking
	s.mu.Lock()
	delete(s.utilization, storedKey)
	s.mu.Unlock()

	return nil
}

// GetUtilization implements shared.Meterable.
// Returns the current absolute utilization for all tracked resources.
func (s *Store) GetUtilization(_ context.Context) []*shared.Utilization {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*shared.Utilization, 0, len(s.utilization))
	for _, u := range s.utilization {
		result = append(result, u)
	}
	return result
}

// ResourceInfo implements shared.Meterable.
// Returns metadata about this resource for snapshot emission.
// share FQDN
func (s *Store) ResourceInfo() shared.ResourceInfo {
	return s.resourceInfo
}

// safeFilename converts a stored key to a safe filename
func safeFilename(storedKey string) string {
	// Replace : with _ to make it filesystem safe
	result := ""
	for _, c := range storedKey {
		if c == ':' {
			result += "_"
		} else {
			result += string(c)
		}
	}
	return result
}
