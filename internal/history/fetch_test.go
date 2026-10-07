package history

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// gzipped is s as the release asset serves it.
func gzipped(s string) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return b.Bytes()
}

// releaseServer is a data repo with a latest release holding two assets
// (ids 201 and 202, the full archive) and the pinned asset by id.
// latestStatus is what the release lookup answers and assetStatus what
// every download does; lookups and downloads count the calls.
type releaseServer struct {
	*httptest.Server
	latestStatus int
	latestBody   string
	assetStatus  int
	lookups      atomic.Int32
	downloads    atomic.Int32
}

func newReleaseServer(t *testing.T) *releaseServer {
	rs := &releaseServer{latestStatus: http.StatusOK, assetStatus: http.StatusOK}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			rs.lookups.Add(1)
			if r.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(rs.latestStatus)
			body := rs.latestBody
			if body == "" {
				body = fmt.Sprintf(`{"tag_name":"v2","assets":[`+
					`{"id":201,"name":"history.db.gz","url":"%s/assets/201"},`+
					`{"id":202,"name":"history-full.db.gz","url":"%s/assets/202"}]}`, rs.URL, rs.URL)
			}
			_, _ = w.Write([]byte(body))
		case "/assets/202", "/assets/617060111", "/assets/override":
			rs.downloads.Add(1)
			if r.Header.Get("Accept") != "application/octet-stream" {
				w.WriteHeader(http.StatusUnsupportedMediaType)
				return
			}
			if rs.assetStatus != http.StatusOK {
				w.WriteHeader(rs.assetStatus)
				return
			}
			_, _ = w.Write(gzipped(r.URL.Path))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(rs.Close)
	t.Setenv("HISTORY_DB_TOKEN", "tok")
	t.Setenv("HISTORY_DB_URL", "")
	savedLatest, savedDefault := latestReleaseURL, defaultAssetURL
	latestReleaseURL, defaultAssetURL = rs.URL+"/releases/latest", rs.URL+"/assets/617060111"
	t.Cleanup(func() { latestReleaseURL, defaultAssetURL = savedLatest, savedDefault })
	return rs
}

// contents is what the archive at path holds, and its sidecar.
func contents(t *testing.T, path string) (string, string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	side, _ := os.ReadFile(sidecar(path))
	return string(raw), string(side)
}

// fetched resolves the archive, downloads it into a fresh path and
// returns what landed there and in the sidecar.
func fetched(t *testing.T) (string, string) {
	dst := filepath.Join(t.TempDir(), "history.db")
	if !fetchDB(dst, resolveArchive()) {
		t.Fatal("fetchDB failed")
	}
	return contents(t, dst)
}

// The rebuilt archive is a new release with a new asset id. With no
// HISTORY_DB_URL the download is the history-full.db.gz on the latest
// release, found by name and fetched by its own url, and the sidecar
// says which asset it is.
func TestFetchDBTakesTheLatestReleasesFullArchive(t *testing.T) {
	rs := newReleaseServer(t)
	got, side := fetched(t)
	if got != "/assets/202" {
		t.Errorf("downloaded %q, want the latest release's history-full.db.gz", got)
	}
	if side != "202\n" {
		t.Errorf("sidecar %q, want 202", side)
	}
	if n := rs.lookups.Load(); n != 1 {
		t.Errorf("release looked up %d times, want 1", n)
	}
}

// When the release lookup fails in any way, the pinned asset is
// downloaded as before: the lookup answering an error, and the lookup
// answering a release with no full archive on it.
func TestFetchDBFallsBackToThePinnedAsset(t *testing.T) {
	rs := newReleaseServer(t)
	rs.latestStatus = http.StatusInternalServerError
	if got, side := fetched(t); got != "/assets/617060111" || side != "617060111\n" {
		t.Errorf("after a failed lookup downloaded %q (sidecar %q), want the pinned asset", got, side)
	}
	rs.latestStatus = http.StatusOK
	rs.latestBody = `{"tag_name":"v3","assets":[{"id":301,"name":"history.db.gz","url":"` + rs.URL + `/assets/301"}]}`
	if got, _ := fetched(t); got != "/assets/617060111" {
		t.Errorf("with no full archive on the release downloaded %q, want the pinned asset", got)
	}
}

// HISTORY_DB_URL still overrides everything: the release is not looked
// up, and the file is no release asset, so it gets no sidecar.
func TestFetchDBHonoursTheOverride(t *testing.T) {
	rs := newReleaseServer(t)
	t.Setenv("HISTORY_DB_URL", rs.URL+"/assets/override")
	got, side := fetched(t)
	if got != "/assets/override" {
		t.Errorf("downloaded %q, want HISTORY_DB_URL", got)
	}
	if side != "" {
		t.Errorf("sidecar %q for a HISTORY_DB_URL download", side)
	}
	if n := rs.lookups.Load(); n != 0 {
		t.Errorf("release looked up %d times with HISTORY_DB_URL set", n)
	}
}

// onDisk is an archive already on the persistent disk, from the asset
// the sidecar names ("" for no sidecar).
func onDisk(t *testing.T, side string) string {
	path := filepath.Join(t.TempDir(), "history.db")
	if err := os.WriteFile(path, []byte("on disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if side != "" {
		if err := os.WriteFile(sidecar(path), []byte(side), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// A downloaded archive is replaced when its sidecar names another asset
// than the latest release's, and left alone when it names the same one. A
// file with no sidecar is the user's own (HISTORY_DB at a histgen.py
// build): it is kept, and the release is not even looked up.
func TestAnArchiveOnDiskIsReplacedWhenTheLatestAssetDiffers(t *testing.T) {
	for _, c := range []struct {
		side      string
		replaced  bool
		lookups   int32
		downloads int32
	}{
		{"201\n", true, 1, 1},
		{"", false, 0, 0},
		{"202\n", false, 1, 0},
	} {
		rs := newReleaseServer(t)
		path := onDisk(t, c.side)
		got, ok := ensureArchive(path)
		if !ok || got != path {
			t.Fatalf("sidecar %q: ensureArchive = %q, %v", c.side, got, ok)
		}
		raw, side := contents(t, path)
		want, wantSide := "on disk", c.side
		if c.replaced {
			want, wantSide = "/assets/202", "202\n"
		}
		if raw != want || side != wantSide {
			t.Errorf("sidecar %q: archive %q, sidecar %q; want %q, %q", c.side, raw, side, want, wantSide)
		}
		if n := rs.lookups.Load(); n != c.lookups {
			t.Errorf("sidecar %q: %d release lookups, want %d", c.side, n, c.lookups)
		}
		if n := rs.downloads.Load(); n != c.downloads {
			t.Errorf("sidecar %q: %d downloads, want %d", c.side, n, c.downloads)
		}
	}
}

// When the replacement download fails the file on disk keeps serving.
func TestAnArchiveOnDiskIsKeptWhenTheDownloadFails(t *testing.T) {
	rs := newReleaseServer(t)
	rs.assetStatus = http.StatusInternalServerError
	path := onDisk(t, "201\n")
	got, ok := ensureArchive(path)
	if !ok || got != path {
		t.Fatalf("ensureArchive = %q, %v", got, ok)
	}
	if raw, side := contents(t, path); raw != "on disk" || side != "201\n" {
		t.Errorf("archive %q, sidecar %q after a failed download; want the file as it was", raw, side)
	}
	if _, err := os.Stat(path + ".part"); err == nil {
		t.Error("a .part file was left beside the archive")
	}
}

// HISTORY_DB_URL names no asset, so a file on disk is never replaced
// by it.
func TestAnArchiveOnDiskIsKeptUnderTheOverride(t *testing.T) {
	rs := newReleaseServer(t)
	t.Setenv("HISTORY_DB_URL", rs.URL+"/assets/override")
	path := onDisk(t, "201\n")
	if got, ok := ensureArchive(path); !ok || got != path {
		t.Fatalf("ensureArchive = %q, %v", got, ok)
	}
	if raw, _ := contents(t, path); raw != "on disk" {
		t.Errorf("archive %q under HISTORY_DB_URL; want the file as it was", raw)
	}
	if n := rs.downloads.Load(); n != 0 {
		t.Errorf("%d downloads under HISTORY_DB_URL with a file on disk", n)
	}
}
