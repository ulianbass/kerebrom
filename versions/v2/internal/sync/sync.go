package sync

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ulianbass/kerebrom/internal/store/sqlite"
)

const (
	ManifestFile = "manifest.json"
	ChunksDir    = "chunks"
)

type Options struct {
	Dir     string
	Project string
	All     bool
}

type Chunk struct {
	ID        string               `json:"id"`
	File      string               `json:"file"`
	CreatedAt string               `json:"created_at"`
	Project   string               `json:"project,omitempty"`
	All       bool                 `json:"all"`
	Since     string               `json:"since,omitempty"`
	Counts    sqlite.ImportSummary `json:"counts"`
}

type Manifest struct {
	App       string  `json:"app"`
	Schema    int     `json:"schema"`
	UpdatedAt string  `json:"updated_at"`
	Chunks    []Chunk `json:"chunks"`
}

type ExportResult struct {
	Created bool  `json:"created"`
	Chunk   Chunk `json:"chunk"`
}

type ImportResult struct {
	Imported int                  `json:"imported"`
	Skipped  int                  `json:"skipped"`
	Counts   sqlite.ImportSummary `json:"counts"`
}

type Status struct {
	Dir            string `json:"dir"`
	ChunkCount     int    `json:"chunk_count"`
	PendingImports int    `json:"pending_imports"`
}

type chunkRecord struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func Export(ctx context.Context, store *sqlite.Store, opts Options) (ExportResult, error) {
	opts = normalizeOptions(opts)
	manifest, err := LoadManifest(opts.Dir)
	if err != nil {
		return ExportResult{}, err
	}

	since := ""
	if !opts.All {
		since = latestExportTime(manifest, opts.Project)
	}

	data, err := store.ExportData(ctx, sqlite.ExportOptions{
		Project: opts.Project,
		Since:   since,
	})
	if err != nil {
		return ExportResult{}, err
	}

	counts := sqlite.ImportSummary{
		Sessions:     len(data.Sessions),
		Observations: len(data.Observations),
		Prompts:      len(data.Prompts),
		Aliases:      len(data.ProjectAliases),
	}
	if counts.Sessions == 0 && counts.Observations == 0 && counts.Prompts == 0 && counts.Aliases == 0 {
		return ExportResult{Created: false}, nil
	}

	raw, err := encodeJSONL(data)
	if err != nil {
		return ExportResult{}, err
	}
	hash := sha256.Sum256(raw)
	chunkID := hex.EncodeToString(hash[:])[:16]
	chunkFile := filepath.ToSlash(filepath.Join(ChunksDir, chunkID+".jsonl.gz"))

	if err := os.MkdirAll(filepath.Join(opts.Dir, ChunksDir), 0o755); err != nil {
		return ExportResult{}, fmt.Errorf("create sync chunks dir: %w", err)
	}
	if err := writeGzip(filepath.Join(opts.Dir, chunkFile), raw); err != nil {
		return ExportResult{}, err
	}

	chunk := Chunk{
		ID:        chunkID,
		File:      chunkFile,
		CreatedAt: data.ExportedAt,
		Project:   strings.TrimSpace(opts.Project),
		All:       opts.All,
		Since:     since,
		Counts:    counts,
	}
	manifest = upsertChunk(manifest, chunk)
	if err := SaveManifest(opts.Dir, manifest); err != nil {
		return ExportResult{}, err
	}
	if err := store.MarkSyncChunkImported(ctx, chunk.ID); err != nil {
		return ExportResult{}, err
	}

	return ExportResult{Created: true, Chunk: chunk}, nil
}

func Import(ctx context.Context, store *sqlite.Store, opts Options) (ImportResult, error) {
	opts = normalizeOptions(opts)
	manifest, err := LoadManifest(opts.Dir)
	if err != nil {
		return ImportResult{}, err
	}

	var result ImportResult
	for _, chunk := range manifest.Chunks {
		imported, err := store.SyncChunkImported(ctx, chunk.ID)
		if err != nil {
			return ImportResult{}, err
		}
		if imported {
			result.Skipped++
			continue
		}

		data, err := readChunk(opts.Dir, chunk)
		if err != nil {
			return ImportResult{}, err
		}
		summary, err := store.ImportData(ctx, data)
		if err != nil {
			return ImportResult{}, err
		}
		if err := store.MarkSyncChunkImported(ctx, chunk.ID); err != nil {
			return ImportResult{}, err
		}
		result.Imported++
		result.Counts.Aliases += summary.Aliases
		result.Counts.Sessions += summary.Sessions
		result.Counts.Observations += summary.Observations
		result.Counts.Prompts += summary.Prompts
	}

	return result, nil
}

func Inspect(ctx context.Context, store *sqlite.Store, dir string) (Status, error) {
	dir = defaultDir(dir)
	manifest, err := LoadManifest(dir)
	if err != nil {
		return Status{}, err
	}

	status := Status{Dir: dir, ChunkCount: len(manifest.Chunks)}
	for _, chunk := range manifest.Chunks {
		imported, err := store.SyncChunkImported(ctx, chunk.ID)
		if err != nil {
			return Status{}, err
		}
		if !imported {
			status.PendingImports++
		}
	}
	return status, nil
}

func LoadManifest(dir string) (Manifest, error) {
	dir = defaultDir(dir)
	path := filepath.Join(dir, ManifestFile)
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Manifest{App: "kerebrom", Schema: 1, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}, nil
		}
		return Manifest{}, fmt.Errorf("read sync manifest: %w", err)
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return Manifest{App: "kerebrom", Schema: 1, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}, nil
	}

	var manifest Manifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode sync manifest: %w", err)
	}
	if manifest.App == "" {
		manifest.App = "kerebrom"
	}
	if manifest.Schema == 0 {
		manifest.Schema = 1
	}
	return manifest, nil
}

func SaveManifest(dir string, manifest Manifest) error {
	dir = defaultDir(dir)
	manifest.App = "kerebrom"
	manifest.Schema = 1
	manifest.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create sync dir: %w", err)
	}
	content, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sync manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), append(content, '\n'), 0o644); err != nil {
		return fmt.Errorf("write sync manifest: %w", err)
	}
	return nil
}

func normalizeOptions(opts Options) Options {
	opts.Dir = defaultDir(opts.Dir)
	opts.Project = strings.TrimSpace(opts.Project)
	return opts
}

func defaultDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ".kerebrom"
	}
	return dir
}

func latestExportTime(manifest Manifest, project string) string {
	project = strings.TrimSpace(project)
	latest := ""
	for _, chunk := range manifest.Chunks {
		if chunk.All {
			continue
		}
		if strings.TrimSpace(chunk.Project) != project {
			continue
		}
		if chunk.CreatedAt > latest {
			latest = chunk.CreatedAt
		}
	}
	return latest
}

func upsertChunk(manifest Manifest, chunk Chunk) Manifest {
	for i, existing := range manifest.Chunks {
		if existing.ID == chunk.ID {
			manifest.Chunks[i] = chunk
			return manifest
		}
	}
	manifest.Chunks = append(manifest.Chunks, chunk)
	return manifest
}

func encodeJSONL(data sqlite.ExportData) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	for _, session := range data.Sessions {
		if err := encodeChunkRecord(encoder, "session", session); err != nil {
			return nil, err
		}
	}
	for _, alias := range data.ProjectAliases {
		if err := encodeChunkRecord(encoder, "project_alias", alias); err != nil {
			return nil, err
		}
	}
	for _, observation := range data.Observations {
		if err := encodeChunkRecord(encoder, "observation", observation); err != nil {
			return nil, err
		}
	}
	for _, prompt := range data.Prompts {
		if err := encodeChunkRecord(encoder, "prompt", prompt); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func encodeChunkRecord(encoder *json.Encoder, recordType string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode %s payload: %w", recordType, err)
	}
	if err := encoder.Encode(chunkRecord{Type: recordType, Data: raw}); err != nil {
		return fmt.Errorf("encode %s record: %w", recordType, err)
	}
	return nil
}

func writeGzip(path string, raw []byte) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create sync chunk: %w", err)
	}
	defer file.Close()

	writer := gzip.NewWriter(file)
	if _, err := writer.Write(raw); err != nil {
		_ = writer.Close()
		return fmt.Errorf("write sync chunk gzip: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close sync chunk gzip: %w", err)
	}
	return nil
}

func readChunk(dir string, chunk Chunk) (sqlite.ExportData, error) {
	path, err := safeChunkPath(dir, chunk.File)
	if err != nil {
		return sqlite.ExportData{}, err
	}

	file, err := os.Open(path)
	if err != nil {
		return sqlite.ExportData{}, fmt.Errorf("open sync chunk %s: %w", chunk.ID, err)
	}
	defer file.Close()

	reader, err := gzip.NewReader(file)
	if err != nil {
		return sqlite.ExportData{}, fmt.Errorf("open sync chunk gzip %s: %w", chunk.ID, err)
	}
	defer reader.Close()

	var data sqlite.ExportData
	data.App = "kerebrom"
	data.Schema = 1
	data.ExportedAt = chunk.CreatedAt
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var record chunkRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return sqlite.ExportData{}, fmt.Errorf("decode chunk record %s: %w", chunk.ID, err)
		}
		if err := appendRecord(&data, record); err != nil {
			return sqlite.ExportData{}, err
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return sqlite.ExportData{}, fmt.Errorf("read sync chunk %s: %w", chunk.ID, err)
	}
	return data, nil
}

func appendRecord(data *sqlite.ExportData, record chunkRecord) error {
	switch record.Type {
	case "session":
		var session sqlite.Session
		if err := json.Unmarshal(record.Data, &session); err != nil {
			return fmt.Errorf("decode session record: %w", err)
		}
		data.Sessions = append(data.Sessions, session)
	case "project_alias":
		var alias sqlite.ProjectAlias
		if err := json.Unmarshal(record.Data, &alias); err != nil {
			return fmt.Errorf("decode project alias record: %w", err)
		}
		data.ProjectAliases = append(data.ProjectAliases, alias)
	case "observation":
		var observation sqlite.Observation
		if err := json.Unmarshal(record.Data, &observation); err != nil {
			return fmt.Errorf("decode observation record: %w", err)
		}
		data.Observations = append(data.Observations, observation)
	case "prompt":
		var prompt sqlite.Prompt
		if err := json.Unmarshal(record.Data, &prompt); err != nil {
			return fmt.Errorf("decode prompt record: %w", err)
		}
		data.Prompts = append(data.Prompts, prompt)
	default:
		return fmt.Errorf("unknown sync chunk record type %q", record.Type)
	}
	return nil
}

func safeChunkPath(dir string, name string) (string, error) {
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("unsafe chunk path %q", name)
	}
	return filepath.Join(defaultDir(dir), clean), nil
}
