package controller

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type aiccUploadDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *aiccUploadDeadlineRecorder) SetReadDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func TestAICCUploadControllerDeadlineSupport(t *testing.T) {
	setupAICCUploadController(t)
	c, _ := aiccUploadControllerRequest(t, aiccUploadControllerPNG(t), "mine", "Image", "image/png")
	writer := &aiccUploadDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	check, _ := gin.CreateTestContext(writer)
	check.Request = c.Request
	check.Set("id", 101)
	CreateAICCUpload(check)
	require.Equal(t, 200, writer.Code)
	require.Len(t, writer.deadlines, 2)
	require.WithinDuration(t, time.Now().Add(120*time.Second), writer.deadlines[0], 5*time.Second)
	require.True(t, writer.deadlines[1].IsZero())
	rec := httptest.NewRecorder()
	unsupported, _ := gin.CreateTestContext(rec)
	unsupported.Set("id", 101)
	unsupported.Request = httptest.NewRequest(http.MethodPost, "/api/aicc/uploads", nil)
	CreateAICCUpload(unsupported)
	require.Equal(t, 503, rec.Code)
	require.Len(t, aiccUploadAdmission, 0)
	// Real net/http connection proves Gin Unwrap reaches a deadline-capable writer.
	engine := gin.New()
	engine.POST("/api/aicc/uploads", func(c *gin.Context) { c.Set("id", 101); CreateAICCUpload(c) })
	server := httptest.NewServer(engine)
	defer server.Close()
	c, _ = aiccUploadControllerRequest(t, aiccUploadControllerPNG(t), "mine", "Image", "image/png")
	req, e := http.NewRequest(http.MethodPost, server.URL+"/api/aicc/uploads", c.Request.Body)
	require.NoError(t, e)
	req.Header = c.Request.Header
	response, e := server.Client().Do(req)
	require.NoError(t, e)
	defer response.Body.Close()
	require.Equal(t, 200, response.StatusCode)
}

func setupAICCUploadController(t *testing.T) {
	t.Helper()
	setupAICCTestDB(t)
	t.Setenv("AICC_UPLOAD_DIR", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	secret, base, fallback := common.CryptoSecret, system_setting.TaskPublicAddress, system_setting.ServerAddress
	common.CryptoSecret = "test-controller-upload-secret"
	system_setting.TaskPublicAddress = "https://configured.example"
	system_setting.ServerAddress = "https://fallback.example"
	t.Cleanup(func() {
		common.CryptoSecret = secret
		system_setting.TaskPublicAddress = base
		system_setting.ServerAddress = fallback
	})
	require.NoError(t, model.RecordAICCAssetGroupOwnership(101, "mine", "AIGC"))
	require.NoError(t, model.RecordAICCAssetGroupOwnership(202, "other", "AIGC"))
}
func aiccUploadControllerPNG(t *testing.T) []byte {
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 400, 400))))
	return b.Bytes()
}
func aiccUploadControllerRequest(t *testing.T, data []byte, group, kind, mt string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	require.NoError(t, w.WriteField("groupId", group))
	require.NoError(t, w.WriteField("assetType", kind))
	header := textproto.MIMEHeader{}
	// Intentionally hostile filename: never used as a storage path or format hint.
	header.Set("Content-Disposition", `form-data; name="file"; filename="../../fake.html"`)
	header.Set("Content-Type", mt)
	p, e := w.CreatePart(header)
	require.NoError(t, e)
	_, e = p.Write(data)
	require.NoError(t, e)
	require.NoError(t, w.Close())
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(&aiccUploadDeadlineRecorder{ResponseRecorder: rec})
	c.Set("id", 101)
	c.Set("role", common.RoleCommonUser)
	c.Request = httptest.NewRequest(http.MethodPost, "http://attacker.example/api/aicc/uploads", &b)
	c.Request.Header.Set("Content-Type", w.FormDataContentType())
	return c, rec
}
func aiccUploadControllerDownload(r service.AICCUploadResult, method string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(&aiccUploadDeadlineRecorder{ResponseRecorder: rec})
	c.Request = httptest.NewRequest(method, r.URL, nil)
	c.Params = gin.Params{{Key: "id", Value: r.ID}}
	return c, rec
}
func TestAICCUploadControllerPNGDownloadHead(t *testing.T) {
	setupAICCUploadController(t)
	data := aiccUploadControllerPNG(t)
	c, rec := aiccUploadControllerRequest(t, data, "mine", "Image", "image/png")
	CreateAICCUpload(c)
	require.Equal(t, http.StatusOK, rec.Code)
	var envelope struct {
		Success bool                     `json:"success"`
		Data    service.AICCUploadResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	require.True(t, envelope.Success)
	r := envelope.Data
	require.True(t, strings.HasPrefix(r.URL, "https://configured.example/api/aicc/upload-content/"))
	require.NotContains(t, r.URL, "attacker.example")
	require.Equal(t, int64(len(data)), r.Bytes)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		c, rec = aiccUploadControllerDownload(r, method)
		GetAICCUploadContent(c)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "image/png", rec.Header().Get("Content-Type"))
		require.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
		require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
		require.Empty(t, rec.Header().Get("Location"))
		if method == http.MethodGet {
			require.Equal(t, data, rec.Body.Bytes())
		} else {
			require.Empty(t, rec.Body.Bytes())
		}
	}
	u, e := url.Parse(r.URL)
	require.NoError(t, e)
	q := u.Query()
	for _, change := range []string{"signature", "expiry", "duplicate", "traversal"} {
		invalid := r
		switch change {
		case "signature":
			q.Set("access", strings.Repeat("0", 64))
		case "expiry":
			q = u.Query()
			q.Set("expires", "1")
		case "duplicate":
			q = u.Query()
			q.Add("expires", q.Get("expires"))
		case "traversal":
			q = u.Query()
			invalid.ID = "../" + r.ID
		}
		bad := *u
		bad.RawQuery = q.Encode()
		invalid.URL = bad.String()
		c, rec = aiccUploadControllerDownload(invalid, http.MethodGet)
		GetAICCUploadContent(c)
		c.Writer.WriteHeaderNow()
		require.Equal(t, http.StatusNotFound, rec.Code)
		require.Empty(t, rec.Body.Bytes())
	}
}
func TestAICCUploadControllerAuthTypesAndRequestLimits(t *testing.T) {
	setupAICCUploadController(t)
	pngData := aiccUploadControllerPNG(t)
	for _, tc := range []struct {
		name, group, kind, mt string
		id, role              int
		scope                 bool
		data                  []byte
		status                int
	}{
		{"unauth", "mine", "Image", "image/png", 0, common.RoleCommonUser, false, pngData, 401},
		{"other user", "other", "Image", "image/png", 101, common.RoleCommonUser, false, pngData, 403},
		{"ordinary admin still personal", "other", "Image", "image/png", 101, common.RoleRootUser, false, pngData, 403},
		{"management rejected", "mine", "Image", "image/png", 101, common.RoleRootUser, true, pngData, 403},
		{"wrong MIME", "mine", "Image", "image/jpeg", 101, common.RoleCommonUser, false, pngData, 400},
		{"html spoof", "mine", "Image", "image/png", 101, common.RoleCommonUser, false, []byte("<html>bad</html>"), 400},
		{"empty", "mine", "Image", "image/png", 101, common.RoleCommonUser, false, nil, 400},
		{"wrong type", "mine", "Video", "video/mp4", 101, common.RoleCommonUser, false, pngData, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := aiccUploadControllerRequest(t, tc.data, tc.group, tc.kind, tc.mt)
			c.Set("id", tc.id)
			c.Set("role", tc.role)
			c.Set("aicc_management_scope", tc.scope)
			CreateAICCUpload(c)
			require.Equal(t, tc.status, rec.Code)
		})
	}
	c, rec := aiccUploadControllerRequest(t, pngData, "mine", "Image", "image/png")
	c.Request.ContentLength = service.AICCUploadRequestLimit + 1
	CreateAICCUpload(c)
	require.Equal(t, 413, rec.Code)
	// Real unknown-length multipart body exceeds the cap; parser spill files must
	// be removed on error. Does not rely on a truthful Content-Length header.
	var prefix bytes.Buffer
	w := multipart.NewWriter(&prefix)
	require.NoError(t, w.WriteField("groupId", "mine"))
	require.NoError(t, w.WriteField("assetType", "Video"))
	_, e := w.CreateFormFile("file", "file.mp4")
	require.NoError(t, e)
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(&aiccUploadDeadlineRecorder{ResponseRecorder: rec})
	c.Set("id", 101)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/aicc/uploads", io.MultiReader(&prefix, io.LimitReader(aiccUploadControllerZeros{}, service.AICCUploadRequestLimit+1)))
	c.Request.Header.Set("Content-Type", w.FormDataContentType())
	CreateAICCUpload(c)
	require.Equal(t, 413, rec.Code)
	entries, e := os.ReadDir(os.Getenv("TMPDIR"))
	require.NoError(t, e)
	require.Empty(t, entries)
}

type aiccUploadControllerZeros struct{}

func (aiccUploadControllerZeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func TestAICCUploadControllerSpillCleanupAndFallback(t *testing.T) {
	setupAICCUploadController(t)
	system_setting.TaskPublicAddress = ""
	data := append(aiccUploadControllerPNG(t), make([]byte, 2<<20)...)
	c, rec := aiccUploadControllerRequest(t, data, "mine", "Image", "image/png")
	CreateAICCUpload(c)
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Body.String(), "https://fallback.example/")
	entries, e := os.ReadDir(os.Getenv("TMPDIR"))
	require.NoError(t, e)
	require.Empty(t, entries)
	system_setting.TaskPublicAddress = "javascript:bad"
	c, rec = aiccUploadControllerRequest(t, data, "mine", "Image", "image/png")
	CreateAICCUpload(c)
	require.Equal(t, 500, rec.Code)
	entries, e = os.ReadDir(os.Getenv("TMPDIR"))
	require.NoError(t, e)
	require.Empty(t, entries)
}
