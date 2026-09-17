package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAICCUploadCapabilityIsHiddenFromAccessLogButReachesHandler(t *testing.T) {
	var output bytes.Buffer
	previous := gin.DefaultWriter
	gin.DefaultWriter = &output
	t.Cleanup(func() { gin.DefaultWriter = previous })
	r := gin.New()
	SetUpLogger(r)
	r.GET("/api/aicc/upload-content/:id", func(c *gin.Context) {
		require.Equal(t, "private-capability", c.Query("access"))
		c.Status(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/aicc/upload-content/test?expires=123&access=private-capability", nil))
	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.NotContains(t, output.String(), "private-capability")
	require.NotContains(t, output.String(), "expires=123")
}
