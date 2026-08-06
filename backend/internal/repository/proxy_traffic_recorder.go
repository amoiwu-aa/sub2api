package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	proxyTrafficCacheTTL       = time.Minute
	proxyTrafficFlushInterval  = 2 * time.Second
	proxyTrafficFlushBatchSize = 1000
)

type proxyTrafficCacheEntry struct {
	proxyID   int64
	expiresAt time.Time
}

type proxyTrafficBucketKey struct {
	proxyID int64
	day     string
}

type proxyTrafficBucket struct {
	uploadBytes   atomic.Int64
	downloadBytes atomic.Int64
}

type proxyTrafficDelta struct {
	proxyID       int64
	day           string
	uploadBytes   int64
	downloadBytes int64
}

type proxyTrafficRecorder struct {
	db *sql.DB

	resolveCache sync.Map
	buckets      sync.Map
	stopCh       chan struct{}
	stopOnce     sync.Once
	stopped      atomic.Bool
	wg           sync.WaitGroup
	flushMu      sync.Mutex
}

func NewProxyTrafficRecorder(db *sql.DB) service.ProxyTrafficRecorder {
	recorder := &proxyTrafficRecorder{
		db:     db,
		stopCh: make(chan struct{}),
	}
	if db != nil {
		recorder.wg.Add(1)
		go recorder.run()
	}
	return recorder
}

func (r *proxyTrafficRecorder) ResolveProxyID(ctx context.Context, accountID int64, proxyURL string) int64 {
	if r == nil || r.db == nil || accountID <= 0 || strings.TrimSpace(proxyURL) == "" || r.stopped.Load() {
		return 0
	}

	cacheKey := strconv.FormatInt(accountID, 10) + "\x1f" + strings.TrimSpace(proxyURL)
	now := time.Now()
	if value, ok := r.resolveCache.Load(cacheKey); ok {
		if cached, ok := value.(proxyTrafficCacheEntry); ok && cached.expiresAt.After(now) {
			return cached.proxyID
		}
		r.resolveCache.Delete(cacheKey)
	}

	queryCtx := ctx
	if queryCtx == nil {
		queryCtx = context.Background()
	}
	queryCtx, cancel := context.WithTimeout(queryCtx, 2*time.Second)
	defer cancel()

	var proxyID int64
	err := r.db.QueryRowContext(queryCtx, `
		SELECT proxy_id
		FROM accounts
		WHERE id = $1
		  AND proxy_id IS NOT NULL
		  AND deleted_at IS NULL
	`, accountID).Scan(&proxyID)
	if err != nil {
		if err != sql.ErrNoRows && queryCtx.Err() == nil {
			logger.LegacyPrintf("repository.proxy_traffic", "resolve proxy for account %d failed: %v", accountID, err)
		}
		return 0
	}

	r.resolveCache.Store(cacheKey, proxyTrafficCacheEntry{
		proxyID:   proxyID,
		expiresAt: now.Add(proxyTrafficCacheTTL),
	})
	return proxyID
}

func (r *proxyTrafficRecorder) Record(proxyID, uploadBytes, downloadBytes int64) {
	if r == nil || r.db == nil || r.stopped.Load() || proxyID <= 0 || (uploadBytes <= 0 && downloadBytes <= 0) {
		return
	}

	key := proxyTrafficBucketKey{
		proxyID: proxyID,
		day:     time.Now().UTC().Format(time.DateOnly),
	}
	value, _ := r.buckets.LoadOrStore(key, &proxyTrafficBucket{})
	bucket := value.(*proxyTrafficBucket)
	if uploadBytes > 0 {
		bucket.uploadBytes.Add(uploadBytes)
	}
	if downloadBytes > 0 {
		bucket.downloadBytes.Add(downloadBytes)
	}
}

func (r *proxyTrafficRecorder) Stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		r.stopped.Store(true)
		close(r.stopCh)
		r.wg.Wait()
		r.flush(context.Background())
	})
}

func (r *proxyTrafficRecorder) run() {
	defer r.wg.Done()

	ticker := time.NewTicker(proxyTrafficFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			r.flush(ctx)
			cancel()
		case <-r.stopCh:
			return
		}
	}
}

func (r *proxyTrafficRecorder) flush(ctx context.Context) {
	if r == nil || r.db == nil {
		return
	}
	r.flushMu.Lock()
	defer r.flushMu.Unlock()

	deltas := r.drain()
	for start := 0; start < len(deltas); start += proxyTrafficFlushBatchSize {
		end := start + proxyTrafficFlushBatchSize
		if end > len(deltas) {
			end = len(deltas)
		}
		batch := deltas[start:end]
		if err := r.persist(ctx, batch); err != nil {
			logger.LegacyPrintf("repository.proxy_traffic", "persist proxy traffic failed: %v", err)
			r.restore(batch)
		}
	}
}

func (r *proxyTrafficRecorder) drain() []proxyTrafficDelta {
	deltas := make([]proxyTrafficDelta, 0)
	r.buckets.Range(func(key, value any) bool {
		bucketKey, keyOK := key.(proxyTrafficBucketKey)
		bucket, bucketOK := value.(*proxyTrafficBucket)
		if !keyOK || !bucketOK {
			return true
		}
		uploadBytes := bucket.uploadBytes.Swap(0)
		downloadBytes := bucket.downloadBytes.Swap(0)
		if uploadBytes <= 0 && downloadBytes <= 0 {
			return true
		}
		deltas = append(deltas, proxyTrafficDelta{
			proxyID:       bucketKey.proxyID,
			day:           bucketKey.day,
			uploadBytes:   uploadBytes,
			downloadBytes: downloadBytes,
		})
		return true
	})
	return deltas
}

func (r *proxyTrafficRecorder) restore(deltas []proxyTrafficDelta) {
	for _, delta := range deltas {
		if delta.proxyID <= 0 || (delta.uploadBytes <= 0 && delta.downloadBytes <= 0) {
			continue
		}
		key := proxyTrafficBucketKey{proxyID: delta.proxyID, day: delta.day}
		value, _ := r.buckets.LoadOrStore(key, &proxyTrafficBucket{})
		bucket := value.(*proxyTrafficBucket)
		if delta.uploadBytes > 0 {
			bucket.uploadBytes.Add(delta.uploadBytes)
		}
		if delta.downloadBytes > 0 {
			bucket.downloadBytes.Add(delta.downloadBytes)
		}
	}
}

func (r *proxyTrafficRecorder) persist(ctx context.Context, deltas []proxyTrafficDelta) error {
	if len(deltas) == 0 {
		return nil
	}

	var query strings.Builder
	query.WriteString(`
		UPDATE proxies AS p
		SET
			traffic_upload_bytes = p.traffic_upload_bytes + v.upload_bytes,
			traffic_download_bytes = p.traffic_download_bytes + v.download_bytes,
			traffic_today_upload_bytes = CASE
				WHEN p.traffic_today_date = v.usage_date
				THEN p.traffic_today_upload_bytes + v.upload_bytes
				ELSE v.upload_bytes
			END,
			traffic_today_download_bytes = CASE
				WHEN p.traffic_today_date = v.usage_date
				THEN p.traffic_today_download_bytes + v.download_bytes
				ELSE v.download_bytes
			END,
			traffic_today_date = v.usage_date
		FROM (VALUES `)

	args := make([]any, 0, len(deltas)*4)
	for i, delta := range deltas {
		if i > 0 {
			query.WriteString(",")
		}
		base := i * 4
		fmt.Fprintf(&query, "($%d::bigint, $%d::date, $%d::bigint, $%d::bigint)", base+1, base+2, base+3, base+4)
		args = append(args, delta.proxyID, delta.day, delta.uploadBytes, delta.downloadBytes)
	}
	query.WriteString(`
		) AS v(proxy_id, usage_date, upload_bytes, download_bytes)
		WHERE p.id = v.proxy_id
		  AND p.deleted_at IS NULL`)

	_, err := r.db.ExecContext(ctx, query.String(), args...)
	return err
}
