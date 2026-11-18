/* Copyright 2026 AtomAI
*
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You may obtain a copy of the License at
*
*     http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
*
Part of this work fall under the AtomAI Enterprise Edition (EE)
and are licensed under the Business Source License 1.1,
if you are the Cloud companies or your companies with revenue greater than $1M,
please refer to [AGPL-3.0](./LICENSE-EE)) and LICENSE-EE for details,
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.
*/

package directory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
	segment "github.com/vcaesar/bluge_segment_api"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/index"
)

type objectInfo struct {
	Key  string
	Size int64
}

// objectStore is the subset of object storage operations the directory needs.
type objectStore interface {
	List(ctx context.Context, prefix string) ([]objectInfo, error)
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Put stores size bytes read from r under key.
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	Delete(ctx context.Context, key string) error
}

// MemoryObjectStore is an in-memory objectStore used by tests, in this package
// and, through SetTestObjectStore, by packages that only see the exported API.
type MemoryObjectStore struct {
	mu   sync.Mutex
	objs map[string][]byte
	err  error
	gets int
}

func NewMemoryObjectStore() *MemoryObjectStore {
	return &MemoryObjectStore{objs: map[string][]byte{}}
}

// SetErr makes List and Get fail with err; nil clears the failure.
func (m *MemoryObjectStore) SetErr(err error) {
	m.mu.Lock()
	m.err = err
	m.mu.Unlock()
}

// Gets returns how many objects were fetched from the store.
func (m *MemoryObjectStore) Gets() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gets
}

// Len returns how many objects are stored under prefix.
func (m *MemoryObjectStore) Len(prefix string) int {
	objs, err := m.List(context.Background(), prefix)
	if err != nil {
		return 0
	}
	return len(objs)
}

func (m *MemoryObjectStore) List(_ context.Context, prefix string) ([]objectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	var rv []objectInfo
	for k, v := range m.objs {
		if strings.HasPrefix(k, prefix) {
			rv = append(rv, objectInfo{Key: k, Size: int64(len(v))})
		}
	}
	sort.Slice(rv, func(i, j int) bool { return rv[i].Key < rv[j].Key })
	return rv, nil
}

func (m *MemoryObjectStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	m.gets++
	v, ok := m.objs[key]
	if !ok {
		return nil, fmt.Errorf("object %s: %w", key, fs.ErrNotExist)
	}
	return io.NopCloser(bytes.NewReader(v)), nil
}

func (m *MemoryObjectStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objs[key] = data
	return nil
}

func (m *MemoryObjectStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objs, key)
	return nil
}

// backend lazily builds the process-wide objectStore of one storage type.
type backend struct {
	newStore func() (objectStore, error)
	mu       sync.Mutex
	inited   bool
	store    objectStore
	err      error
}

func (b *backend) get() (objectStore, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.inited {
		b.store, b.err = b.newStore()
		b.inited = true
	}
	return b.store, b.err
}

// setStore replaces the store of an already or not yet initialized backend and
// returns a function restoring the previous state.
func (b *backend) setStore(store objectStore) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	oldInited, oldStore, oldErr := b.inited, b.store, b.err
	b.inited, b.store, b.err = true, store, nil
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.inited, b.store, b.err = oldInited, oldStore, oldErr
	}
}

// backendFor returns the backend of an object storage type.
func backendFor(storageType string) (*backend, error) {
	switch storageType {
	case "s3", "minio":
		return s3Backend, nil
	case "gcs":
		return gcsBackend, nil
	case "oss":
		return ossBackend, nil
	default:
		return nil, fmt.Errorf("storage type [%s] is not an object storage", storageType)
	}
}

// SetTestObjectStore installs store as the backend of storageType and returns a
// function restoring the previous one. It exists so packages outside directory
// can exercise the object storage paths without a real bucket.
func SetTestObjectStore(storageType string, store *MemoryObjectStore) (func(), error) {
	b, err := backendFor(storageType)
	if err != nil {
		return nil, err
	}
	return b.setStore(store), nil
}

// isIndexItem reports whether name is a riot index item (segment, snapshot or
// persisted HNSW graph). Everything else found next to them (pid files, WAL)
// must not be copied.
func isIndexItem(name string) bool {
	ext := path.Ext(name)
	return ext == index.ItemKindSegment || ext == index.ItemKindSnapshot || ext == index.ItemKindGraph
}

// directoryMarker marks an object prefix as an existing directory, so a shard
// that holds no segments yet can still be opened read-only. Its name matches no
// item kind, so List and Load never see it.
const directoryMarker = ".zed_directory"

// CopyDiskToObject uploads every index item of indexName found under rootPath to
// the object storage backend of storageType. Items are immutable and keyed by
// name, so re-running after a partial copy simply overwrites what it re-uploads.
func CopyDiskToObject(storageType, rootPath, indexName string) error {
	store, err := storeFor(storageType)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if err := store.Put(ctx, indexName+"/"+directoryMarker, bytes.NewReader(nil), 0); err != nil {
		return err
	}
	dir := filepath.Join(rootPath, filepath.FromSlash(indexName))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing was ever written for this shard
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !isIndexItem(entry.Name()) {
			continue
		}
		if err := copyFileToObject(ctx, store, filepath.Join(dir, entry.Name()), indexName+"/"+entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

// copyFileToObject streams src to key without loading it into memory.
func copyFileToObject(ctx context.Context, store objectStore, src, key string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err == nil {
		err = store.Put(ctx, key, f, info.Size())
	}
	return errors.Join(err, f.Close())
}

// CopyObjectToDisk downloads every index item of indexName from the object
// storage backend of storageType into rootPath.
func CopyObjectToDisk(storageType, rootPath, indexName string) error {
	store, err := storeFor(storageType)
	if err != nil {
		return err
	}
	ctx := context.Background()
	objs, err := store.List(ctx, indexName+"/")
	if err != nil {
		return err
	}
	dir := filepath.Join(rootPath, filepath.FromSlash(indexName))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, obj := range objs {
		name := path.Base(obj.Key)
		if !isIndexItem(name) {
			continue
		}
		if err := copyObjectToFile(ctx, store, obj.Key, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	// the bucket copy may be dropped right after, so the renames must be durable
	return syncDir(dir)
}

// syncDir commits the directory entries of dir. Windows cannot fsync a directory.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func copyObjectToFile(ctx context.Context, store objectStore, key, dst string) error {
	rc, err := store.Get(ctx, key)
	if err != nil {
		return err
	}
	defer func() {
		if err := rc.Close(); err != nil {
			log.Warn().Err(err).Str("key", key).Msg("object store: close body")
		}
	}()
	f, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".*"+cacheTempExt)
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer removeTemp(tmp)
	_, copyErr := io.Copy(f, rc)
	if err := errors.Join(copyErr, f.Sync(), f.Close()); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// ObjectIndexHasData reports whether indexName has any index item stored, as
// opposed to only the directory marker of a shard that was never written to.
func ObjectIndexHasData(storageType, indexName string) (bool, error) {
	store, err := storeFor(storageType)
	if err != nil {
		return false, err
	}
	objs, err := store.List(context.Background(), indexName+"/")
	if err != nil {
		return false, err
	}
	for _, obj := range objs {
		if isIndexItem(path.Base(obj.Key)) {
			return true, nil
		}
	}
	return false, nil
}

// RemoveObjectIndexByType deletes every object and cached file of indexName in
// the backend of storageType.
func RemoveObjectIndexByType(storageType, indexName string) error {
	b, err := backendFor(storageType)
	if err != nil {
		return err
	}
	return removeObjectIndex(b, indexName)
}

func storeFor(storageType string) (objectStore, error) {
	b, err := backendFor(storageType)
	if err != nil {
		return nil, err
	}
	return b.get()
}

// getObjectConfig returns a riot config that stores index items as objects
// under "<indexName>/" in the backend, cached locally by defaultFileCache.
func getObjectConfig(b *backend, indexName string, timeRange []int64) (riot.Config, error) {
	store, err := b.get()
	if err != nil {
		return riot.Config{}, err
	}
	cache := defaultFileCache()
	cfg := index.DefaultConfigWithDirectory(func() index.Directory {
		return NewObjectDirectory(store, cache, indexName)
	})
	cfg = cfg.WithPersisterNapTimeMSec(50)
	if len(timeRange) == 2 {
		if timeRange[0] <= timeRange[1] {
			cfg = cfg.WithTimeRange(timeRange[0], timeRange[1])
		}
	}
	return riot.DefaultConfigWithIndexConfig(cfg), nil
}

// removeObjectIndex deletes every object and cached file stored for the index.
func removeObjectIndex(b *backend, indexName string) error {
	store, err := b.get()
	if err != nil {
		return err
	}
	return removeObjectPrefix(store, defaultFileCache(), indexName)
}

// removeObjectPrefix deletes the objects first: a local cache problem must not
// leave the bucket data orphaned.
func removeObjectPrefix(store objectStore, cache *fileCache, prefix string) error {
	ctx := context.Background()
	objs, err := store.List(ctx, prefix+"/")
	if err != nil {
		return err
	}
	for _, obj := range objs {
		if err := store.Delete(ctx, obj.Key); err != nil {
			return err
		}
	}
	if cache == nil {
		return nil
	}
	return cache.RemovePrefix(prefix + "/")
}

// ObjectDirectory implements index.Directory on top of an objectStore.
// With a cache, items are kept as local files and memory-mapped; without one
// they are loaded fully into memory on every open.
//
// The prefix is listed once and then tracked through Persist and Remove: riot
// opens one directory per writer or reader, and a writer owns its prefix, so
// this saves the LIST requests riot would otherwise send on every persist.
type ObjectDirectory struct {
	store  objectStore
	cache  *fileCache // nil disables local caching
	prefix string

	mu    sync.Mutex
	items map[string]int64 // item name -> size; nil until listed
}

func NewObjectDirectory(store objectStore, cache *fileCache, indexName string) *ObjectDirectory {
	return &ObjectDirectory{store: store, cache: cache, prefix: indexName + "/"}
}

func (d *ObjectDirectory) key(kind string, id uint64) string {
	return d.prefix + fmt.Sprintf("%012x", id) + kind
}

// listing returns the objects directly under the prefix, listing it on first
// use. d.mu must be held.
func (d *ObjectDirectory) listing() (map[string]int64, error) {
	if d.items != nil {
		return d.items, nil
	}
	objs, err := d.store.List(context.Background(), d.prefix)
	if err != nil {
		return nil, err
	}
	items := make(map[string]int64, len(objs))
	for _, obj := range objs {
		name := strings.TrimPrefix(obj.Key, d.prefix)
		if strings.Contains(name, "/") {
			continue // nested keys belong to a deeper prefix
		}
		items[name] = obj.Size
	}
	d.items = items
	return items, nil
}

// track records name as stored with size, or as removed when size < 0.
func (d *ObjectDirectory) track(name string, size int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.items == nil {
		return // listed on next use
	}
	if size < 0 {
		delete(d.items, name)
	} else {
		d.items[name] = size
	}
}

func (d *ObjectDirectory) Setup(readOnly bool) error {
	if !readOnly {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	items, err := d.listing()
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("readOnly, directory does not exist")
	}
	return nil
}

func (d *ObjectDirectory) List(kind string) ([]uint64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	items, err := d.listing()
	if err != nil {
		return nil, err
	}
	var rv uint64Slice
	for name := range items {
		if !strings.HasSuffix(name, kind) {
			continue
		}
		base := name[:len(name)-len(kind)]
		id, err := strconv.ParseUint(base, 16, 64)
		if err != nil {
			return nil, fmt.Errorf("error parsing identifier '%s': %w", base, err)
		}
		rv = append(rv, id)
	}
	sort.Sort(sort.Reverse(rv))
	return rv, nil
}

func (d *ObjectDirectory) Load(kind string, id uint64) (*segment.Data, io.Closer, error) {
	key := d.key(kind, id)
	if d.cache != nil {
		if data, closer, ok := d.cache.Get(key); ok {
			return data, closer, nil
		}
		// cache errors are kept apart from store errors: on Windows a broken cache
		// path reports ERROR_PATH_NOT_FOUND, which also matches fs.ErrNotExist
		if getErr, cacheErr := d.fetchToCache(key); errors.Is(getErr, fs.ErrNotExist) {
			// riot builds and persists missing optional items such as HNSW graphs
			return nil, nil, getErr
		} else if err := errors.Join(getErr, cacheErr); err != nil {
			log.Warn().Err(err).Str("key", key).Msg("object cache: falling back to in-memory load")
		} else if data, closer, ok := d.cache.Get(key); ok {
			return data, closer, nil
		}
	}
	rc, err := d.store.Get(context.Background(), key)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err := rc.Close(); err != nil {
			log.Warn().Err(err).Str("key", key).Msg("object store: close body")
		}
	}()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, nil, err
	}
	return segment.NewDataBytes(data), io.NopCloser(nil), nil
}

// fetchToCache copies key from the store into the cache, returning the store
// and cache errors separately.
func (d *ObjectDirectory) fetchToCache(key string) (getErr, cacheErr error) {
	rc, err := d.store.Get(context.Background(), key)
	if err != nil {
		return err, nil
	}
	defer func() {
		if err := rc.Close(); err != nil {
			log.Warn().Err(err).Str("key", key).Msg("object store: close body")
		}
	}()
	return nil, d.cache.Put(key, rc)
}

// Persist spools the item to a temporary file and streams it to the store, so
// large merged segments are never held in memory. With a cache the spooled file
// becomes the cached copy. Closing closeCh aborts the upload.
func (d *ObjectDirectory) Persist(kind string, id uint64, w index.WriterTo, closeCh chan struct{}) error {
	ctx, cancel := closeContext(closeCh)
	defer cancel()
	tempDir := "" // os.TempDir
	if d.cache != nil {
		tempDir = d.cache.TempDir()
	}
	f, err := os.CreateTemp(tempDir, "persist-*"+cacheTempExt)
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer removeTemp(tmp)
	key := d.key(kind, id)
	size, err := w.WriteTo(f, closeCh)
	if err == nil && d.cache != nil {
		err = f.Sync() // the file is kept as the cached copy
	}
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err == nil {
		err = d.store.Put(ctx, key, f, size)
	}
	if err := errors.Join(err, f.Close()); err != nil {
		return err
	}
	d.track(path.Base(key), size)
	if d.cache != nil {
		if err := d.cache.PutFile(key, tmp, size); err != nil {
			log.Warn().Err(err).Str("key", key).Msg("object cache: persist not cached")
		}
	}
	return nil
}

// closeContext returns a context cancelled once closeCh is closed.
func closeContext(closeCh chan struct{}) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	if closeCh != nil {
		go func() {
			select {
			case <-closeCh:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	return ctx, cancel
}

func (d *ObjectDirectory) Remove(kind string, id uint64) error {
	key := d.key(kind, id)
	if d.cache != nil {
		if err := d.cache.Remove(key); err != nil {
			return err
		}
	}
	if err := d.store.Delete(context.Background(), key); err != nil {
		return err
	}
	d.track(path.Base(key), -1)
	return nil
}

func (d *ObjectDirectory) Stats() (numItems uint64, numBytes uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	items, err := d.listing()
	if err != nil {
		log.Error().Err(err).Str("prefix", d.prefix).Msg("object directory stats")
		return 0, 0
	}
	for _, size := range items {
		numItems++
		numBytes += uint64(size)
	}
	return numItems, numBytes
}

func (d *ObjectDirectory) Sync() error   { return nil }
func (d *ObjectDirectory) Lock() error   { return nil }
func (d *ObjectDirectory) Unlock() error { return nil }

type uint64Slice []uint64

func (e uint64Slice) Len() int           { return len(e) }
func (e uint64Slice) Swap(i, j int)      { e[i], e[j] = e[j], e[i] }
func (e uint64Slice) Less(i, j int) bool { return e[i] < e[j] }

// PutObject stores data under key in the backend of storageType.
func PutObject(storageType, key string, data []byte) error {
	store, err := storeFor(storageType)
	if err != nil {
		return err
	}
	return store.Put(context.Background(), key, bytes.NewReader(data), int64(len(data)))
}

// GetObject reads the object stored under key.
func GetObject(storageType, key string) ([]byte, error) {
	store, err := storeFor(storageType)
	if err != nil {
		return nil, err
	}
	rc, err := store.Get(context.Background(), key)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(rc)
	return data, errors.Join(readErr, rc.Close())
}

// ListObjects returns the keys stored under prefix, sorted.
func ListObjects(storageType, prefix string) ([]string, error) {
	store, err := storeFor(storageType)
	if err != nil {
		return nil, err
	}
	objs, err := store.List(context.Background(), prefix)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(objs))
	for _, obj := range objs {
		keys = append(keys, obj.Key)
	}
	sort.Strings(keys)
	return keys, nil
}

// DeleteObject removes the object stored under key.
func DeleteObject(storageType, key string) error {
	store, err := storeFor(storageType)
	if err != nil {
		return err
	}
	return store.Delete(context.Background(), key)
}
