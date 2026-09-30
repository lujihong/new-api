package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// This is the exact shape stored by the production Seedance catalog.
func TestModelSaveWithProductionEmptyEndpointObjects(t *testing.T) {
	db := modelManagementDB(t, "sqlite", "")
	legacy := `{"openai-video":{},"openai":{}}`
	want := `{"openai-video":{"path":"/v1/videos","method":"POST"},"openai":{"path":"/v1/chat/completions","method":"POST"}}`
	for _, name := range []string{"moma-seedance-2.0", "nm-moma-seedance-2.0"} {
		t.Run(name, func(t *testing.T) {
			record := model.Model{ModelName: name, Description: "original", Endpoints: legacy, Status: 1}
			require.NoError(t, db.Create(&record).Error)
			record.Description = "edited description"
			var response struct {
				Success bool
				Message string
				Data    model.Model
			}
			modelManagementRequest(t, UpdateModelMeta, http.MethodPut, "/api/models/", record, &response)
			require.True(t, response.Success, response.Message)
			require.JSONEq(t, want, response.Data.Endpoints)
			var persisted model.Model
			require.NoError(t, db.First(&persisted, record.Id).Error)
			require.Equal(t, "edited description", persisted.Description)
			require.JSONEq(t, want, persisted.Endpoints)
			// An unchanged repeat save must also work.
			modelManagementRequest(t, UpdateModelMeta, http.MethodPut, "/api/models/", persisted, &response)
			require.True(t, response.Success, response.Message)
		})
	}
	normalized, err := model.NormalizeModelEndpoints(legacy)
	require.NoError(t, err)
	require.JSONEq(t, want, normalized)
	again, err := model.NormalizeModelEndpoints(normalized)
	require.NoError(t, err)
	require.Equal(t, normalized, again)
	for _, raw := range []string{`{"unknown":{}}`, `{"openai":{"path":null}}`, `{"openai":{"path":123}}`, `{"openai":{"method":"TRACE"}}`, `{"openai":"https://example.invalid"}`} {
		_, err := model.NormalizeModelEndpoints(raw)
		require.Error(t, err, raw)
	}
}
