package memory

import "sync"

// StoreConfig configures memory storage paths without relying on package globals.
type StoreConfig struct {
	VaultDir  string
	FTSDBPath string
}

// Store exposes memory operations bound to an explicit config.
type Store struct {
	config StoreConfig
}

var configuredStoreMu sync.Mutex

// NewStore creates a memory store using explicit paths where provided.
func NewStore(config StoreConfig) *Store {
	if config.VaultDir == "" {
		config.VaultDir = defaultVaultDir()
	}
	if config.FTSDBPath == "" {
		config.FTSDBPath = defaultFTSDB()
	}
	return &Store{config: config}
}

func (s *Store) withConfig(fn func() error) error {
	configuredStoreMu.Lock()
	defer configuredStoreMu.Unlock()

	prevVault := VaultDir
	prevFTS := ftsDB
	_ = CloseFTS()
	VaultDir = s.config.VaultDir
	ftsDB = s.config.FTSDBPath
	defer func() {
		_ = CloseFTS()
		VaultDir = prevVault
		ftsDB = prevFTS
	}()
	return fn()
}

func (s *Store) Ingest(content, memType, project string, tags []string, source, title string) (Result, error) {
	var result Result
	err := s.withConfig(func() error {
		var err error
		result, err = IngestWithOptions(content, IngestOptions{
			Type:    memType,
			Project: project,
			Tags:    tags,
			Source:  source,
			Title:   title,
		})
		return err
	})
	return result, err
}

// IngestWithOptions binds the options-based ingest to this store's config, so
// workflow memory paths can persist layer/provenance without global VaultDir
// drift.
func (s *Store) IngestWithOptions(content string, opts IngestOptions) (Result, error) {
	var result Result
	err := s.withConfig(func() error {
		var err error
		result, err = IngestWithOptions(content, opts)
		return err
	})
	return result, err
}

func (s *Store) Recall(query, project, memType string, tags []string, minScore float64, status string, limit int, downrankConflicts ...bool) ([]Memory, int, error) {
	var memories []Memory
	var total int
	err := s.withConfig(func() error {
		var err error
		memories, total, err = Recall(query, project, memType, tags, minScore, status, limit, downrankConflicts...)
		return err
	})
	return memories, total, err
}

func (s *Store) Cleanup() (CleanupResult, error) {
	var result CleanupResult
	err := s.withConfig(func() error {
		var err error
		result, err = Cleanup()
		return err
	})
	return result, err
}

func (s *Store) Stats() (total, active, archived, superseded int, byType map[string]int, err error) {
	err = s.withConfig(func() error {
		total, active, archived, superseded, byType, err = Stats()
		return err
	})
	return total, active, archived, superseded, byType, err
}
