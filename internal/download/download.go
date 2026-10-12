// Package download acquires immutable archives, with digest locks and resumable
// checkpoints. Package semantics are verified by the preparation layer.
package download

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/internal/diskspace"
	"github.com/diffscope/diffscope-package-manager/internal/downloadcache"
	"github.com/diffscope/diffscope-package-manager/internal/netpolicy"
	"github.com/diffscope/diffscope-package-manager/internal/registry"
	"github.com/diffscope/diffscope-package-manager/packagearchive"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	repo "github.com/diffscope/diffscope-package-manager/packagerepository"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Progress struct {
	Phase             string               `json:"phase"`
	Target            string               `json:"target"`
	State             string               `json:"state"`
	ProgressVersion   int                  `json:"progressVersion"`
	OperationID       string               `json:"operationId"`
	TaskID            string               `json:"taskId"`
	TransportID       string               `json:"transportId"`
	Package           packageinfo.Identity `json:"package"`
	Sequence          int64                `json:"sequence"`
	Attempt           int                  `json:"attempt"`
	SourceAttempt     int                  `json:"sourceAttempt"`
	Generation        int                  `json:"generation"`
	Status            string               `json:"status"`
	Current           int64                `json:"current"`
	Total             int64                `json:"total"`
	TotalKnown        bool                 `json:"totalKnown"`
	ReceivedBytes     int64                `json:"receivedBytes"`
	ResumedBytes      int64                `json:"resumedBytes"`
	BytesPerSecond    float64              `json:"bytesPerSecond"`
	ETASeconds        *float64             `json:"etaSeconds"`
	CacheStatus       string               `json:"cacheStatus"`
	RetryInMs         int64                `json:"retryInMs,omitempty"`
	AuthoritySourceID string               `json:"authoritySourceId"`
	SourceID          string               `json:"sourceId,omitempty"`
	Host              string               `json:"host,omitempty"`
	PreviousSourceID  string               `json:"previousSourceId,omitempty"`
	Reason            string               `json:"reason,omitempty"`
	NetworkPolicy     string               `json:"networkPolicy"`
	AddressValidation string               `json:"addressValidation,omitempty"`
	FilePath          string               `json:"filePath"`
	FilesDone         int                  `json:"filesDone"`
	FilesTotal        int                  `json:"filesTotal"`
	Error             any                  `json:"error,omitempty"`
}
type Attempt struct {
	SourceID string `json:"sourceId"`
	Attempt  int    `json:"attempt"`
	Error    string `json:"error"`
	Code     string `json:"code,omitempty"`
}
type RejectedCandidate struct {
	Code     string `json:"code"`
	SourceID string `json:"sourceId,omitempty"`
	Reason   string `json:"reason"`
}
type Outcome struct {
	Identity               packageinfo.Identity `json:"identity"`
	AuthoritySourceID      string               `json:"authoritySourceId"`
	ActualSourceID         string               `json:"actualSourceId,omitempty"`
	TransportID            string               `json:"transportId"`
	ReceivedBytes          int64                `json:"receivedBytes"`
	SourceSwitches         int                  `json:"sourceSwitches"`
	SourceHistory          []string             `json:"sourceHistory"`
	PerSourceBytes         map[string]int64     `json:"perSourceReceivedBytes"`
	Attempts               []Attempt            `json:"attempts"`
	RejectedCandidates     []RejectedCandidate  `json:"rejectedCandidates"`
	CacheStatus            string               `json:"cacheStatus"`
	AddressValidation      string               `json:"addressValidation,omitempty"`
	VerifiedDownloadSha512 string               `json:"verifiedDownloadSha512,omitempty"`
	SourceProofs           []SourceProof        `json:"sourceProofs"`
}
type SourceProof struct {
	Repository     registry.Source `json:"repository"`
	ManifestSha512 string          `json:"manifestSha512"`
	DownloadSha512 string          `json:"downloadSha512"`
}
type Error struct {
	Code    string
	Message string
	Details any
}

func (e *Error) Error() string { return e.Message }

type checkpoint struct {
	Expected    string `json:"expectedSha512"`
	Bytes       int64  `json:"checkpoint"`
	Total       int64  `json:"total"`
	ResourceKey string `json:"resourceKey"`
	ETag        string `json:"etag,omitempty"`
}
type Engine struct {
	Cache       *downloadcache.Cache
	Policy      netpolicy.Policy
	Client      *http.Client
	MaxBytes    int64
	IdleTimeout time.Duration
	OperationID string
	mu          sync.Mutex
	sequence    int64
	lastUpdate  map[string]time.Time
	lastStatus  map[string]string
	lastCurrent map[string]int64
	activeTasks map[string]struct{}
	cooldown    map[string]time.Time
	Observe     func(Progress)
}

func New(c *downloadcache.Cache, p netpolicy.Policy, observe func(Progress)) *Engine {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return &Engine{Cache: c, Policy: p, Client: p.Client(false), MaxBytes: 64 << 30, IdleTimeout: 60 * time.Second, OperationID: hex.EncodeToString(b), cooldown: map[string]time.Time{}, Observe: observe}
}
func (e *Engine) Close() {
	e.Client.CloseIdleConnections()
	_ = e.Cache.Root.RemoveAll("tmp/" + e.OperationID)
}
func (e *Engine) emit(p *Progress, state, status string) {
	p.State = state
	p.Status = status
	if state == "done" {
		zero := float64(0)
		p.ETASeconds = &zero
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lastStatus == nil {
		e.lastStatus = map[string]string{}
		e.lastCurrent = map[string]int64{}
		e.lastUpdate = map[string]time.Time{}
		e.activeTasks = map[string]struct{}{}
	}
	switch state {
	case "start":
		e.activeTasks[p.TaskID] = struct{}{}
	case "done", "failed", "cancelled":
		delete(e.activeTasks, p.TaskID)
	}
	// Per-task intervals prevent one transfer from suppressing another.
	// Active task counts scale the interval to the soft global event budget.
	interval := max(100*time.Millisecond, time.Duration(len(e.activeTasks))*25*time.Millisecond)
	firstBytes := e.lastCurrent[p.TaskID] == 0 && p.Current > 0
	if state == "update" && !firstBytes && e.lastStatus[p.TaskID] == status && time.Since(e.lastUpdate[p.TaskID]) < interval {
		return
	}
	if state == "update" {
		e.lastUpdate[p.TaskID] = time.Now()
	}
	e.lastStatus[p.TaskID] = status
	e.lastCurrent[p.TaskID] = p.Current
	e.sequence++
	p.Sequence = e.sequence
	if e.Observe != nil {
		e.Observe(*p)
	}
}
func (e *Engine) wait(ctx context.Context, host string) error {
	e.mu.Lock()
	until := e.cooldown[host]
	e.mu.Unlock()
	duration := time.Until(until)
	if duration <= 0 {
		return nil
	}
	if duration > 120*time.Second {
		return &Error{"RATE_LIMITED", "The server cooldown exceeds the automatic wait budget.", nil}
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func retryAfter(raw string) time.Duration {
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n >= 0 && n <= 86400 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(raw); err == nil {
		return max(0, time.Until(t))
	}
	return 0
}
func safeError(err error) string {
	var u *url.Error
	if errors.As(err, &u) {
		return u.Err.Error()
	}
	return err.Error()
}
func hash(ctx context.Context, r *os.Root, p string) (string, error) {
	f, err := r.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	return packagearchive.HashFileBytes(ctx, f, st.Size(), nil)
}

var rangePattern = regexp.MustCompile(`^bytes ([0-9]+)-([0-9]+)/([0-9]+|\*)$`)
var unsatisfiedPattern = regexp.MustCompile(`^bytes \*/([0-9]+)$`)

func protocol(reason string) error { return &Error{"DOWNLOAD_PROTOCOL_ERROR", reason, nil} }
func (e *Engine) Acquire(ctx context.Context, authority registry.Resolved, discover func(context.Context) (*registry.Resolved, error)) (filename string, out Outcome, finalErr error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	m := authority.Manifest
	p := Progress{Phase: "download", ProgressVersion: 1, OperationID: e.OperationID, TaskID: netpolicy.Key(m.String()), TransportID: netpolicy.Key(e.OperationID + m.String()), Package: m.Identity, Target: m.String(), AuthoritySourceID: authority.Source.ID, NetworkPolicy: e.Policy.Name(), CacheStatus: "miss"}
	out = Outcome{Identity: m.Identity, AuthoritySourceID: authority.Source.ID, TransportID: p.TransportID, SourceHistory: []string{}, PerSourceBytes: map[string]int64{}, Attempts: []Attempt{}, RejectedCandidates: []RejectedCandidate{}}
	out.SourceProofs = []SourceProof{{authority.Source, authority.ManifestSha512, m.DownloadSha512}}
	e.emit(&p, "start", "queued")
	defer func() {
		out.ReceivedBytes = p.ReceivedBytes
		out.CacheStatus = p.CacheStatus
		out.AddressValidation = p.AddressValidation
		if finalErr == nil {
			out.VerifiedDownloadSha512 = m.DownloadSha512
		}
		var detailed *Error
		if errors.As(finalErr, &detailed) {
			if _, ok := detailed.Details.(Outcome); ok {
				detailed.Details = out
			}
		}
		if finalErr != nil {
			state, status := "failed", "failed"
			if errors.Is(ctx.Err(), context.Canceled) {
				state, status = "cancelled", "cancelled"
			}
			code := "DOWNLOAD_FAILED"
			var d *Error
			if errors.As(finalErr, &d) {
				code = d.Code
			}
			if errors.Is(finalErr, context.DeadlineExceeded) {
				code = "DOWNLOAD_TIMEOUT"
			}
			if diskspace.IsFull(finalErr) {
				code = "DISK_FULL"
			}
			if state == "cancelled" {
				code = "CANCELLED"
			}
			p.Error = map[string]any{"code": code, "message": safeError(finalErr), "retryable": false}
			e.emit(&p, state, status)
		}
	}()
	blob, partial, sidecar, err := e.Cache.Paths(m.DownloadSha512)
	if err != nil {
		return "", out, err
	}
	e.emit(&p, "update", "waiting-lock")
	lock, err := e.Cache.ObjectLock(ctx, m.DownloadSha512)
	if err != nil {
		return "", out, err
	}
	defer lock.Unlock()
	e.emit(&p, "update", "checking-cache")
	if digest, err := hash(ctx, e.Cache.Root, blob); err == nil && digest == m.DownloadSha512 {
		st, _ := e.Cache.Root.Stat(blob)
		p.Current = st.Size()
		p.Total = p.Current
		p.TotalKnown = true
		p.CacheStatus = "hit"
		e.emit(&p, "cache-hit", "verifying")
		e.emit(&p, "update", "verifying")
		e.emit(&p, "done", "completed")
		return filepath.Join(e.Cache.Root.Name(), filepath.FromSlash(blob)), out, nil
	} else if err == nil {
		_ = e.Cache.Root.Remove(blob)
	}
	if err = e.Cache.Root.MkdirAll(path.Dir(partial), 0700); err != nil {
		return "", out, err
	}
	var cp checkpoint
	if e.Cache.ReadJSON(sidecar, &cp) == nil && cp.Expected == m.DownloadSha512 && cp.Bytes >= 0 && cp.Bytes <= e.MaxBytes {
		if st, err := e.Cache.Root.Stat(partial); err == nil && st.Size() >= cp.Bytes {
			f, err := e.Cache.Root.OpenFile(partial, os.O_WRONLY, 0600)
			if err != nil {
				return "", out, err
			}
			err = f.Truncate(cp.Bytes)
			f.Close()
			if err != nil {
				return "", out, err
			}
			p.Current = cp.Bytes
			p.Total = cp.Total
			p.TotalKnown = cp.Total > 0
			if p.Current > 0 {
				p.CacheStatus = "resume"
				p.ResumedBytes = p.Current
			}
		}
	} else {
		_ = e.Cache.Root.Remove(partial)
	}
	current := authority
	attemptsBySource := map[string]int{}
	requests, retries, cleanRecovery := 0, 0, 0
	ephemeral := false
	visitedURLs := map[string]bool{}
	restart := func(reason string) error {
		f, err := e.Cache.Root.OpenFile(partial, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		f.Close()
		p.Current = 0
		p.Total = 0
		p.TotalKnown = false
		p.ETASeconds = nil
		p.ResumedBytes = 0
		p.Generation++
		p.Reason = reason
		cp = checkpoint{Expected: m.DownloadSha512}
		e.emit(&p, "restart", "connecting")
		return nil
	}
	save := func(f *os.File) error {
		if err := f.Sync(); err != nil {
			return err
		}
		if ephemeral {
			return nil
		}
		cp.Expected = m.DownloadSha512
		cp.Bytes = p.Current
		cp.Total = p.Total
		b, err := json.Marshal(cp)
		if err != nil {
			return err
		}
		return downloadcache.AtomicWrite(e.Cache.Root, sidecar, b)
	}
	for {
		if err = ctx.Err(); err != nil {
			return "", out, err
		}
		p.SourceID = current.Source.ID
		p.SourceAttempt = attemptsBySource[p.SourceID]
		p.Host = ""
		p.Reason = ""
		visitedURLs[current.Manifest.DownloadURL] = true
		transfer := func() (bool, error) {
			raw := current.Manifest.DownloadURL
			u, _ := url.Parse(raw)
			if err := e.Policy.Validate(raw); err != nil {
				return false, err
			}
			if err := e.wait(ctx, u.Host); err != nil {
				return false, err
			}
			if requests >= 64 {
				return false, &Error{"DOWNLOAD_FAILED", "The HTTP request budget is exhausted.", nil}
			}
			requests++
			p.Attempt++
			attemptsBySource[p.SourceID]++
			p.SourceAttempt = attemptsBySource[p.SourceID]
			p.Host = u.Host
			p.AddressValidation = e.Policy.AddressValidation(raw)
			p.ResumedBytes = p.Current
			e.emit(&p, "update", "connecting")
			requestCtx, stop := context.WithCancel(ctx)
			defer stop()
			req, err := http.NewRequestWithContext(requestCtx, "GET", raw, nil)
			if err != nil {
				return false, err
			}
			req.Header.Set("Accept-Encoding", "identity")
			if p.Current > 0 {
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-", p.Current))
			}
			client := *e.Client
			originalRedirect := client.CheckRedirect
			client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
				requests++
				if requests > 64 {
					return fmt.Errorf("The HTTP request count exceeds its budget.")
				}
				if err := originalRedirect(req, via); err != nil {
					return err
				}
				if err := e.wait(ctx, req.URL.Host); err != nil {
					return err
				}
				return nil
			}
			resp, err := client.Do(req)
			if err != nil {
				var urlErr *url.Error
				if errors.As(err, &urlErr) {
					err = urlErr.Err
				}
				return false, fmt.Errorf("The archive download from %s failed: %w", netpolicy.Redact(raw), err)
			}
			defer resp.Body.Close()
			p.Host = resp.Request.URL.Host
			p.AddressValidation = e.Policy.AddressValidation(resp.Request.URL.String())
			if resp.StatusCode == 429 || resp.StatusCode == 503 {
				duration := retryAfter(resp.Header.Get("Retry-After"))
				if duration > 0 {
					e.mu.Lock()
					e.cooldown[p.Host] = time.Now().Add(duration)
					e.mu.Unlock()
				}
			}
			if resp.StatusCode == 416 {
				parts := unsatisfiedPattern.FindStringSubmatch(resp.Header.Get("Content-Range"))
				if len(parts) == 2 {
					total, _ := strconv.ParseInt(parts[1], 10, 64)
					if total == p.Current {
						digest, err := hash(ctx, e.Cache.Root, partial)
						if err == nil && digest == m.DownloadSha512 {
							p.Total = total
							p.TotalKnown = true
							return true, nil
						}
					}
				}
				return false, protocol("The HTTP 416 response does not confirm a complete verified prefix.")
			}
			if resp.StatusCode != 200 && resp.StatusCode != 206 {
				return false, &Error{"DOWNLOAD_FAILED", fmt.Sprintf("The server returned HTTP status %d at %s.", resp.StatusCode, netpolicy.Redact(raw)), map[string]any{"status": resp.StatusCode, "retryable": resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode == 500 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504}}
			}
			if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
				return false, protocol("The archive response uses a non-identity content encoding.")
			}
			if strings.HasPrefix(resp.Header.Get("Content-Type"), "multipart/byteranges") {
				return false, protocol("Multipart range responses are unsupported.")
			}
			segmentSize := resp.ContentLength
			var rangeTotal int64
			if resp.StatusCode == 206 {
				parts := rangePattern.FindStringSubmatch(resp.Header.Get("Content-Range"))
				if len(parts) != 4 {
					return false, protocol("The Content-Range header is malformed.")
				}
				start, e1 := strconv.ParseInt(parts[1], 10, 64)
				end, e2 := strconv.ParseInt(parts[2], 10, 64)
				if e1 != nil || e2 != nil || start != p.Current || end < start || end >= e.MaxBytes {
					return false, protocol("The response range bounds are invalid.")
				}
				segmentSize = end - start + 1
				if resp.ContentLength >= 0 && resp.ContentLength != segmentSize {
					return false, protocol("The Content-Length header differs from the response range length.")
				}
				if parts[3] != "*" {
					var e3 error
					rangeTotal, e3 = strconv.ParseInt(parts[3], 10, 64)
					if e3 != nil || rangeTotal <= end || rangeTotal > e.MaxBytes || p.TotalKnown && p.Total != rangeTotal {
						return false, protocol("The response range total is invalid.")
					}
				}
			} else {
				if p.Current > 0 {
					if err = restart("The server returned a full response to the Range request."); err != nil {
						return false, err
					}
				}
				rangeTotal = resp.ContentLength
			}
			if rangeTotal > e.MaxBytes {
				return false, protocol("The archive exceeds its download byte budget.")
			}
			if err := diskspace.Check(e.Cache.Root.Name(), max(0, rangeTotal-p.Current)); err != nil {
				return false, &Error{"DISK_FULL", err.Error(), nil}
			}
			if rangeTotal > 0 {
				p.Total = rangeTotal
				p.TotalKnown = true
			}
			if strings.Contains(strings.ToLower(resp.Header.Get("Cache-Control")), "no-store") && !ephemeral {
				tmp := "tmp/" + e.OperationID + "/" + m.DownloadSha512 + ".part"
				if err = e.Cache.Root.MkdirAll(path.Dir(tmp), 0700); err != nil {
					return false, err
				}
				if p.Current > 0 {
					if err = e.Cache.Root.Rename(partial, tmp); err != nil {
						return false, err
					}
				} else {
					_ = e.Cache.Root.Remove(partial)
				}
				_ = e.Cache.Root.Remove(sidecar)
				partial = tmp
				ephemeral = true
				p.CacheStatus = "bypass"
				p.Reason = "The server forbids persistent caching."
			}
			f, err := e.Cache.Root.OpenFile(partial, os.O_CREATE|os.O_WRONLY, 0600)
			if err != nil {
				return false, err
			}
			defer f.Close()
			if err = f.Truncate(p.Current); err != nil {
				return false, err
			}
			if _, err = f.Seek(p.Current, io.SeekStart); err != nil {
				return false, err
			}
			cp.ResourceKey = netpolicy.Key(resp.Request.URL.String())
			cp.ETag = resp.Header.Get("ETag")
			var idleExpired atomic.Bool
			idleTimeout := e.IdleTimeout
			if idleTimeout <= 0 {
				idleTimeout = 60 * time.Second
			}
			timer := time.AfterFunc(idleTimeout, func() { idleExpired.Store(true); stop() })
			defer timer.Stop()
			buf := make([]byte, 128<<10)
			var read int64
			lastSave := time.Now()
			lastProgress := time.Time{}
			startTime := time.Now()
			startBytes := p.ReceivedBytes
			type sample struct {
				time  time.Time
				bytes int64
			}
			samples := []sample{{startTime, startBytes}}
			e.emit(&p, "update", "downloading")
			for {
				n, readErr := resp.Body.Read(buf)
				if n > 0 {
					timer.Reset(idleTimeout)
					p.ReceivedBytes += int64(n)
					out.PerSourceBytes[p.SourceID] += int64(n)
					if p.Current+int64(n) > e.MaxBytes {
						return false, protocol("The received archive bytes exceed their budget.")
					}
					written, writeErr := f.Write(buf[:n])
					p.Current += int64(written)
					read += int64(n)
					if writeErr != nil {
						_ = save(f)
						return false, writeErr
					}
					if written != n {
						return false, io.ErrShortWrite
					}
					now := time.Now()
					if now.Sub(samples[len(samples)-1].time) >= 100*time.Millisecond {
						samples = append(samples, sample{now, p.ReceivedBytes})
					}
					for len(samples) > 2 && now.Sub(samples[1].time) > 5*time.Second {
						samples = samples[1:]
					}
					p.BytesPerSecond = float64(p.ReceivedBytes-samples[0].bytes) / max(.001, now.Sub(samples[0].time).Seconds())
					if p.TotalKnown && p.BytesPerSecond > 0 {
						eta := float64(max(0, p.Total-p.Current)) / p.BytesPerSecond
						p.ETASeconds = &eta
					}
					if time.Since(lastProgress) >= 100*time.Millisecond {
						e.emit(&p, "update", "downloading")
						lastProgress = time.Now()
					}
					if p.Current-cp.Bytes >= 32<<20 || time.Since(lastSave) >= 5*time.Second {
						if err = save(f); err != nil {
							return false, err
						}
						lastSave = time.Now()
					}
				}
				if readErr != nil {
					if err = save(f); err != nil {
						return false, err
					}
					if readErr != io.EOF {
						if idleExpired.Load() {
							return false, &Error{"DOWNLOAD_TIMEOUT", "The archive response made no byte progress before the idle deadline.", map[string]any{"retryable": true}}
						}
						return false, readErr
					}
					break
				}
			}
			if segmentSize >= 0 && read != segmentSize {
				return false, io.ErrUnexpectedEOF
			}
			if p.TotalKnown && p.Current < p.Total {
				return false, nil
			}
			if p.TotalKnown && p.Current != p.Total {
				return false, protocol("The downloaded byte count differs from the expected total.")
			}
			e.emit(&p, "update", "verifying")
			digest, err := hash(ctx, e.Cache.Root, partial)
			if err != nil {
				return false, err
			}
			if digest != m.DownloadSha512 {
				return false, &Error{"DOWNLOAD_HASH_MISMATCH", fmt.Sprintf("The expected digest is %s. The actual digest is %s.", m.DownloadSha512, digest), nil}
			}
			return true, nil
		}
		complete, transferErr := transfer()
		if complete && transferErr == nil {
			if !ephemeral {
				if err = e.Cache.Root.MkdirAll(path.Dir(blob), 0700); err != nil {
					return "", out, err
				}
				if err = e.Cache.Root.Rename(partial, blob); err != nil {
					return "", out, err
				}
				_ = e.Cache.Root.Remove(sidecar)
				partial = blob
			}
			p.Total = p.Current
			p.TotalKnown = true
			out.ActualSourceID = p.SourceID
			out.SourceHistory = append(out.SourceHistory, p.SourceID)
			e.emit(&p, "done", "completed")
			return filepath.Join(e.Cache.Root.Name(), filepath.FromSlash(partial)), out, nil
		}
		if transferErr == nil {
			continue
		}
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return "", out, &Error{"DOWNLOAD_TIMEOUT", "Archive acquisition exceeded its total deadline.", nil}
			}
			return "", out, ctx.Err()
		}
		var pathErr *os.PathError
		if errors.As(transferErr, &pathErr) {
			return "", out, transferErr
		}
		var timeout net.Error
		if errors.As(transferErr, &timeout) && timeout.Timeout() {
			transferErr = &Error{"DOWNLOAD_TIMEOUT", safeError(transferErr), map[string]any{"retryable": true}}
		}
		attempt := Attempt{SourceID: p.SourceID, Attempt: p.Attempt, Error: safeError(transferErr)}
		var typed *Error
		isTyped := errors.As(transferErr, &typed)
		if isTyped {
			attempt.Code = typed.Code
		}
		out.Attempts = append(out.Attempts, attempt)
		if isTyped && typed.Code == "DISK_FULL" {
			return "", out, transferErr
		}
		protocolError := isTyped && (typed.Code == "DOWNLOAD_PROTOCOL_ERROR" || typed.Code == "DOWNLOAD_HASH_MISMATCH")
		if protocolError {
			resumed := p.ResumedBytes > 0
			if err = restart(typed.Code); err != nil {
				return "", out, err
			}
			if cleanRecovery == 0 && (typed.Code == "DOWNLOAD_PROTOCOL_ERROR" || resumed) {
				cleanRecovery++
				continue
			}
		}
		retryable := !isTyped
		var unknownCA x509.UnknownAuthorityError
		var badName x509.HostnameError
		var badCertificate x509.CertificateInvalidError
		var dnsErr *net.DNSError
		if errors.As(transferErr, &unknownCA) || errors.As(transferErr, &badName) || errors.As(transferErr, &badCertificate) || errors.As(transferErr, &dnsErr) && dnsErr.IsNotFound || strings.Contains(transferErr.Error(), "DOWNLOAD_POLICY_VIOLATION") {
			retryable = false
		}
		if isTyped {
			if d, ok := typed.Details.(map[string]any); ok {
				retryable, _ = d["retryable"].(bool)
			}
		}
		if retryable && retries < 3 && attemptsBySource[p.SourceID] == 1 {
			retries++
			p.RetryInMs = int64(200 * retries)
			p.Reason = safeError(transferErr)
			e.emit(&p, "retry", "retrying")
			timer := time.NewTimer(time.Duration(p.RetryInMs) * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", out, ctx.Err()
			case <-timer.C:
			}
			p.RetryInMs = 0
			continue
		}
		e.emit(&p, "update", "discovering-sources")
		var next *registry.Resolved
		for discover != nil {
			candidate, discoveryErr := discover(ctx)
			if discoveryErr != nil {
				rejection := RejectedCandidate{Code: "DOWNLOAD_SOURCE_REJECTED", Reason: safeError(discoveryErr)}
				var detail *Error
				if errors.As(discoveryErr, &detail) {
					if fields, ok := detail.Details.(map[string]any); ok {
						rejection.SourceID, _ = fields["sourceId"].(string)
					}
				}
				out.RejectedCandidates = append(out.RejectedCandidates, rejection)
				p.Reason = safeError(discoveryErr)
				e.emit(&p, "update", "discovering-sources")
				continue
			}
			if candidate == nil {
				break
			}
			if !repo.SameContent(m, candidate.Manifest) {
				out.RejectedCandidates = append(out.RejectedCandidates, RejectedCandidate{Code: "DOWNLOAD_SOURCE_REJECTED", SourceID: candidate.Source.ID, Reason: "The content identity or metadata differs."})
				continue
			}
			if visitedURLs[candidate.Manifest.DownloadURL] {
				continue
			}
			out.SourceProofs = append(out.SourceProofs, SourceProof{candidate.Source, candidate.ManifestSha512, candidate.Manifest.DownloadSha512})
			next = candidate
			break
		}
		if next == nil {
			if retryable && retries < 3 {
				retries++
				p.RetryInMs = int64(200 * retries)
				p.Reason = safeError(transferErr)
				e.emit(&p, "retry", "retrying")
				timer := time.NewTimer(time.Duration(p.RetryInMs) * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return "", out, ctx.Err()
				case <-timer.C:
				}
				p.RetryInMs = 0
				continue
			}
			return "", out, &Error{"DOWNLOAD_SOURCES_EXHAUSTED", "All permitted download sources failed.", out}
		}
		previous := p.SourceID
		out.SourceHistory = append(out.SourceHistory, previous)
		out.SourceSwitches++
		current = *next
		p.PreviousSourceID = previous
		p.SourceID = next.Source.ID
		p.SourceAttempt = attemptsBySource[p.SourceID]
		p.Host = ""
		p.BytesPerSecond = 0
		p.ETASeconds = nil
		p.Reason = safeError(transferErr)
		e.emit(&p, "source-switch", "switching-source")
		p.PreviousSourceID = ""
		p.Reason = ""
	}
}
