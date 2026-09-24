package plugins

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDoubaoMobileAudioDefault(t *testing.T) {
	source, err := Source("doubao")
	require.NoError(t, err)
	p, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "doubao"})
	require.NoError(t, err)
	for _, model := range []string{"moma-seedance-2.0", "nm-moma-seedance-2.0"} {
		require.Contains(t, p.Meta.Models, model)
		for _, tc := range []struct {
			name, base string
			field      string
			value      any
			want       any
		}{
			{"default", "https://zhenze-huhehaote.cmecloud.cn", "", nil, true},
			{"native-off", "https://zhenze-huhehaote.cmecloud.cn", "generate_audio", false, false},
			{"form-off", "https://zhenze-huhehaote.cmecloud.cn/api/v3", "video_generate_audio", "false", false},
			{"native-on", "https://zhenze-huhehaote.cmecloud.cn", "generate_audio", true, true},
			{"other-domain", "https://example.com", "", nil, nil},
			{"domain-suffix", "https://cmecloud.cn.example.com", "", nil, nil},
		} {
			t.Run(model+"/"+tc.name, func(t *testing.T) {
				body := map[string]any{"model": model, "prompt": "landscape", "seconds": 5}
				if tc.field != "" {
					body[tc.field] = tc.value
				}
				result, err := p.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"model": model, "requestBody": body, "upstreamModel": "doubao-seedance-2.0", "baseUrl": tc.base, "apiKey": "offline-test"})
				require.NoError(t, err)
				wire, err := common.Marshal(result)
				require.NoError(t, err)
				var decoded struct {
					Body map[string]any `json:"body"`
				}
				require.NoError(t, common.Unmarshal(wire, &decoded))
				require.Equal(t, tc.want, decoded.Body["generate_audio"])
			})
		}
	}
}
