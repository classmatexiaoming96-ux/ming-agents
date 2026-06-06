package codegraph

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// ProcessPool manages pre-warmed CLI processes for low-latency queries.
type ProcessPool struct {
	mu sync.RWMutex
	pools map[string]*repoProcessPool // keyed by absolute repo path

	maxProcsPerRepo int
	idleTimeout    time.Duration

	metricsLock sync.RWMutex
	metrics     PoolMetrics
}

type repoProcessPool struct {
	repoPath    string
	procs       []*Process
	waitQueue   chan struct{}
	mu          sync.Mutex
	idleTimeout time.Duration
	maxProcs    int
}

type Process struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   *jsonScanner
	stderr   io.Reader
	drainDone chan struct{}
	inUse    atomic.Bool
	usedAt   atomic.Int64
	repoPath string
}

type jsonScanner struct {
	sc *bufio.Scanner
}

func (j *jsonScanner) Read() (json.RawMessage, error) {
	if j.sc.Scan() {
		return j.sc.Bytes(), nil
	}
	if err := j.sc.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

// PoolMetrics tracks pool performance.
type PoolMetrics struct {
	TotalRequests  int64
	CacheHits     int64
	CacheMisses   int64
	AvgLatencyMs float64
}

// NewProcessPool creates a new process pool.
func NewProcessPool(maxProcsPerRepo int, idleTimeout time.Duration) *ProcessPool {
	return &ProcessPool{
		pools:          make(map[string]*repoProcessPool),
		maxProcsPerRepo: maxProcsPerRepo,
		idleTimeout:    idleTimeout,
	}
}

// GetProcess acquires or spawns a process for the given repo.
func (p *ProcessPool) GetProcess(ctx context.Context, repoPath string) (*Process, error) {
	p.mu.RLock()
	pool, exists := p.pools[repoPath]
	p.mu.RUnlock()

	if !exists {
		p.mu.Lock()
		if pool, exists = p.pools[repoPath]; !exists {
			pool = &repoProcessPool{
				repoPath:    repoPath,
				procs:       make([]*Process, 0),
				waitQueue:   make(chan struct{}, p.maxProcsPerRepo),
				idleTimeout: p.idleTimeout,
				maxProcs:    p.maxProcsPerRepo,
			}
			p.pools[repoPath] = pool
		}
		p.mu.Unlock()
	}

	return pool.acquire(ctx)
}

func (p *repoProcessPool) acquire(ctx context.Context) (*Process, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, proc := range p.procs {
		if !proc.inUse.Load() && time.Since(time.Unix(proc.usedAt.Load(), 0)) < p.idleTimeout {
			proc.inUse.Store(true)
			return proc, nil
		}
	}

	if len(p.procs) < p.maxProcs {
		proc, err := p.spawnProcess()
		if err != nil {
			return nil, err
		}
		proc.inUse.Store(true)
		p.procs = append(p.procs, proc)
		return proc, nil
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case p.waitQueue <- struct{}{}:
		for _, proc := range p.procs {
			if !proc.inUse.Load() {
				proc.inUse.Store(true)
				return proc, nil
			}
		}
	}

	return nil, fmt.Errorf("timeout waiting for process")
}

func (p *repoProcessPool) spawnProcess() (*Process, error) {
	cmd := exec.Command("codegraph", "serve", "--mcp", "--path", p.repoPath)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	proc := &Process{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   &jsonScanner{sc: bufio.NewScanner(stdout)},
		stderr:   stderr,
		drainDone: make(chan struct{}),
		repoPath: p.repoPath,
	}
	go func() {
		select {
		case <-proc.drainDone:
			return
		default:
			io.Copy(io.Discard, stderr)
		}
	}()
	proc.usedAt.Store(time.Now().Unix())

	return proc, nil
}

// ReturnProcess returns a process to the pool.
func (p *ProcessPool) ReturnProcess(repoPath string, proc *Process) {
	proc.inUse.Store(false)
	proc.usedAt.Store(time.Now().Unix())

	p.mu.RLock()
	pool, exists := p.pools[repoPath]
	p.mu.RUnlock()

	if exists {
		select {
		case <-pool.waitQueue:
		default:
		}
	}
}

// Exec sends a JSON-RPC request to the process and returns the response.
func (proc *Process) Exec(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	id := time.Now().UnixNano()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	_, err = proc.stdin.Write(append(reqBytes, '\n'))
	if err != nil {
		return nil, fmt.Errorf("write stdin: %w", err)
	}

	resp, err := proc.stdout.Read()
	if err != nil {
		return nil, fmt.Errorf("read stdout: %w", err)
	}

	var rpcResp struct {
		ID interface{} `json:"id"`
		Result json.RawMessage `json:"result,omitempty"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	if err := json.Unmarshal(resp, &rpcResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}

// Close terminates the process.
func (proc *Process) Close() error {
	if proc.drainDone != nil {
		close(proc.drainDone)
	}
	if proc.cmd.Process != nil {
		proc.cmd.Process.Kill()
	}
	proc.cmd.Wait()
	return nil
}