// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package registry

import (
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	gcrregistry "github.com/google/go-containerregistry/pkg/registry"
	gcr "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// localImages is a registry served in the test's process, holding two images
// that share their first layer, so the layer cache holds one copy of it.
type localImages struct {
	base, derived             string // references
	baseDigest, derivedDigest string
	shared                    gcr.Hash // the layer both images have
	added                     gcr.Hash // the layer only derived has
}

func newLocalImages(t *testing.T) localImages {
	t.Helper()

	server := httptest.NewServer(gcrregistry.New(gcrregistry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")

	base, err := random.Image(1024, 1)
	if err != nil {
		t.Fatal(err)
	}
	extra, err := random.Layer(2048, "application/vnd.docker.image.rootfs.diff.tar.gzip")
	if err != nil {
		t.Fatal(err)
	}
	derived, err := mutate.AppendLayers(base, extra)
	if err != nil {
		t.Fatal(err)
	}

	images := localImages{base: host + "/test/base:1", derived: host + "/test/derived:1"}
	images.baseDigest = push(t, images.base, base)
	images.derivedDigest = push(t, images.derived, derived)

	layers, err := base.Layers()
	if err != nil {
		t.Fatal(err)
	}
	if images.shared, err = layers[0].Digest(); err != nil {
		t.Fatal(err)
	}
	if images.added, err = extra.Digest(); err != nil {
		t.Fatal(err)
	}

	return images
}

// push writes image to ref, and returns its manifest digest.
func push(t *testing.T, ref string, image gcr.Image) string {
	t.Helper()

	parsed, err := name.ParseReference(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(parsed, image, remote.WithContext(t.Context())); err != nil {
		t.Fatalf("push %s: %v", ref, err)
	}
	digest, err := image.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return digest.String()
}

// pullToCache pulls an image into c's layer cache, without unpacking it, and
// returns the progress it reported.
func pullToCache(t *testing.T, c *Client, ref, digest string) []Progress {
	t.Helper()

	var progress []Progress
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.pullToCache(t.Context(), ref, digest, strings.TrimPrefix(digest, "sha256:"), func(p Progress) {
		progress = append(progress, p)
	}); err != nil {
		t.Fatalf("pull %s: %v", ref, err)
	}
	return progress
}

// TestPullCountsOnlyLayersNotCached checks that a pull's progress
// is measured against what it downloads: a layer the cache already holds
// from another image is not counted.
func TestPullCountsOnlyLayersNotCached(t *testing.T) {
	images := newLocalImages(t)
	c, err := NewClient(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	pullToCache(t, c, images.base, images.baseDigest)
	progress := pullToCache(t, c, images.derived, images.derivedDigest)

	total := progress[0].Total
	if progress[0].Phase != PhaseDownloading {
		t.Fatalf("first progress = %+v, want the download's total", progress[0])
	}
	if want := blobSize(t, c, images.added); total != want {
		t.Errorf("total = %d bytes, want %d, the size of the one layer not cached", total, want)
	}

	var downloaded int64
	for _, p := range progress {
		if p.Phase == PhaseDownloading {
			downloaded = p.Downloaded
		}
	}
	if downloaded != total {
		t.Errorf("downloaded %d bytes, want the total of %d", downloaded, total)
	}
}

// TestPruneCacheKeepsWhatAKeptImageNeeds checks that pruning drops the
// images not kept, and the blobs only they used, but not a layer a kept
// image shares with one that goes.
func TestPruneCacheKeepsWhatAKeptImageNeeds(t *testing.T) {
	images := newLocalImages(t)
	c, err := NewClient(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pullToCache(t, c, images.base, images.baseDigest)
	pullToCache(t, c, images.derived, images.derivedDigest)

	before, err := c.CacheSize()
	if err != nil {
		t.Fatal(err)
	}

	derivedHex := strings.TrimPrefix(images.derivedDigest, "sha256:")
	reclaimed, err := c.PruneCache([]string{derivedHex})
	if err != nil {
		t.Fatalf("PruneCache: %v", err)
	}

	after, err := c.CacheSize()
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed <= 0 || before-after < reclaimed {
		t.Errorf("reclaimed %d bytes, and the cache shrank from %d to %d", reclaimed, before, after)
	}

	if _, err := os.Stat(c.blobPath(images.shared)); err != nil {
		t.Errorf("the layer the kept image shares: %v", err)
	}
	baseHex := strings.TrimPrefix(images.baseDigest, "sha256:")
	if _, err := os.Stat(filepath.Join(c.cacheDir, "blobs", "sha256", baseHex)); err == nil {
		t.Error("the dropped image's manifest is still cached")
	}

	cached, err := c.isCached(t.Context(), derivedHex)
	if err != nil || !cached {
		t.Fatalf("the kept image is cached = %v, %v; want it", cached, err)
	}
	// Every blob it is made of is still there.
	image, err := c.cachedImage(derivedHex)
	if err != nil {
		t.Fatalf("open the kept image: %v", err)
	}
	manifest, err := image.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, blob := range append(manifest.Layers, manifest.Config) {
		if _, err := os.Stat(c.blobPath(blob.Digest)); err != nil {
			t.Errorf("a blob of the kept image: %v", err)
		}
	}
}

// TestPruneCacheKeepingEverythingReclaimsNothing checks that a prune with
// nothing to drop leaves the cache alone.
func TestPruneCacheKeepingEverythingReclaimsNothing(t *testing.T) {
	images := newLocalImages(t)
	c, err := NewClient(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pullToCache(t, c, images.base, images.baseDigest)

	before := cachedBlobs(t, c)
	reclaimed, err := c.PruneCache([]string{strings.TrimPrefix(images.baseDigest, "sha256:")})
	if err != nil {
		t.Fatalf("PruneCache: %v", err)
	}
	if reclaimed != 0 {
		t.Errorf("reclaimed %d bytes, want none", reclaimed)
	}
	if after := cachedBlobs(t, c); len(after) != len(before) {
		t.Errorf("the cache went from %d blobs to %d", len(before), len(after))
	}
}

// TestPruneCacheOfAnEmptyCache checks that a cache nothing was ever pulled
// into prunes to nothing, rather than failing.
func TestPruneCacheOfAnEmptyCache(t *testing.T) {
	c, err := NewClient(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	reclaimed, err := c.PruneCache(nil)
	if err != nil || reclaimed != 0 {
		t.Errorf("PruneCache = %d, %v; want 0, nil", reclaimed, err)
	}
	if size, err := c.CacheSize(); err != nil || size != 0 {
		t.Errorf("CacheSize = %d, %v; want 0, nil", size, err)
	}
}

func cachedBlobs(t *testing.T, c *Client) []os.DirEntry {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(c.cacheDir, "blobs", "sha256"))
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func blobSize(t *testing.T, c *Client, digest gcr.Hash) int64 {
	t.Helper()

	info, err := os.Stat(c.blobPath(digest))
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
