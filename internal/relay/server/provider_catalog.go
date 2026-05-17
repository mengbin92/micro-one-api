package server

type oneAPIProviderCatalogEntry struct {
	Type                 string   `json:"type"`
	Name                 string   `json:"name"`
	DefaultBaseURL       string   `json:"default_base_url"`
	Models               []string `json:"models"`
	RequiredConfigFields []string `json:"required_config_fields"`
	Adapter              string   `json:"adapter"`
	NativeSupported      bool     `json:"native_supported"`
	OpenAICompatible     bool     `json:"openai_compatible"`
}

func oneAPIChannelModelsByType() map[string][]string {
	models := make(map[string][]string, len(oneAPIProviderCatalog()))
	for _, entry := range oneAPIProviderCatalog() {
		models[entry.Type] = entry.Models
	}
	return models
}

func oneAPIProviderCatalogMetadata() map[string]oneAPIProviderCatalogEntry {
	metadata := make(map[string]oneAPIProviderCatalogEntry, len(oneAPIProviderCatalog()))
	for _, entry := range oneAPIProviderCatalog() {
		metadata[entry.Type] = entry
	}
	return metadata
}

func oneAPIProviderCatalog() []oneAPIProviderCatalogEntry {
	return []oneAPIProviderCatalogEntry{
		{
			Type:             "1",
			Name:             "OpenAI",
			DefaultBaseURL:   "https://api.openai.com/v1",
			Models:           []string{"gpt-4o-mini", "gpt-4o", "gpt-4-turbo", "gpt-3.5-turbo", "text-embedding-3-small", "text-embedding-3-large"},
			Adapter:          "openai_compatible",
			OpenAICompatible: true,
		},
		{
			Type:             "2",
			Name:             "Anthropic",
			DefaultBaseURL:   "https://api.anthropic.com",
			Models:           []string{"claude-3-5-sonnet-20241022", "claude-3-5-haiku-20241022", "claude-3-opus-20240229", "claude-3-sonnet-20240229", "claude-3-haiku-20240307"},
			Adapter:          "native",
			NativeSupported:  true,
			OpenAICompatible: false,
		},
		{
			Type:             "3",
			Name:             "Gemini",
			DefaultBaseURL:   "https://generativelanguage.googleapis.com",
			Models:           []string{"gemini-pro", "gemini-pro-vision"},
			Adapter:          "native",
			NativeSupported:  true,
			OpenAICompatible: false,
		},
		{
			Type:                 "5",
			Name:                 "Azure OpenAI",
			Models:               []string{"gpt-4o-mini", "gpt-4o", "gpt-4-turbo", "gpt-35-turbo"},
			RequiredConfigFields: []string{"base_url", "api_version"},
			Adapter:              "native",
			NativeSupported:      true,
			OpenAICompatible:     false,
		},
		{
			Type:             "6",
			Name:             "DeepSeek",
			DefaultBaseURL:   "https://api.deepseek.com/v1",
			Models:           []string{"deepseek-chat", "deepseek-reasoner"},
			Adapter:          "openai_compatible",
			OpenAICompatible: true,
		},
		{
			Type:             "7",
			Name:             "Mistral AI",
			DefaultBaseURL:   "https://api.mistral.ai/v1",
			Models:           []string{"mistral-large-latest", "mistral-small-latest"},
			Adapter:          "openai_compatible",
			OpenAICompatible: true,
		},
		{
			Type:             "8",
			Name:             "Zhipu",
			DefaultBaseURL:   "https://open.bigmodel.cn/api/paas/v4",
			Models:           []string{"glm-4", "glm-4v", "glm-3-turbo"},
			Adapter:          "openai_compatible",
			OpenAICompatible: true,
		},
		{
			Type:             "9",
			Name:             "Moonshot",
			DefaultBaseURL:   "https://api.moonshot.cn/v1",
			Models:           []string{"moonshot-v1-8k", "moonshot-v1-32k", "moonshot-v1-128k"},
			Adapter:          "openai_compatible",
			OpenAICompatible: true,
		},
		{
			Type:             "11",
			Name:             "Cohere",
			DefaultBaseURL:   "https://api.cohere.com/compatibility/v1",
			Models:           []string{"command-r", "command-r-plus"},
			Adapter:          "openai_compatible",
			OpenAICompatible: true,
		},
		{
			Type:             "13",
			Name:             "Tongyi",
			DefaultBaseURL:   "https://dashscope.aliyuncs.com/compatible-mode/v1",
			Models:           []string{"qwen-turbo", "qwen-plus", "qwen-max", "qwen-max-longcontext", "text-embedding-v1"},
			Adapter:          "openai_compatible",
			OpenAICompatible: true,
		},
		{
			Type:                 "14",
			Name:                 "Tencent Hunyuan",
			DefaultBaseURL:       "https://hunyuan.tencentcloudapi.com",
			Models:               []string{"hunyuan-lite", "hunyuan-standard", "hunyuan-standard-256K", "hunyuan-pro", "hunyuan-vision", "hunyuan-embedding"},
			RequiredConfigFields: []string{"secret_id", "secret_key"},
			Adapter:              "native_required",
		},
		{
			Type:             "22",
			Name:             "VoyageAI",
			DefaultBaseURL:   "https://api.voyageai.com/v1",
			Models:           []string{"voyage-3", "voyage-3-lite", "voyage-code-3"},
			Adapter:          "native",
			NativeSupported:  true,
			OpenAICompatible: false,
		},
		{
			Type:             "23",
			Name:             "OpenRouter",
			DefaultBaseURL:   "https://openrouter.ai/api/v1",
			Models:           []string{"openai/gpt-4o-mini", "openai/gpt-4o", "anthropic/claude-3.5-sonnet"},
			Adapter:          "openai_compatible",
			OpenAICompatible: true,
		},
		{
			Type:             "24",
			Name:             "SiliconFlow",
			DefaultBaseURL:   "https://api.siliconflow.cn/v1",
			Models:           []string{"deepseek-ai/DeepSeek-V3", "deepseek-ai/DeepSeek-R1", "Qwen/Qwen2.5-72B-Instruct"},
			Adapter:          "openai_compatible",
			OpenAICompatible: true,
		},
		{
			Type:             "25",
			Name:             "Ollama",
			DefaultBaseURL:   "http://localhost:11434/v1",
			Models:           []string{"codellama:7b-instruct", "llama2:7b", "llama2:latest", "llama3:latest", "phi3:latest", "qwen:0.5b-chat", "qwen:7b"},
			Adapter:          "openai_compatible",
			OpenAICompatible: true,
		},
		{
			Type:                 "26",
			Name:                 "Cloudflare Workers AI",
			DefaultBaseURL:       "https://api.cloudflare.com/client/v4/accounts",
			Models:               []string{"@cf/meta/llama-3.1-8b-instruct", "@cf/meta/llama-3-8b-instruct", "@cf/mistral/mistral-7b-instruct-v0.2-lora"},
			RequiredConfigFields: []string{"account_id"},
			Adapter:              "native_required",
		},
		{
			Type:                 "27",
			Name:                 "VertexAI",
			Models:               []string{"gemini-1.5-pro", "gemini-1.5-flash", "claude-3-5-sonnet-v2@20241022"},
			RequiredConfigFields: []string{"region", "vertex_ai_project_id", "vertex_ai_adc"},
			Adapter:              "native_required",
		},
		{
			Type:           "28",
			Name:           "Replicate",
			DefaultBaseURL: "https://api.replicate.com/v1",
			Models:         []string{"black-forest-labs/flux-1.1-pro", "meta/meta-llama-3.1-405b-instruct", "mistralai/mixtral-8x7b-instruct-v0.1"},
			Adapter:        "native_required",
		},
		{
			Type:                 "29",
			Name:                 "Baidu Qianfan",
			DefaultBaseURL:       "https://aip.baidubce.com",
			Models:               []string{"ERNIE-4.0-8K", "ERNIE-3.5-8K", "ERNIE-Speed-8K", "Embedding-V1"},
			RequiredConfigFields: []string{"api_key", "secret_key"},
			Adapter:              "native_required",
		},
		{
			Type:                 "30",
			Name:                 "Xunfei Spark",
			Models:               []string{"Spark-Lite", "Spark-Pro", "Spark-Pro-128K", "Spark-Max", "Spark-Max-32K", "Spark-4.0-Ultra"},
			RequiredConfigFields: []string{"app_id", "api_key", "api_secret", "api_version"},
			Adapter:              "native_required",
		},
		{
			Type:             "31",
			Name:             "Volcano Doubao",
			DefaultBaseURL:   "https://ark.cn-beijing.volces.com/api/v3",
			Models:           []string{"Doubao-pro-128k", "Doubao-pro-32k", "Doubao-pro-4k", "Doubao-lite-128k", "Doubao-lite-32k", "Doubao-lite-4k", "Doubao-embedding"},
			Adapter:          "openai_compatible",
			OpenAICompatible: true,
		},
	}
}
