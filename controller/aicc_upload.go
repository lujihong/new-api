package controller

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Bound multipart spool usage before the store's serialized quota accounting.
// One parser per process adds at most 51MiB transient disk usage; excess uploads
// fail fast rather than queueing unbounded request bodies or spill files.
var aiccUploadAdmission = make(chan struct{}, 1)

// CreateAICCUpload belongs behind TokenOrUserAuth, never a management route.
// Uploading only creates a temporary fetch capability; no upstream API is called.
func CreateAICCUpload(c *gin.Context) {
	if !checkAICCAuth(c) {
		return
	}
	// Reject the explicit scope even if its role/token combination is malformed.
	if c.GetBool("aicc_management_scope") || isAICCAdmin(c) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "请在本人素材组上传"})
		return
	}
	select {
	case aiccUploadAdmission <- struct{}{}:
		defer func() { <-aiccUploadAdmission }()
	default:
		aiccUploadError(c, service.ErrAICCUploadQuota)
		return
	}
	// Gin's base writer supports Unwrap. Middleware must preserve it (exclude
	// this route from the legacy gzip writer). Fail closed if unsupported.
	rc := http.NewResponseController(c.Writer)
	if err := rc.SetReadDeadline(time.Now().Add(120 * time.Second)); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "上传读取超时保护不可用"})
		return
	}
	defer rc.SetReadDeadline(time.Time{})
	r := c.Request
	r.Body = http.MaxBytesReader(c.Writer, r.Body, service.AICCUploadRequestLimit)
	defer r.Body.Close()
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	if r.ContentLength > service.AICCUploadRequestLimit {
		aiccUploadError(c, service.ErrAICCUploadTooLarge)
		return
	}
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			aiccUploadError(c, service.ErrAICCUploadTooLarge)
		} else {
			aiccUploadError(c, service.ErrAICCUploadInvalid)
		}
		return
	}
	// Multipart parsing can stop at the final boundary before reading epilogue
	// bytes. Drain through MaxBytesReader so chunked trailing data is capped too.
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		aiccUploadError(c, service.ErrAICCUploadTooLarge)
		return
	}
	form := r.MultipartForm
	if form == nil || len(form.File) != 1 || len(form.File["file"]) != 1 || len(form.Value) != 2 || len(form.Value["groupId"]) != 1 || len(form.Value["assetType"]) != 1 {
		aiccUploadError(c, service.ErrAICCUploadInvalid)
		return
	}
	group, kind := strings.TrimSpace(form.Value["groupId"][0]), form.Value["assetType"][0]
	if group == "" || len(group) > 191 || strings.ContainsAny(group, "\x00/\\?#") || (kind != "Image" && kind != "Video" && kind != "Audio") {
		aiccUploadError(c, service.ErrAICCUploadInvalid)
		return
	}
	if !requireAICCGroupOwner(c, group) {
		return
	}
	store, err := service.DefaultAICCUploadStore()
	if err != nil {
		aiccUploadError(c, err)
		return
	}
	part := form.File["file"][0]
	file, err := part.Open()
	if err != nil {
		aiccUploadError(c, err)
		return
	}
	defer file.Close()
	result, err := store.Save(r.Context(), aiccUserID(c), group, kind, part.Header.Get("Content-Type"), file)
	if err != nil {
		aiccUploadError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func aiccUploadError(c *gin.Context, err error) {
	code, message := http.StatusInternalServerError, "临时上传服务不可用"
	switch {
	case errors.Is(err, service.ErrAICCUploadInvalid):
		code = http.StatusBadRequest
		message = "文件类型或内容无效：图片仅支持JPEG/PNG/WebP，音视频须为2–15秒"
	case errors.Is(err, service.ErrAICCUploadTooLarge):
		code = http.StatusRequestEntityTooLarge
		message = "文件超限：图片30MiB、视频50MiB、音频15MiB，请求总量51MiB"
	case errors.Is(err, service.ErrAICCUploadQuota):
		code = http.StatusTooManyRequests
		message = "临时上传空间不足，请稍后重试"
	}
	c.JSON(code, gin.H{"success": false, "message": message})
}

// GetAICCUploadContent handles both GET and HEAD on the unauthenticated route.
// Fixed headers and direct descriptor streaming: no redirects, path reopening,
// content sniffing, conditional-cache handling, or permanent public artifacts.
func GetAICCUploadContent(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.Status(http.StatusNotFound)
		return
	}
	values, parseErr := url.ParseQuery(c.Request.URL.RawQuery)
	if parseErr != nil || len(values) != 2 || len(values["expires"]) != 1 || len(values["access"]) != 1 {
		c.Status(http.StatusNotFound)
		return
	}
	store, err := service.DefaultAICCUploadStore()
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	f, mt, err := store.Open(c.Param("id"), values["expires"][0], values["access"][0])
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Content-Type", mt)
	c.Header("Content-Length", strconv.FormatInt(st.Size(), 10))
	c.Header("Content-Disposition", "inline")
	c.Status(http.StatusOK)
	if c.Request.Method == http.MethodGet {
		_, _ = io.Copy(c.Writer, f)
	}
}
