package service

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

func aiccUploadTestPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 400, 400))))
	return b.Bytes()
}
func aiccUploadTestStore(t *testing.T) *AICCUploadStore {
	t.Helper()
	s, e := NewAICCUploadStore(t.TempDir(), "test-upload-secret", "https://configured.example/prefix")
	require.NoError(t, e)
	return s
}
func aiccUploadTestSave(t *testing.T, s *AICCUploadStore) AICCUploadResult {
	t.Helper()
	r, e := s.Save(context.Background(), 101, "group", "Image", "image/png", bytes.NewReader(aiccUploadTestPNG(t)))
	require.NoError(t, e)
	return r
}
func aiccUploadTestOpen(s *AICCUploadStore, r AICCUploadResult) (*os.File, string, error) {
	u, _ := url.Parse(r.URL)
	return s.Open(r.ID, u.Query().Get("expires"), u.Query().Get("access"))
}
func TestAICCUploadPNGCapabilityOwnership(t *testing.T) {
	s := aiccUploadTestStore(t)
	r := aiccUploadTestSave(t, s)
	require.Len(t, r.ID, 64)
	require.Equal(t, "image/png", r.MIMEType)
	require.InDelta(t, 24*3600, r.ExpiresAt-time.Now().Unix(), 2)
	f, mt, err := aiccUploadTestOpen(s, r)
	require.NoError(t, err)
	data, err := io.ReadAll(f)
	f.Close()
	require.NoError(t, err)
	require.Equal(t, aiccUploadTestPNG(t), data)
	require.Equal(t, r.MIMEType, mt)
	for _, p := range []string{s.dir, filepath.Join(s.dir, r.ID+".blob"), filepath.Join(s.dir, r.ID+".json")} {
		st, e := os.Stat(p)
		require.NoError(t, e)
		want := os.FileMode(0600)
		if p == s.dir {
			want = 0700
		}
		require.Equal(t, want, st.Mode().Perm())
	}
	for _, tc := range []struct {
		owner       int
		group, kind string
		ok          bool
	}{{101, "group", "Image", true}, {202, "group", "Image", false}, {101, "other", "Image", false}, {101, "group", "Video", false}} {
		internal, e := s.ValidateURL(r.URL, tc.owner, tc.group, tc.kind)
		require.True(t, internal)
		if tc.ok {
			require.NoError(t, e)
		} else {
			require.ErrorIs(t, e, ErrAICCUploadNotFound)
		}
	}
	internal, e := s.ValidateURL("https://external.example/file.png", 101, "group", "Image")
	require.False(t, internal)
	require.NoError(t, e)
	internal, e = s.ValidateURL(strings.Replace(r.URL, "configured.example", "evil.example", 1), 101, "group", "Image")
	require.True(t, internal)
	require.Error(t, e)
	u, _ := url.Parse(r.URL)
	q := u.Query()
	expires, access := q.Get("expires"), q.Get("access")
	for _, tc := range []struct{ id, exp, sig string }{{r.ID, expires, strings.Repeat("0", 64)}, {r.ID, strconv.FormatInt(r.ExpiresAt+1, 10), access}, {"../" + r.ID, expires, access}, {strings.Repeat("a", 64), expires, access}} {
		f, _, e := s.Open(tc.id, tc.exp, tc.sig)
		require.Nil(t, f)
		require.ErrorIs(t, e, ErrAICCUploadNotFound)
	}
	s.now = func() time.Time { return time.Unix(r.ExpiresAt, 0) }
	f, _, e = aiccUploadTestOpen(s, r)
	require.Nil(t, f)
	require.ErrorIs(t, e, ErrAICCUploadNotFound)
}
func TestAICCUploadBoundCapability(t *testing.T) {
	s := aiccUploadTestStore(t)
	binding := model.AICCBinding{ChannelID: 7, AICCAccountID: strings.Repeat("a", 64)}
	r, err := s.Save(context.Background(), 101, "group", "Image", "image/png", bytes.NewReader(aiccUploadTestPNG(t)), binding)
	require.NoError(t, err)
	_, err = s.ValidateURL(r.URL, 101, "group", "Image", binding)
	require.NoError(t, err)
	for _, tc := range []struct {
		owner       int
		group, kind string
		binding     model.AICCBinding
	}{
		{202, "group", "Image", binding},
		{101, "other", "Image", binding},
		{101, "group", "Video", binding},
		{101, "group", "Image", model.AICCBinding{ChannelID: 8, AICCAccountID: binding.AICCAccountID}},
		{101, "group", "Image", model.AICCBinding{ChannelID: 7, AICCAccountID: strings.Repeat("b", 64)}},
		{101, "group", "Image", model.AICCBinding{}},
	} {
		_, err := s.ValidateURL(r.URL, tc.owner, tc.group, tc.kind, tc.binding)
		require.ErrorIs(t, err, ErrAICCUploadNotFound)
	}
	legacy := aiccUploadTestSave(t, s)
	_, err = s.ValidateURL(legacy.URL, 101, "group", "Image", binding)
	require.ErrorIs(t, err, ErrAICCUploadNotFound)
	f, _, err := aiccUploadTestOpen(s, legacy)
	require.NoError(t, err)
	f.Close()
	// Binding fields and version are authenticated, not merely trusted metadata.
	metaPath := filepath.Join(s.dir, r.ID+".json")
	original, err := os.ReadFile(metaPath)
	require.NoError(t, err)
	for _, field := range []string{"account", "channel", "version"} {
		var meta map[string]any
		require.NoError(t, json.Unmarshal(original, &meta))
		switch field {
		case "account":
			meta[field] = strings.Repeat("b", 64)
		case "channel":
			meta[field] = 8
		case "version":
			meta[field] = 1
		}
		data, err := json.Marshal(meta)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(metaPath, data, 0600))
		f, _, err := aiccUploadTestOpen(s, r)
		require.Nil(t, f)
		require.ErrorIs(t, err, ErrAICCUploadNotFound)
	}
	_, err = s.Save(context.Background(), 101, "group", "Image", "image/png", bytes.NewReader(aiccUploadTestPNG(t)), model.AICCBinding{})
	require.ErrorIs(t, err, ErrAICCUploadInvalid)
}

func TestAICCUploadURLHelperEncodings(t *testing.T) {
	s := aiccUploadTestStore(t)
	r := aiccUploadTestSave(t, s)
	secret, base := common.CryptoSecret, system_setting.TaskPublicAddress
	common.CryptoSecret = string(s.secret)
	system_setting.TaskPublicAddress = s.base
	t.Setenv("AICC_UPLOAD_DIR", s.dir)
	t.Cleanup(func() { common.CryptoSecret = secret; system_setting.TaskPublicAddress = base })
	require.NoError(t, ValidateAICCUploadURL(r.URL, 101, "group", "Image"))
	require.Error(t, ValidateAICCUploadURL(r.URL, 202, "group", "Image"))
	for _, raw := range []string{
		strings.Replace(r.URL, "configured.example", "other.example", 1),
		strings.Replace(r.URL, "/api/aicc/", "//api/aicc/", 1),
		strings.Replace(r.URL, "/api/aicc/", "/%2561pi/aicc/", 1),
		strings.Replace(r.URL, "/api/aicc/", "/api/temp/../aicc/", 1),
		r.URL + "&expires=1", r.URL + "&access=bad", r.URL + ";ignored=x", r.URL + "#fragment",
	} {
		require.Error(t, ValidateAICCUploadURL(raw, 101, "group", "Image"))
	}
	escaped := strings.Replace(r.URL, "/api/", "/%61pi/", 1)
	require.NoError(t, ValidateAICCUploadURL(escaped, 101, "group", "Image"))
	require.Error(t, ValidateAICCUploadURL(escaped, 202, "group", "Image"))
	encodedQuery := strings.Replace(r.URL, "expires=", "%65xpires=", 1)
	require.NoError(t, ValidateAICCUploadURL(encodedQuery, 101, "group", "Image"))
	require.Error(t, ValidateAICCUploadURL(encodedQuery, 202, "group", "Image"))
	common.CryptoSecret = ""
	require.Error(t, ValidateAICCUploadURL(r.URL, 101, "group", "Image"))
	require.NoError(t, ValidateAICCUploadURL("https://external.example/image.png", 101, "group", "Image"))
	_, err := NewAICCUploadStore(t.TempDir(), "", s.base)
	require.Error(t, err)
}

func TestAICCUploadInvalidAndLimits(t *testing.T) {
	s := aiccUploadTestStore(t)
	valid := aiccUploadTestPNG(t)
	for _, tc := range []struct {
		name, kind, mime string
		data             []byte
	}{{"empty", "Image", "image/png", nil}, {"HTML", "Image", "image/png", []byte("<html>evil</html>")}, {"SVG", "Image", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)}, {"random", "Video", "video/mp4", []byte("random media")}, {"wrong declared", "Image", "image/jpeg", valid}, {"wrong kind", "Video", "video/mp4", valid}, {"corrupt PNG", "Image", "image/png", valid[:64]}, {"AVIF", "Video", "video/mp4", []byte("\x00\x00\x00\x20ftypavif\x00\x00\x00\x00")}, {"playlist", "Video", "video/mp4", []byte("#EXTM3U\nfile:///etc/passwd")}} {
		t.Run(tc.name, func(t *testing.T) {
			_, e := s.Save(context.Background(), 101, "group", tc.kind, tc.mime, bytes.NewReader(tc.data))
			require.ErrorIs(t, e, ErrAICCUploadInvalid)
		})
	}
	for _, dims := range [][2]int{{300, 400}, {400, 6000}, {400, 1000}, {1000, 400}} {
		var b bytes.Buffer
		require.NoError(t, png.Encode(&b, image.NewGray(image.Rect(0, 0, dims[0], dims[1]))))
		_, e := s.Save(context.Background(), 101, "group", "Image", "image/png", &b)
		require.ErrorIs(t, e, ErrAICCUploadInvalid)
	}
	for _, kind := range []string{"Image", "Video", "Audio"} {
		_, e := s.Save(context.Background(), 101, "group", kind, "application/octet-stream", io.LimitReader(aiccUploadZeros{}, aiccUploadLimit(kind)+1))
		require.ErrorIs(t, e, ErrAICCUploadTooLarge)
	}
	entries, e := os.ReadDir(s.dir)
	require.NoError(t, e)
	require.Empty(t, entries)
}

type aiccUploadZeros struct{}

func (aiccUploadZeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func TestAICCUploadConcurrentQuotaAndCleanup(t *testing.T) {
	s := aiccUploadTestStore(t)
	data := aiccUploadTestPNG(t)
	s.userLimit = int64(len(data)) * 2
	var wg sync.WaitGroup
	var success atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			clone := *s
			r, e := clone.Save(context.Background(), 101, "group", "Image", "image/png", bytes.NewReader(data))
			if e == nil {
				success.Add(1)
				require.NotEmpty(t, r.ID)
			} else {
				require.ErrorIs(t, e, ErrAICCUploadQuota)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(2), success.Load())
	s.totalLimit = 2 * (int64(len(data)) + 4096)
	_, e := s.Save(context.Background(), 202, "group", "Image", "image/png", bytes.NewReader(data))
	require.ErrorIs(t, e, ErrAICCUploadQuota)
	unrelated := filepath.Join(s.dir, "do-not-delete.txt")
	require.NoError(t, os.WriteFile(unrelated, []byte("keep"), 0600))
	partial := strings.Repeat("a", 64) + ".part"
	require.NoError(t, os.WriteFile(filepath.Join(s.dir, partial), []byte("partial"), 0600))
	broken := strings.Repeat("b", 64) + ".json"
	require.NoError(t, os.WriteFile(filepath.Join(s.dir, broken), []byte("broken"), 0600))
	s.now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	require.NoError(t, s.Cleanup())
	entries, e := os.ReadDir(s.dir)
	require.NoError(t, e)
	require.Len(t, entries, 1)
	require.Equal(t, "do-not-delete.txt", entries[0].Name())
	s.now = time.Now
	_, e = s.Save(context.Background(), 101, "group", "Image", "image/png", bytes.NewReader(data))
	require.NoError(t, e)
}
func TestAICCUploadTamperingSymlinksAndTraversal(t *testing.T) {
	s := aiccUploadTestStore(t)
	r := aiccUploadTestSave(t, s)
	blob := filepath.Join(s.dir, r.ID+".blob")
	data, e := os.ReadFile(blob)
	require.NoError(t, e)
	data[len(data)-1] ^= 1
	require.NoError(t, os.WriteFile(blob, data, 0600))
	f, _, e := aiccUploadTestOpen(s, r)
	require.Nil(t, f)
	require.ErrorIs(t, e, ErrAICCUploadNotFound)
	require.NoError(t, s.Cleanup())
	_, e = os.Stat(blob)
	require.True(t, os.IsNotExist(e))
	r = aiccUploadTestSave(t, s)
	outside := filepath.Join(t.TempDir(), "sentinel")
	require.NoError(t, os.WriteFile(outside, []byte("private"), 0600))
	blob = filepath.Join(s.dir, r.ID+".blob")
	require.NoError(t, os.Remove(blob))
	require.NoError(t, os.Symlink(outside, blob))
	f, _, e = aiccUploadTestOpen(s, r)
	require.Nil(t, f)
	require.Error(t, e)
	require.NoError(t, s.Cleanup())
	b, e := os.ReadFile(outside)
	require.NoError(t, e)
	require.Equal(t, "private", string(b))
	r = aiccUploadTestSave(t, s)
	metaPath := filepath.Join(s.dir, r.ID+".json")
	b, e = os.ReadFile(metaPath)
	require.NoError(t, e)
	var m aiccUploadMeta
	require.NoError(t, json.Unmarshal(b, &m))
	m.Owner = 202
	b, e = json.Marshal(m)
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(metaPath, b, 0600))
	f, _, e = aiccUploadTestOpen(s, r)
	require.Nil(t, f)
	require.Error(t, e)
	symlinkDir := filepath.Join(t.TempDir(), "linked")
	require.NoError(t, os.Symlink(s.dir, symlinkDir))
	other, e := NewAICCUploadStore(symlinkDir, "test", "https://example.com")
	require.NoError(t, e)
	require.Error(t, other.Cleanup())
}
func TestAICCUploadRealFFprobe(t *testing.T) {
	if _, e := exec.LookPath("ffprobe"); e != nil {
		t.Skip("ffprobe not installed")
	}
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		t.Skip("ffmpeg fixture generator not installed")
	}
	s := aiccUploadTestStore(t)
	for _, tc := range []struct {
		name, kind, mime, duration string
		ok                         bool
	}{{"sound.wav", "Audio", "audio/wav", "3", true}, {"short.wav", "Audio", "audio/wav", "1", false}, {"long.wav", "Audio", "audio/wav", "16", false}, {"movie.mp4", "Video", "video/mp4", "3", true}, {"not-video.mp4", "Video", "video/mp4", "3", false}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.name)
			args := []string{"-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=16000", "-t", tc.duration}
			if tc.name == "movie.mp4" {
				args = []string{"-v", "error", "-f", "lavfi", "-i", "color=c=black:s=320x320:r=10", "-t", tc.duration, "-c:v", "libx264", "-pix_fmt", "yuv420p"}
			}
			args = append(args, path)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			out, e := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput()
			require.NoError(t, e, string(out))
			f, e := os.Open(path)
			require.NoError(t, e)
			defer f.Close()
			r, e := s.Save(context.Background(), 101, "group", tc.kind, tc.mime, f)
			if tc.ok {
				require.NoError(t, e)
				require.Equal(t, tc.mime, r.MIMEType)
			} else {
				require.ErrorIs(t, e, ErrAICCUploadInvalid)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e := s.Save(ctx, 101, "group", "Image", "image/png", bytes.NewReader(aiccUploadTestPNG(t)))
	require.ErrorIs(t, e, context.Canceled)
}
