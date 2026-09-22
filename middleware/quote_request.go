package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const QuoteBatchCountKey = "quote_batch_count"
const maxQuoteBodyBytes = 1 << 20

// QuoteRequest unwraps a bounded quote without replacing the authenticated or
// cancellable request context. It must run after TokenAuth and before Distribute.
func QuoteRequest() gin.HandlerFunc {
	return func(c *gin.Context) {
		reject := func(message string) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": message})
		}
		data, err := io.ReadAll(io.LimitReader(c.Request.Body, maxQuoteBodyBytes+1))
		if err != nil || len(data) > maxQuoteBodyBytes {
			reject("quote request must be valid JSON of at most 1 MiB")
			return
		}
		var wrapper struct {
			Endpoint   string          `json:"endpoint"`
			Body       json.RawMessage `json:"body"`
			BatchCount *int            `json:"batch_count"`
		}
		if err := common.Unmarshal(data, &wrapper); err != nil {
			reject("invalid quote JSON")
			return
		}
		switch wrapper.Endpoint {
		case "/v1/images/generations", "/v1/images/edits", "/v1/videos", "/api/v3/contents/generations/tasks", "/v1/chat/completions", "/v1/audio/speech", "/v1/responses":
		default:
			reject("unsupported quote endpoint")
			return
		}
		batch := 1
		if wrapper.BatchCount != nil {
			batch = *wrapper.BatchCount
		}
		if batch < 1 || batch > 20 {
			reject("batch_count must be between 1 and 20")
			return
		}
		var body map[string]json.RawMessage
		if err := common.Unmarshal(wrapper.Body, &body); err != nil || body == nil {
			reject("body must be a JSON object")
			return
		}
		var model string
		if common.Unmarshal(body["model"], &model) != nil || strings.TrimSpace(model) == "" || model != strings.TrimSpace(model) || countTopLevelJSONKey(wrapper.Body, "model") != 1 {
			reject("body.model must be a nonempty model identifier")
			return
		}
		// Pricing authority is always server-side. These fields must not reach a
		// request-aware expression or a task plugin as purported billing facts.
		for key := range body {
			normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
			switch normalized {
			case "group", "discount", "baseurl", "groupratio", "usage", "key", "apikey", "quota", "modelprice", "otherratios":
				reject("client billing authority fields are not accepted")
				return
			}
		}
		common.CleanupBodyStorage(c)
		c.Set(common.KeyRequestBody, nil)
		c.Set(gin.BodyBytesKey, nil)
		_ = c.Request.Body.Close()
		c.Request.Body = io.NopCloser(bytes.NewReader(wrapper.Body))
		c.Request.GetBody = nil
		c.Request.ContentLength = int64(len(wrapper.Body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Del("Content-Length")
		c.Request.URL.Path = wrapper.Endpoint
		c.Request.URL.RawPath = ""
		c.Request.URL.RawQuery = ""
		c.Request.RequestURI = wrapper.Endpoint
		c.Set(QuoteBatchCountKey, batch)
		c.Header("Cache-Control", "no-store")
		defer common.CleanupBodyStorage(c)
		c.Next()
	}
}
