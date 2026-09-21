package service

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"strings"
)

// ExtractAICCAssetIDs parses media references without database or network access.
func ExtractAICCAssetIDs(request any) ([]string, error) {
	// Inspect decoded trees directly. Serializing unrelated plugin billing facts
	// here misclassifies non-finite usage values as asset authorization failures.
	// Typed legacy values are normalized locally by the visitor below.
	assetIDs := make([]string, 0)
	depth, visited := 0, 0
	var visit func(any, bool) error
	visit = func(value any, media bool) error {
		depth++
		visited++
		defer func() { depth-- }()
		if depth > 128 || visited > 100000 {
			return fmt.Errorf("task reference structure exceeds validation limits")
		}
		switch typed := value.(type) {
		case string:
			text := strings.TrimSpace(typed)
			if media && (strings.HasPrefix(text, "asset://") || strings.HasPrefix(text, "asset-")) {
				id := strings.TrimPrefix(text, "asset://")
				if !strings.HasPrefix(id, "asset-") || strings.ContainsAny(id, "/?#%\\\" \t\r\n") {
					return fmt.Errorf("invalid AICC asset reference")
				}
				assetIDs = append(assetIDs, id)
			}
		case nil, bool, float32, float64, int, int32, int64, uint, uint32, uint64:
			// Numeric validity belongs to the host billing validator.
		case []string:
			for _, item := range typed {
				if err := visit(item, media); err != nil {
					return err
				}
			}
		case []any:
			for _, item := range typed {
				if err := visit(item, media); err != nil {
					return err
				}
			}
		case map[string]any:
			for key, item := range typed {
				switch key {
				case "prompt", "text", "description", "negative_prompt":
					continue
				case "metadata":
					// Normalize typed metadata before recognizing a JSON-encoded string.
					switch item.(type) {
					case string, []string, map[string]any, []any, nil:
					default:
						wire, err := common.Marshal(item)
						if err != nil {
							return err
						}
						var normalizedMetadata any
						if err := common.Unmarshal(wire, &normalizedMetadata); err != nil {
							return err
						}
						item = normalizedMetadata
					}
					if values, ok := item.([]string); ok {
						for _, text := range values {
							var decoded any
							if err := common.Unmarshal([]byte(text), &decoded); err != nil {
								return fmt.Errorf("invalid task metadata")
							}
							if err := visit(decoded, media); err != nil {
								return err
							}
						}
						continue
					}
					if text, ok := item.(string); ok {
						var decoded any
						if err := common.Unmarshal([]byte(text), &decoded); err != nil {
							return fmt.Errorf("invalid task metadata")
						}
						item = decoded
					}
				case "image", "images", "input_reference", "input_reference[]", "asset_id", "liveness_asset_id", "image_url", "video_url", "audio_url", "first_frame_url", "last_frame_url", "video_reference", "video_reference[]", "audio_reference", "audio_reference[]":
					if err := visit(item, true); err != nil {
						return err
					}
					continue
				}
				if err := visit(item, media); err != nil {
					return err
				}
			}
		default:
			wire, err := common.Marshal(value)
			if err != nil {
				return err
			}
			var normalized any
			if err := common.Unmarshal(wire, &normalized); err != nil {
				return err
			}
			return visit(normalized, media)
		}
		return nil
	}
	if err := visit(request, false); err != nil {
		return nil, err
	}
	return assetIDs, nil
}

// AICCAssetChannelFilter narrows routing to the original channel configuration.
// A configuration snapshot is not proof of a shared upstream video account.
func AICCAssetChannelFilter(userID int, assetIDs []string) (dto.ChannelFilter, error) {
	filter := dto.ChannelFilter{Kind: dto.FilterAICCAssetAllowedChannels}
	if userID <= 0 || len(assetIDs) == 0 {
		return filter, fmt.Errorf("invalid AICC asset owner or references")
	}
	var expected model.AICCBinding
	seen := make(map[string]bool)
	for _, id := range assetIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		binding, err := model.GetOwnedAICCAssetBinding(userID, id)
		if err != nil {
			return filter, err
		}
		if binding.ChannelID <= 0 || len(binding.AICCAccountID) != 64 {
			return filter, fmt.Errorf("AICC asset has no valid binding")
		}
		if expected.ChannelID != 0 && expected.AICCAccountID != binding.AICCAccountID {
			return filter, fmt.Errorf("AICC assets have mixed account bindings")
		}
		expected = binding
	}
	var channels []model.Channel
	if err := model.DB.Where("status = ?", common.ChannelStatusEnabled).Find(&channels).Error; err != nil {
		return filter, err
	}
	for _, channel := range channels {
		cfg, err := aiccConfigFromChannel(channel)
		if err != nil {
			continue
		}
		if (cfg.AccountVerified || (cfg.ChannelPinned && channel.Id == expected.ChannelID)) && cfg.AICCAccountID == expected.AICCAccountID {
			filter.AllowedChannelIDs = append(filter.AllowedChannelIDs, channel.Id)
		}
	}
	if len(filter.AllowedChannelIDs) == 0 {
		return filter, fmt.Errorf("素材账号没有已核实的视频渠道")
	}
	return filter, nil
}
