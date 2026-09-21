package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"mime"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"golang.org/x/image/webp"
	"golang.org/x/sys/unix"
)

const (
	AICCUploadRequestLimit int64 = 51 << 20
	aiccUploadLifetime           = 24 * time.Hour
	aiccUploadUserLimit    int64 = 200 << 20
	aiccUploadTotalLimit   int64 = 2 << 30
)

var (
	ErrAICCUploadInvalid  = errors.New("invalid upload (supported images: JPEG/PNG/WebP; media duration: 2–15 seconds)")
	ErrAICCUploadTooLarge = errors.New("upload size limit exceeded")
	ErrAICCUploadQuota    = errors.New("temporary upload quota exceeded")
	ErrAICCUploadNotFound = errors.New("upload not found")
	aiccUploadMutex       sync.Mutex
)

type AICCUploadResult struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	AssetType string `json:"assetType"`
	MIMEType  string `json:"mimeType"`
	Bytes     int64  `json:"bytes"`
	ExpiresAt int64  `json:"expiresAt"`
}
type aiccUploadMeta struct {
	ID      string `json:"id"`
	Owner   int    `json:"owner"`
	Group   string `json:"group"`
	Type    string `json:"type"`
	MIME    string `json:"mime"`
	Created int64  `json:"created"`
	Expires int64  `json:"expires"`
	Bytes   int64  `json:"bytes"`
	Hash    string `json:"hash"`
	// Omitted on v1 records so their existing HMAC remains valid.
	Version       int    `json:"version,omitempty"`
	ChannelID     int    `json:"channel,omitempty"`
	AICCAccountID string `json:"account,omitempty"`
}

// AICCUploadStore is independently constructible without production environment.
// A directory must be dedicated to this module on a local filesystem supporting
// flock. All instances/processes sharing it serialize accounting and mutation.
// Keep the directory private; changing the signing secret invalidates old URLs.
type AICCUploadStore struct {
	dir                   string
	secret                []byte
	base                  string
	now                   func() time.Time
	userLimit, totalLimit int64
}

func NewAICCUploadStore(dir, secret, base string) (*AICCUploadStore, error) {
	if !filepath.IsAbs(dir) || secret == "" {
		return nil, errors.New("upload storage configuration unavailable")
	}
	if err := ValidateTaskArtifactBaseURL(base); err != nil {
		return nil, err
	}
	return &AICCUploadStore{dir: filepath.Clean(dir), secret: []byte(secret), base: strings.TrimRight(base, "/"), now: time.Now, userLimit: aiccUploadUserLimit, totalLimit: aiccUploadTotalLimit}, nil
}
func DefaultAICCUploadStore() (*AICCUploadStore, error) {
	dir := os.Getenv("AICC_UPLOAD_DIR")
	if dir == "" {
		dir = "/data/aicc-uploads"
	}
	base := system_setting.TaskPublicAddress
	if base == "" {
		base = system_setting.ServerAddress
	}
	return NewAICCUploadStore(dir, common.CryptoSecret, base)
}
func aiccUploadID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func aiccUploadLimit(kind string) int64 {
	switch kind {
	case "Image":
		return 30 << 20
	case "Video":
		return 50 << 20
	case "Audio":
		return 15 << 20
	}
	return 0
}
func (s *AICCUploadStore) locked(fn func(*os.Root) error) error {
	aiccUploadMutex.Lock()
	defer aiccUploadMutex.Unlock()
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.dir, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = f.Chmod(0700); err != nil {
		return err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	a, err := f.Stat()
	if err != nil {
		return err
	}
	b, err := root.Stat(".")
	if err != nil || !os.SameFile(a, b) {
		return ErrAICCUploadInvalid
	}
	return fn(root)
}
func aiccUploadOpen(root *os.Root, name string) (*os.File, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		f.Close()
		return nil, ErrAICCUploadNotFound
	}
	return f, nil
}
func (s *AICCUploadStore) read(root *os.Root, id string, hash bool) (aiccUploadMeta, *os.File, error) {
	var m aiccUploadMeta
	if !aiccUploadID(id) {
		return m, nil, ErrAICCUploadNotFound
	}
	meta, err := aiccUploadOpen(root, id+".json")
	if err != nil {
		return m, nil, ErrAICCUploadNotFound
	}
	data, err := io.ReadAll(io.LimitReader(meta, 4097))
	meta.Close()
	if err != nil || len(data) > 4096 || json.Unmarshal(data, &m) != nil || m.ID != id || m.Owner <= 0 || m.Group == "" || len(m.Group) > 191 || m.Bytes <= 0 || m.Bytes > aiccUploadLimit(m.Type) || !aiccUploadID(m.Hash) || m.Expires-m.Created != int64(aiccUploadLifetime/time.Second) || m.Created > s.now().Unix() || m.Expires <= s.now().Unix() {
		return m, nil, ErrAICCUploadNotFound
	}
	if (m.Version != 0 && m.Version != 1 && m.Version != 2) || (m.Version < 2 && (m.ChannelID != 0 || m.AICCAccountID != "")) || (m.Version == 2 && !(model.AICCBinding{ChannelID: m.ChannelID, AICCAccountID: m.AICCAccountID}).Valid()) {
		return m, nil, ErrAICCUploadNotFound
	}
	if !aiccUploadMIMEAllowed(m.Type, m.MIME) {
		return m, nil, ErrAICCUploadNotFound
	}
	f, err := aiccUploadOpen(root, id+".blob")
	if err != nil {
		return m, nil, ErrAICCUploadNotFound
	}
	st, _ := f.Stat()
	if st.Size() != m.Bytes {
		f.Close()
		return m, nil, ErrAICCUploadNotFound
	}
	if hash {
		h := sha256.New()
		_, err = io.Copy(h, f)
		if err != nil || hex.EncodeToString(h.Sum(nil)) != m.Hash {
			f.Close()
			return m, nil, ErrAICCUploadNotFound
		}
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			f.Close()
			return m, nil, err
		}
	}
	return m, f, nil
}
func aiccUploadMIMEAllowed(kind, mt string) bool {
	switch kind {
	case "Image":
		return mt == "image/jpeg" || mt == "image/png" || mt == "image/webp"
	case "Video":
		return mt == "video/mp4" || mt == "video/quicktime"
	case "Audio":
		return mt == "audio/mp4" || mt == "audio/mpeg" || mt == "audio/wav" || mt == "audio/flac" || mt == "audio/ogg"
	}
	return false
}

// cleanupLocked only touches this module's exact random-ID namespace, never
// recursively removes directories and never follows symlinks. Incomplete pairs
// are safe to remove immediately because uploads hold the same process/OS lock.
func (s *AICCUploadStore) cleanupLocked(root *os.Root) (map[int]int64, int64, int, error) {
	d, err := root.Open(".")
	if err != nil {
		return nil, 0, 0, err
	}
	entries, err := d.ReadDir(-1)
	d.Close()
	if err != nil {
		return nil, 0, 0, err
	}
	ids := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		ext := filepath.Ext(name)
		id := strings.TrimSuffix(name, ext)
		if aiccUploadID(id) && (ext == ".blob" || ext == ".json" || ext == ".part") {
			ids[id] = true
		}
	}
	users := map[int]int64{}
	var total int64
	count := 0
	remove := func(name string) error {
		st, e := root.Lstat(name)
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if st.IsDir() {
			return errors.New("unexpected directory in upload namespace")
		}
		return root.Remove(name)
	}
	for id := range ids {
		m, f, e := s.read(root, id, true)
		if e != nil {
			for _, ext := range []string{".json", ".blob", ".part"} {
				if err = remove(id + ext); err != nil {
					return nil, 0, 0, err
				}
			}
			continue
		}
		f.Close()
		if err = remove(id + ".part"); err != nil {
			return nil, 0, 0, err
		}
		users[m.Owner] += m.Bytes
		total += m.Bytes + 4096
		count++
	}
	return users, total, count, nil
}
func (s *AICCUploadStore) Cleanup() error {
	return s.locked(func(r *os.Root) error { _, _, _, err := s.cleanupLocked(r); return err })
}

// StartAICCUploadCleanup must be called once with the server lifetime context.
// Runs even without traffic; no goroutine or environment access at package init.
func StartAICCUploadCleanup(ctx context.Context) {
	go func() {
		tick := time.NewTicker(10 * time.Minute)
		defer tick.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			if s, err := DefaultAICCUploadStore(); err == nil {
				if err = s.Cleanup(); err != nil {
					common.SysError("AICC upload cleanup failed")
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}
func (s *AICCUploadStore) access(m aiccUploadMeta) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte("aicc-temporary-upload/v1\x00"))
	data, _ := json.Marshal(m)
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}
func (s *AICCUploadStore) contentURL(m aiccUploadMeta) string {
	u, _ := url.Parse(s.base)
	u.Path = strings.TrimRight(u.Path, "/") + "/api/aicc/upload-content/" + m.ID
	u.RawPath = ""
	q := url.Values{"expires": {strconv.FormatInt(m.Expires, 10)}, "access": {s.access(m)}}
	u.RawQuery = q.Encode()
	return u.String()
}
func (s *AICCUploadStore) Save(ctx context.Context, owner int, group, kind, declaredMIME string, src io.Reader, binding ...model.AICCBinding) (AICCUploadResult, error) {
	var result AICCUploadResult
	if len(binding) > 1 || (len(binding) == 1 && !binding[0].Valid()) {
		return result, ErrAICCUploadInvalid
	}
	if owner <= 0 || group == "" || len(group) > 191 || strings.ContainsAny(group, "\x00/\\?#") || aiccUploadLimit(kind) == 0 || src == nil {
		return result, ErrAICCUploadInvalid
	}
	err := s.locked(func(root *os.Root) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		users, total, count, err := s.cleanupLocked(root)
		if err != nil {
			return err
		}
		if count >= 4096 {
			return ErrAICCUploadQuota
		}
		budget := aiccUploadLimit(kind)
		quotaBudget := s.userLimit - users[owner]
		if q := s.totalLimit - total - 4096; q < quotaBudget {
			quotaBudget = q
		}
		if quotaBudget < 0 {
			return ErrAICCUploadQuota
		}
		if quotaBudget < budget {
			budget = quotaBudget
		}
		random := make([]byte, 32)
		if _, err = rand.Read(random); err != nil {
			return err
		}
		id := hex.EncodeToString(random)
		f, err := root.OpenFile(id+".part", os.O_CREATE|os.O_EXCL|os.O_RDWR|unix.O_NOFOLLOW, 0600)
		if err != nil {
			return err
		}
		committed := false
		defer func() {
			f.Close()
			if !committed {
				root.Remove(id + ".part")
				root.Remove(id + ".blob")
				root.Remove(id + ".json")
			}
		}()
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(src, budget+1))
		if err != nil {
			return err
		}
		if n > budget {
			if budget < aiccUploadLimit(kind) {
				return ErrAICCUploadQuota
			}
			return ErrAICCUploadTooLarge
		}
		if n == 0 {
			return ErrAICCUploadInvalid
		}
		mt, err := aiccUploadValidate(ctx, f, filepath.Join(s.dir, id+".part"), kind)
		if err != nil {
			return err
		}
		if !aiccUploadDeclaredMIME(declaredMIME, mt) {
			return ErrAICCUploadInvalid
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		now := s.now().Unix()
		m := aiccUploadMeta{ID: id, Owner: owner, Group: group, Type: kind, MIME: mt, Created: now, Expires: now + int64(aiccUploadLifetime/time.Second), Bytes: n, Hash: hex.EncodeToString(h.Sum(nil))}
		if len(binding) == 1 {
			m.Version, m.ChannelID, m.AICCAccountID = 2, binding[0].ChannelID, binding[0].AICCAccountID
		}
		if err = f.Sync(); err != nil {
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		if err = root.Rename(id+".part", id+".blob"); err != nil {
			return err
		}
		data, _ := json.Marshal(m)
		mf, err := root.OpenFile(id+".json", os.O_CREATE|os.O_EXCL|os.O_WRONLY|unix.O_NOFOLLOW, 0600)
		if err != nil {
			return err
		}
		_, err = mf.Write(data)
		if err == nil {
			err = mf.Sync()
		}
		ce := mf.Close()
		if err != nil {
			return err
		}
		if ce != nil {
			return ce
		}
		committed = true
		result = AICCUploadResult{ID: id, URL: s.contentURL(m), AssetType: kind, MIMEType: mt, Bytes: n, ExpiresAt: m.Expires}
		return nil
	})
	return result, err
}
func aiccUploadDeclaredMIME(declared, actual string) bool {
	if declared == "" {
		return true
	}
	mt, _, err := mime.ParseMediaType(declared)
	if err != nil {
		return false
	}
	if mt == "application/octet-stream" {
		return true
	}
	if actual == "audio/wav" && (mt == "audio/x-wav" || mt == "audio/wave") {
		return true
	}
	if actual == "audio/flac" && mt == "audio/x-flac" {
		return true
	}
	if actual == "audio/mp4" && mt == "audio/x-m4a" {
		return true
	}
	return mt == actual
}

// Open verifies the complete capability AND current content hash. Caller closes
// the returned descriptor; it is never reopened using a request-derived path.
func (s *AICCUploadStore) Open(id, expires, access string) (*os.File, string, error) {
	var out *os.File
	var mt string
	if !aiccUploadID(id) || !aiccUploadID(access) {
		return nil, "", ErrAICCUploadNotFound
	}
	err := s.locked(func(root *os.Root) error {
		m, f, err := s.read(root, id, false)
		if err != nil {
			return ErrAICCUploadNotFound
		}
		if expires != strconv.FormatInt(m.Expires, 10) || !hmac.Equal([]byte(access), []byte(s.access(m))) {
			f.Close()
			return ErrAICCUploadNotFound
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		if err != nil || hex.EncodeToString(h.Sum(nil)) != m.Hash {
			f.Close()
			return ErrAICCUploadNotFound
		}
		if _, err = f.Seek(0, 0); err != nil {
			f.Close()
			return ErrAICCUploadNotFound
		}
		out = f
		mt = m.MIME
		return nil
	})
	if err != nil {
		return nil, "", ErrAICCUploadNotFound
	}
	return out, mt, nil
}

// ValidateAICCUploadURL leaves external URLs to the existing upstream policy.
// Reserved upload paths are recognized before loading configuration, so missing
// secrets/configuration fail closed only for internal upload capabilities.
func ValidateAICCUploadURL(raw string, userID int, groupID, assetType string, binding ...model.AICCBinding) error {
	u, err := url.Parse(raw)
	if err != nil {
		return ErrAICCUploadInvalid
	}
	if !aiccUploadReservedPath(u.Path) {
		return nil
	}
	s, err := DefaultAICCUploadStore()
	if err != nil {
		return ErrAICCUploadNotFound
	}
	_, err = s.ValidateURL(raw, userID, groupID, assetType, binding...)
	return err
}
func aiccUploadReservedPath(p string) bool {
	// Multiple encodings, backslashes, duplicate slashes and dot segments must
	// never make a capability URL look like an unrelated external URL.
	for i := 0; i < 5; i++ {
		normalized := path.Clean(strings.ReplaceAll(p, "\\", "/"))
		if strings.Contains(normalized, "/api/aicc/upload-content") {
			return true
		}
		decoded, err := url.PathUnescape(p)
		if err != nil || decoded == p {
			break
		}
		p = decoded
	}
	return false
}

// ValidateURL returns (false,nil) for external URLs, (true,error) for rejected
// internal URLs, and (true,nil) for an owned, unexpired capability. Call BEFORE
// forwarding an asset URL upstream; this helper itself performs no HTTP fetch.
func (s *AICCUploadStore) ValidateURL(raw string, owner int, group, kind string, binding ...model.AICCBinding) (bool, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return false, ErrAICCUploadInvalid
	}
	base, _ := url.Parse(s.base)
	prefix := strings.TrimRight(base.Path, "/") + "/api/aicc/upload-content/"
	// Recognize this reserved path even on another host, preventing host aliases
	// and changed configuration from bypassing the ownership check.
	if !aiccUploadReservedPath(u.Path) {
		return false, nil
	}
	if u.Scheme != base.Scheme || !strings.EqualFold(u.Host, base.Host) || u.User != nil || u.Fragment != "" || !strings.HasPrefix(u.Path, prefix) {
		return true, ErrAICCUploadNotFound
	}
	id := strings.TrimPrefix(u.Path, prefix)
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) != 2 || len(q["expires"]) != 1 || len(q["access"]) != 1 {
		return true, ErrAICCUploadNotFound
	}
	err = s.locked(func(root *os.Root) error {
		m, f, e := s.read(root, id, true)
		if e != nil {
			return ErrAICCUploadNotFound
		}
		defer f.Close()
		if len(binding) > 1 || (len(binding) == 1 && (!binding[0].Valid() || m.Version != 2 || m.ChannelID != binding[0].ChannelID || m.AICCAccountID != binding[0].AICCAccountID)) {
			return ErrAICCUploadNotFound
		}
		if m.Owner != owner || m.Group != group || m.Type != kind || q.Get("expires") != strconv.FormatInt(m.Expires, 10) || !hmac.Equal([]byte(q.Get("access")), []byte(s.access(m))) {
			return ErrAICCUploadNotFound
		}
		return nil
	})
	return true, err
}

func aiccUploadValidate(ctx context.Context, f *os.File, path, kind string) (string, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return "", err
	}
	if kind == "Image" {
		header := make([]byte, 12)
		n, _ := f.Read(header)
		header = header[:n]
		f.Seek(0, 0)
		var config func(io.Reader) (image.Config, error)
		var decode func(io.Reader) (image.Image, error)
		mt := ""
		switch {
		case bytes.HasPrefix(header, []byte("\x89PNG\r\n\x1a\n")):
			config = png.DecodeConfig
			decode = png.Decode
			mt = "image/png"
		case bytes.HasPrefix(header, []byte("\xff\xd8\xff")):
			config = jpeg.DecodeConfig
			decode = jpeg.Decode
			mt = "image/jpeg"
		case len(header) == 12 && string(header[:4]) == "RIFF" && string(header[8:]) == "WEBP":
			config = webp.DecodeConfig
			decode = webp.Decode
			mt = "image/webp"
		default:
			return "", ErrAICCUploadInvalid
		}
		c, err := config(f)
		if err != nil {
			return "", ErrAICCUploadInvalid
		}
		// Platform safety limits, not a claim about official Mobile documentation.
		ratio := float64(c.Width) / float64(c.Height)
		if c.Width <= 300 || c.Height <= 300 || c.Width >= 6000 || c.Height >= 6000 || ratio <= 0.4 || ratio >= 2.5 {
			return "", ErrAICCUploadInvalid
		}
		f.Seek(0, 0)
		img, err := decode(f)
		if err != nil || img.Bounds().Dx() != c.Width || img.Bounds().Dy() != c.Height {
			return "", ErrAICCUploadInvalid
		}
		return mt, nil
	}
	header := make([]byte, 512)
	n, _ := f.Read(header)
	header = header[:n]
	format, mt := "", ""
	switch {
	case len(header) >= 16 && string(header[4:8]) == "ftyp":
		// Small closed brand allowlist excludes AVIF/HEIC/image containers, even
		// though ffprobe describes them as a video stream in the MOV demuxer.
		brand := string(header[8:12])
		switch brand {
		case "isom", "iso2", "mp41", "mp42", "avc1", "M4V ", "M4A ", "qt  ":
		default:
			return "", ErrAICCUploadInvalid
		}
		format = "mov"
		mt = "video/mp4"
		if brand == "qt  " {
			mt = "video/quicktime"
		}
		if kind == "Audio" {
			mt = "audio/mp4"
		}
	case len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WAVE":
		format = "wav"
		mt = "audio/wav"
	case bytes.HasPrefix(header, []byte("fLaC")):
		format = "flac"
		mt = "audio/flac"
	case bytes.HasPrefix(header, []byte("OggS")):
		format = "ogg"
		mt = "audio/ogg"
	case bytes.HasPrefix(header, []byte("ID3")) || len(header) >= 2 && header[0] == 255 && header[1]&0xe0 == 0xe0:
		format = "mp3"
		mt = "audio/mpeg"
	default:
		return "", ErrAICCUploadInvalid
	}
	if !aiccUploadMIMEAllowed(kind, mt) {
		return "", ErrAICCUploadInvalid
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Forced demuxer + disabled MOV external references prevent playlist/network
	// inputs and arbitrary local-file reads. No shell, user filenames or flags.
	args := []string{"-v", "error", "-max_alloc", "67108864", "-threads", "1", "-probesize", "8388608", "-analyzeduration", "5000000", "-protocol_whitelist", "file", "-f", format}
	if format == "mov" {
		args = append(args, "-enable_drefs", "0", "-use_absolute_path", "0")
	}
	args = append(args, "-show_format", "-show_streams", "-of", "json", path)
	cmd := exec.CommandContext(probeCtx, "ffprobe", args...)
	out := &aiccUploadProbeBuffer{}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", ErrAICCUploadInvalid
	}
	var probe struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Type        string `json:"codec_type"`
			Codec       string `json:"codec_name"`
			Width       int    `json:"width"`
			Height      int    `json:"height"`
			Duration    string `json:"duration"`
			Channels    int    `json:"channels"`
			Frames      string `json:"nb_frames"`
			Disposition struct {
				Attached int `json:"attached_pic"`
				Still    int `json:"still_image"`
			} `json:"disposition"`
		} `json:"streams"`
	}
	if json.Unmarshal(out.Bytes(), &probe) != nil || len(probe.Streams) == 0 {
		return "", ErrAICCUploadInvalid
	}
	duration, err := strconv.ParseFloat(probe.Format.Duration, 64)
	if err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration < 2 || duration > 15 {
		return "", ErrAICCUploadInvalid
	}
	video, audio := false, false
	for _, st := range probe.Streams {
		if st.Duration != "" && st.Duration != "N/A" {
			d, e := strconv.ParseFloat(st.Duration, 64)
			if e != nil || math.IsNaN(d) || math.IsInf(d, 0) || d <= 0 || d > 15 {
				return "", ErrAICCUploadInvalid
			}
		}
		switch st.Type {
		case "video":
			frames, frameErr := strconv.ParseInt(st.Frames, 10, 64)
			d, durationErr := strconv.ParseFloat(st.Duration, 64)
			// A real MOV/MP4 video track needs multiple samples and its own
			// 2–15 second duration; an audio track cannot pad a still image.
			if frameErr != nil || frames < 2 || durationErr != nil || math.IsNaN(d) || math.IsInf(d, 0) || d < 2 || d > 15 {
				return "", ErrAICCUploadInvalid
			}
			if kind != "Video" || st.Disposition.Attached != 0 || st.Disposition.Still != 0 || st.Width <= 0 || st.Height <= 0 {
				return "", ErrAICCUploadInvalid
			}
			switch st.Codec {
			case "h264", "hevc", "av1", "mpeg4", "vp9":
			default:
				return "", ErrAICCUploadInvalid
			}
			video = true
		case "audio":
			if st.Codec == "" || st.Codec == "unknown" || st.Channels <= 0 {
				return "", ErrAICCUploadInvalid
			}
			audio = true
		default:
			return "", ErrAICCUploadInvalid
		}
	}
	if kind == "Video" && !video || kind == "Audio" && (!audio || video) {
		return "", ErrAICCUploadInvalid
	}
	return mt, nil
}

type aiccUploadProbeBuffer struct{ bytes.Buffer }

func (b *aiccUploadProbeBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, fmt.Errorf("probe output limit")
	}
	return b.Buffer.Write(p)
}
