package common

import "github.com/QuantumNous/new-api/constant"

// GetEndpointTypesByChannelType 获取渠道最优先端点类型（所有的渠道都支持 OpenAI 端点）
func GetEndpointTypesByChannelType(channelType int, modelName string) []constant.EndpointType {
	var endpointTypes []constant.EndpointType
	switch channelType {
	case constant.ChannelTypeJina:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeJinaRerank}
	//case constant.ChannelTypeMidjourney, constant.ChannelTypeMidjourneyPlus:
	//	endpointTypes = []constant.EndpointType{constant.EndpointTypeMidjourney}
	//case constant.ChannelTypeSunoAPI:
	//	endpointTypes = []constant.EndpointType{constant.EndpointTypeSuno}
	//case constant.ChannelTypeKling:
	//	endpointTypes = []constant.EndpointType{constant.EndpointTypeKling}
	//case constant.ChannelTypeJimeng:
	//	endpointTypes = []constant.EndpointType{constant.EndpointTypeJimeng}
	case constant.ChannelTypeAws:
		fallthrough
	case constant.ChannelTypeAnthropic:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI}
	case constant.ChannelTypeVertexAi:
		fallthrough
	case constant.ChannelTypeGemini:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeGemini, constant.EndpointTypeOpenAI}
	case constant.ChannelTypeOpenRouter: // OpenRouter 只支持 OpenAI 端点
		endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAI}
	case constant.ChannelTypeXai:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse}
	case constant.ChannelTypeSora:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAIVideo}
	case constant.ChannelTypeSub2API, constant.ChannelTypeNewAPI:
		if IsVideoGenerationModel(modelName) {
			endpointTypes = []constant.EndpointType{
				constant.EndpointTypeOpenAIVideo,
				constant.EndpointTypeOpenAI,
			}
		} else if IsImageGenerationModel(modelName) {
			endpointTypes = []constant.EndpointType{
				constant.EndpointTypeImageGeneration,
				constant.EndpointTypeOpenAI,
			}
		} else {
			endpointTypes = []constant.EndpointType{
				constant.EndpointTypeOpenAI,
				constant.EndpointTypeOpenAIResponse,
				constant.EndpointTypeOpenAIResponseCompact,
				constant.EndpointTypeAnthropic,
				constant.EndpointTypeGemini,
				constant.EndpointTypeOpenAIAlphaSearch,
			}
		}
	case constant.ChannelTypeCodex:
		endpointTypes = []constant.EndpointType{
			constant.EndpointTypeOpenAIResponse,
			constant.EndpointTypeOpenAIResponseCompact,
			constant.EndpointTypeOpenAIAlphaSearch,
		}
	default:
		if IsOpenAIResponseOnlyModel(modelName) {
			endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAIResponse}
		} else {
			endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAI}
		}
	}
	if IsImageGenerationModel(modelName) {
		hasImage := false
		for _, et := range endpointTypes {
			if et == constant.EndpointTypeImageGeneration {
				hasImage = true
				break
			}
		}
		if !hasImage {
			endpointTypes = append([]constant.EndpointType{constant.EndpointTypeImageGeneration}, endpointTypes...)
		}
	}
	if IsVideoGenerationModel(modelName) {
		hasVideo := false
		for _, et := range endpointTypes {
			if et == constant.EndpointTypeOpenAIVideo {
				hasVideo = true
				break
			}
		}
		if !hasVideo {
			endpointTypes = append([]constant.EndpointType{constant.EndpointTypeOpenAIVideo}, endpointTypes...)
		}
	}
	return endpointTypes
}
